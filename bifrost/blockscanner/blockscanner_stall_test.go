package blockscanner

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "gopkg.in/check.v1"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// armStall backdates the stall clock so the next failure at this height is
// treated as sustained, standing in for the hours of identical failures a
// genuinely wedged scanner accumulates.
func armStall(scanner *BlockScanner, height int64) {
	scanner.stallHeight = height
	scanner.stallSince = time.Now().Add(-24 * time.Hour)
}

// lastObservedServer serves just enough of the THORNode API for GetStartHeight
// and skipAheadIfStalled: /status for WaitToCatchUp and a per-chain lastblock.
func lastObservedServer(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.RequestURI, "/status"):
			_, _ = w.Write([]byte(`{"result":{"sync_info":{"catching_up":false}}}`))
		case strings.HasPrefix(r.RequestURI, "/thorchain/lastblock/ETH"):
			_, _ = w.Write([]byte(body))
		case strings.HasPrefix(r.RequestURI, "/thorchain/lastblock"):
			_, _ = w.Write([]byte(`[{"chain":"","last_observed_in":0,"last_signed_out":0,"thorchain":100}]`))
		}
	}))
}

func (s *BlockScannerExtraSuite) newStallScanner(c *C, server *httptest.Server, storage ScannerStorage, lag time.Duration) *BlockScanner {
	scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		StartBlockHeight:  1,
		ChainID:           common.ETHChain,
		MaxResumeBlockLag: lag,
	}, storage, m, s.makeBridge(c, server), DummyFetcher{})
	c.Assert(err, IsNil)
	return scanner
}

// The GAIA mainnet shape: the cursor is wedged on a block the node can no
// longer serve while the network has observed ~157k blocks past it.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledAdvancesWedgedCursor(c *C) {
	server := lastObservedServer(`[{"chain":"ETH","last_observed_in":160000,"last_signed_out":0,"thorchain":100}]`)
	defer server.Close()

	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(2800), IsNil)
	scanner := s.newStallScanner(c, server, storage, time.Hour)
	atomic.StoreInt64(&scanner.previousBlock, 2800)
	armStall(scanner, 2801)

	// ETH is 12s/block, so a 1h resume lag is 300 blocks: 160000-300.
	c.Check(scanner.skipAheadIfStalled(2801), Equals, true)
	c.Check(scanner.PreviousHeight(), Equals, int64(159700))

	// The durable cursor must move with the in-memory one, or a restart
	// would drop straight back into the wedged range.
	position, err := storage.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(159700))
}

// A scanner that is merely behind must keep scanning every block it has not
// seen; only a cursor outside the catch-up window may be skipped forward.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledIsForwardOnlyAndBounded(c *C) {
	server := lastObservedServer(`[{"chain":"ETH","last_observed_in":1000,"last_signed_out":0,"thorchain":100}]`)
	defer server.Close()

	for _, previous := range []int64{699, 700, 701, 1000, 5000} {
		storage := NewMockScannerStorage()
		c.Assert(storage.SetScanPos(previous), IsNil)
		scanner := s.newStallScanner(c, server, storage, time.Hour)
		atomic.StoreInt64(&scanner.previousBlock, previous)
		armStall(scanner, previous+1)

		// Catch-up window starts at 1000-300 = 700, so only 699 is stalled.
		expected := previous == 699
		c.Check(scanner.skipAheadIfStalled(previous+1), Equals, expected, Commentf("previous=%d", previous))
		if !expected {
			c.Check(scanner.PreviousHeight(), Equals, previous)
			position, err := storage.GetScanPos()
			c.Assert(err, IsNil)
			c.Check(position, Equals, previous)
		}
	}
}

// Without a resume lag configured there is no bound to apply, so the cursor
// must never move on its own.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledNoopWithoutResumeLag(c *C) {
	server := lastObservedServer(`[{"chain":"ETH","last_observed_in":160000,"last_signed_out":0,"thorchain":100}]`)
	defer server.Close()

	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(2800), IsNil)
	scanner := s.newStallScanner(c, server, storage, 0)
	atomic.StoreInt64(&scanner.previousBlock, 2800)
	armStall(scanner, 2801)

	c.Check(scanner.skipAheadIfStalled(2801), Equals, false)
	c.Check(scanner.PreviousHeight(), Equals, int64(2800))
}

// An unreadable consensus height is not evidence of a stall, so the cursor
// must stay put and the scanner must keep retrying.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledNoopOnBridgeFailure(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.RequestURI, "/status"):
			_, _ = w.Write([]byte(`{"result":{"sync_info":{"catching_up":false}}}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(2800), IsNil)
	scanner := s.newStallScanner(c, server, storage, time.Hour)
	atomic.StoreInt64(&scanner.previousBlock, 2800)
	armStall(scanner, 2801)

	c.Check(scanner.skipAheadIfStalled(2801), Equals, false)
	c.Check(scanner.PreviousHeight(), Equals, int64(2800))
}

// A failed durable write must leave the in-memory cursor alone, so the two
// never disagree about the next block to fetch.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledStorageFailureLeavesCursor(c *C) {
	server := lastObservedServer(`[{"chain":"ETH","last_observed_in":160000,"last_signed_out":0,"thorchain":100}]`)
	defer server.Close()

	underlying := NewMockScannerStorage()
	c.Assert(underlying.SetScanPos(2800), IsNil)
	storage := &failingRewindStorage{
		MockScannerStorage: underlying,
		err:                errors.New("forced synchronous write failure"),
	}
	scanner := s.newStallScanner(c, server, storage, time.Hour)
	atomic.StoreInt64(&scanner.previousBlock, 2800)
	armStall(scanner, 2801)

	c.Check(scanner.skipAheadIfStalled(2801), Equals, false)
	c.Check(scanner.PreviousHeight(), Equals, int64(2800))
	position, err := underlying.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(2800))
}

// One failed fetch is not a stall. The skip only unlocks once the same height
// has been failing for longer than MaxResumeBlockLag, so a transient RPC error
// on a scanner that happens to be behind cannot durably skip unscanned blocks.
func (s *BlockScannerExtraSuite) TestSkipAheadIfStalledRequiresSustainedFailure(c *C) {
	server := lastObservedServer(`[{"chain":"ETH","last_observed_in":160000,"last_signed_out":0,"thorchain":100}]`)
	defer server.Close()

	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(2800), IsNil)
	scanner := s.newStallScanner(c, server, storage, time.Hour)
	atomic.StoreInt64(&scanner.previousBlock, 2800)

	// First failure only starts the clock.
	c.Check(scanner.skipAheadIfStalled(2801), Equals, false)
	c.Check(scanner.skipAheadIfStalled(2801), Equals, false)
	c.Check(scanner.PreviousHeight(), Equals, int64(2800))

	// A failure at a different height restarts it, so progress never
	// accumulates toward a skip.
	scanner.stallSince = time.Now().Add(-24 * time.Hour)
	c.Check(scanner.skipAheadIfStalled(2802), Equals, false)
	c.Check(scanner.PreviousHeight(), Equals, int64(2800))

	// Only a sustained failure at one height skips.
	scanner.stallSince = time.Now().Add(-24 * time.Hour)
	c.Check(scanner.skipAheadIfStalled(2802), Equals, true)
	c.Check(scanner.PreviousHeight(), Equals, int64(159700))
}

// startStalledScanner runs a real scanner whose every fetch fails with err,
// pre-armed as if it had already been failing on its first height for a day,
// and returns the cursor it settles on.
func (s *BlockScannerExtraSuite) startStalledScanner(c *C, err error) int64 {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.RequestURI, thorclient.MimirEndpoint):
			_, _ = w.Write([]byte(`-1`))
		case strings.HasPrefix(r.RequestURI, "/status"):
			_, _ = w.Write([]byte(`{"result":{"sync_info":{"catching_up":false}}}`))
		default:
			_, _ = w.Write([]byte(`[{"chain":"ETH","last_observed_in":0,"last_signed_out":0,"thorchain":100}]`))
		}
	}))
	defer server.Close()

	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(2800), IsNil)
	scanner, scanErr := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		BlockScanProcessors:        1,
		HTTPRequestTimeout:         time.Second,
		BlockHeightDiscoverBackoff: time.Millisecond,
		BlockRetryInterval:         time.Millisecond,
		ChainID:                    common.ETHChain,
		MaxResumeBlockLag:          time.Hour,
	}, storage, m, &stallBridge{ThorchainBridge: s.makeBridge(c, server)}, &configFetcher{chainHeight: 1000000, fetchErr: err})
	c.Assert(scanErr, IsNil)
	armStall(scanner, 2801)

	scanner.Start(make(chan types.TxIn, 1), make(chan common.NetworkFee, 1))
	defer scanner.Stop()
	for i := 0; i < 200; i++ {
		if scanner.PreviousHeight() != 2800 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return scanner.PreviousHeight()
}

// stallBridge reports no consensus observed height on its first call, so
// GetStartHeight leaves the persisted cursor alone and the scan loop is the
// only thing that can move it.
type stallBridge struct {
	thorclient.ThorchainBridge
	calls int64
}

func (b *stallBridge) GetLastObservedInHeight(chain common.Chain) (int64, error) {
	if atomic.AddInt64(&b.calls, 1) == 1 {
		return 0, nil
	}
	return 160000, nil
}

func (s *BlockScannerExtraSuite) TestScanBlocksSkipsAheadOnSustainedFetchError(c *C) {
	c.Check(s.startStalledScanner(c, errTest), Equals, int64(159700))
}

// ErrUnavailableBlock is the chain clients' "retry this exact height" signal —
// Monero and XRP return it while holding per-height state — and the scan loop
// already classifies it as benign. It must never authorise a cursor skip.
func (s *BlockScannerExtraSuite) TestScanBlocksNeverSkipsOnUnavailableBlock(c *C) {
	c.Check(s.startStalledScanner(c, btypes.ErrUnavailableBlock), Equals, int64(2800))
}
