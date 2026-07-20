package blockscanner

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "gopkg.in/check.v1"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

type failingRewindStorage struct {
	*MockScannerStorage
	err       error
	onFailure func()
}

func (s *failingRewindStorage) SetScanPos(block int64) error {
	if s.err != nil {
		if s.onFailure != nil {
			s.onFailure()
		}
		return s.err
	}
	return s.MockScannerStorage.SetScanPos(block)
}

func newCursorRewindUnitScanner(storage ScannerStorage, previousHeight, floor int64) *BlockScanner {
	return &BlockScanner{
		cfg: config.BifrostBlockScannerConfiguration{
			FloorBlockHeight: floor,
		},
		scannerStorage: storage,
		previousBlock:  previousHeight,
	}
}

func (s *BlockScannerExtraSuite) TestCursorRewindUsesNextHeightMinusOne(c *C) {
	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(199), IsNil)
	scanner := newCursorRewindUnitScanner(storage, 199, 0)

	// This is the production XMR recovery shape: a 72-block cursor rewind.
	c.Assert(scanner.rewindToNextHeight(200, 128), IsNil)
	c.Check(scanner.PreviousHeight(), Equals, int64(127))
	position, err := storage.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(127))
}

func (s *BlockScannerExtraSuite) TestCursorRewindRejectsNonBackwardHeights(c *C) {
	for _, nextHeight := range []int64{0, 200, 201} {
		storage := NewMockScannerStorage()
		c.Assert(storage.SetScanPos(199), IsNil)
		scanner := newCursorRewindUnitScanner(storage, 199, 0)

		c.Check(scanner.rewindToNextHeight(200, nextHeight), NotNil)
		c.Check(scanner.PreviousHeight(), Equals, int64(199))
		position, err := storage.GetScanPos()
		c.Assert(err, IsNil)
		c.Check(position, Equals, int64(199))
	}
}

func (s *BlockScannerExtraSuite) TestCursorRewindHardLimitBoundary(c *C) {
	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(999), IsNil)
	scanner := newCursorRewindUnitScanner(storage, 999, 0)

	// Exactly 730 blocks is permitted.
	c.Assert(scanner.rewindToNextHeight(1000, 270), IsNil)
	c.Check(scanner.PreviousHeight(), Equals, int64(269))

	storage = NewMockScannerStorage()
	c.Assert(storage.SetScanPos(999), IsNil)
	scanner = newCursorRewindUnitScanner(storage, 999, 0)

	// One block beyond the hard safety horizon is rejected without mutation.
	c.Check(scanner.rewindToNextHeight(1000, 269), NotNil)
	c.Check(scanner.PreviousHeight(), Equals, int64(999))
	position, err := storage.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(999))
}

func (s *BlockScannerExtraSuite) TestCursorRewindRespectsFloorWithoutClamping(c *C) {
	storage := NewMockScannerStorage()
	c.Assert(storage.SetScanPos(199), IsNil)
	scanner := newCursorRewindUnitScanner(storage, 199, 100)

	// next=floor+1 makes the durable previous height exactly the floor.
	c.Assert(scanner.rewindToNextHeight(200, 101), IsNil)
	c.Check(scanner.PreviousHeight(), Equals, int64(100))

	storage = NewMockScannerStorage()
	c.Assert(storage.SetScanPos(199), IsNil)
	scanner = newCursorRewindUnitScanner(storage, 199, 100)

	// next=floor would require previous=floor-1. Reject it; never clamp it.
	c.Check(scanner.rewindToNextHeight(200, 100), NotNil)
	c.Check(scanner.PreviousHeight(), Equals, int64(199))
	position, err := storage.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(199))
}

func (s *BlockScannerExtraSuite) TestCursorRewindStorageFailureLeavesRAMUnchanged(c *C) {
	underlying := NewMockScannerStorage()
	c.Assert(underlying.SetScanPos(199), IsNil)
	storage := &failingRewindStorage{
		MockScannerStorage: underlying,
		err:                errors.New("forced synchronous write failure"),
	}
	scanner := newCursorRewindUnitScanner(storage, 199, 0)

	c.Check(scanner.rewindToNextHeight(200, 128), ErrorMatches, ".*forced synchronous write failure")
	c.Check(scanner.PreviousHeight(), Equals, int64(199))
	position, err := underlying.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(199))
}

func (s *BlockScannerExtraSuite) TestCursorRewindSurvivesStorageRestart(c *C) {
	folder := c.MkDir()
	storage, err := NewBlockScannerStorage(folder, config.LevelDBOptions{})
	c.Assert(err, IsNil)
	scanner := newCursorRewindUnitScanner(storage, 199, 0)

	c.Assert(scanner.rewindToNextHeight(200, 128), IsNil)
	c.Assert(storage.Close(), IsNil)

	reopened, err := NewBlockScannerStorage(folder, config.LevelDBOptions{})
	c.Assert(err, IsNil)
	defer reopened.Close()
	position, err := reopened.GetScanPos()
	c.Assert(err, IsNil)
	c.Check(position, Equals, int64(127))
}

func (s *BlockScannerExtraSuite) TestRewindRequiredErrorExtractionAcceptsWrappedValueAndPointer(c *C) {
	nextHeight, ok := rewindRequiredNextHeight(fmt.Errorf("wrapped: %w", btypes.RewindRequiredError{NextHeight: 128}))
	c.Check(ok, Equals, true)
	c.Check(nextHeight, Equals, int64(128))

	nextHeight, ok = rewindRequiredNextHeight(fmt.Errorf("wrapped: %w", &btypes.RewindRequiredError{NextHeight: 64}))
	c.Check(ok, Equals, true)
	c.Check(nextHeight, Equals, int64(64))

	_, ok = rewindRequiredNextHeight(errors.New("ordinary fetch error"))
	c.Check(ok, Equals, false)
}

func (s *BlockScannerExtraSuite) TestScannerRecoveryExtractionAcceptsWrappedValueAndPointer(c *C) {
	want := btypes.ScannerRecoveryRequiredError{CurrentHeight: 200, NextHeight: 128, Reason: "test"}

	recovery, ok := scannerRecoveryRequired(fmt.Errorf("wrapped: %w", want))
	c.Assert(ok, Equals, true)
	c.Assert(recovery, NotNil)
	c.Check(*recovery, DeepEquals, want)

	recovery, ok = scannerRecoveryRequired(fmt.Errorf("wrapped: %w", &want))
	c.Assert(ok, Equals, true)
	c.Assert(recovery, NotNil)
	c.Check(*recovery, DeepEquals, want)

	_, ok = scannerRecoveryRequired(errors.New("ordinary fetch error"))
	c.Check(ok, Equals, false)
}

type rewindPrefetchFetcher struct {
	mu              sync.Mutex
	calls           map[int64]int
	chainHeight     int64
	errorAborts     atomic.Int64
	discardedAborts atomic.Int64
}

func newRewindPrefetchFetcher(chainHeight int64) *rewindPrefetchFetcher {
	return &rewindPrefetchFetcher{
		calls:       make(map[int64]int),
		chainHeight: chainHeight,
	}
}

func (f *rewindPrefetchFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *rewindPrefetchFetcher) FetchTxs(fetchHeight, _ int64) (stypes.TxIn, error) {
	f.mu.Lock()
	f.calls[fetchHeight]++
	call := f.calls[fetchHeight]
	f.mu.Unlock()

	if fetchHeight == 11 && call == 1 {
		return stypes.TxIn{
			AbortPersistence: func() {
				f.errorAborts.Add(1)
			},
		}, fmt.Errorf("sidecar cursor moved: %w", btypes.RewindRequiredError{NextHeight: 9})
	}
	if fetchHeight == 12 && call == 1 {
		return stypes.TxIn{
			AbortPersistence: func() {
				f.discardedAborts.Add(1)
			},
		}, nil
	}
	return stypes.TxIn{}, nil
}

func (f *rewindPrefetchFetcher) GetHeight() (int64, error) {
	return f.chainHeight, nil
}

func (f *rewindPrefetchFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (f *rewindPrefetchFetcher) callCount(height int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[height]
}

type rollbackPrefetchFetcher struct {
	mu              sync.Mutex
	calls           map[int64]int
	chainHeight     int64
	persisted       chan error
	discardedAborts atomic.Int64
}

func newRollbackPrefetchFetcher(chainHeight int64) *rollbackPrefetchFetcher {
	return &rollbackPrefetchFetcher{
		calls:       make(map[int64]int),
		chainHeight: chainHeight,
		persisted:   make(chan error, 1),
	}
}

func (f *rollbackPrefetchFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *rollbackPrefetchFetcher) FetchTxs(fetchHeight, _ int64) (stypes.TxIn, error) {
	f.mu.Lock()
	f.calls[fetchHeight]++
	call := f.calls[fetchHeight]
	f.mu.Unlock()

	if fetchHeight == 10 && call == 1 {
		return stypes.TxIn{
			Chain:     common.ETHChain,
			TxArray:   []*stypes.TxInItem{{Tx: "prefetch-rollback-test"}},
			Persisted: f.persisted,
		}, nil
	}
	if fetchHeight == 12 && call == 1 {
		return stypes.TxIn{
			AbortPersistence: func() {
				f.discardedAborts.Add(1)
			},
		}, nil
	}
	return stypes.TxIn{}, nil
}

func (f *rollbackPrefetchFetcher) GetHeight() (int64, error) {
	return f.chainHeight, nil
}

func (f *rollbackPrefetchFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (f *rollbackPrefetchFetcher) callCount(height int64) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[height]
}

func (s *BlockScannerExtraSuite) newRewindTestBridge(c *C) (*httptest.Server, thorclient.ThorchainBridge) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.RequestURI, thorclient.MimirEndpoint):
			_, _ = w.Write([]byte(`-1`))
		case strings.HasPrefix(r.RequestURI, "/thorchain/lastblock"):
			_, _ = w.Write([]byte(`[{"chain":"","lastobservedin":0,"lastsignedout":0,"thorchain":100}]`))
		case strings.HasPrefix(r.RequestURI, "/thorchain/inbound_addresses"):
			_, _ = w.Write([]byte(`[{"chain":"ETH","outbound_tx_size":"250","observed_fee_rate":"30"}]`))
		}
	}))
	return server, s.makeBridge(c, server)
}

func waitForFetchCalls(fetchCount func(int64) int, height int64, wanted int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fetchCount(height) >= wanted {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func (s *BlockScannerExtraSuite) TestTypedCursorRewindClearsStalePrefetch(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	storage := NewMockScannerStorage()
	fetcher := newRewindPrefetchFetcher(12)
	scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		StartBlockHeight:           9,
		BlockHeightDiscoverBackoff: 5 * time.Millisecond,
		ChainID:                    common.ETHChain,
		PrefetchBlocks:             3,
		MaxHealthyLag:              time.Hour,
	}, storage, m, bridge, fetcher)
	c.Assert(err, IsNil)

	scanner.Start(make(chan stypes.TxIn, 4), make(chan common.NetworkFee, 4))
	defer scanner.Stop()

	// Height 12 was fetched on the abandoned 10->11 cursor lineage. It must be
	// fetched a second time after the typed rewind restarts at height 9.
	c.Assert(waitForFetchCalls(fetcher.callCount, 12, 2), Equals, true)
	c.Check(fetcher.callCount(9) > 0, Equals, true)
	c.Check(fetcher.errorAborts.Load(), Equals, int64(1))
	c.Check(fetcher.discardedAborts.Load(), Equals, int64(1))
}

func (s *BlockScannerExtraSuite) TestRollbackChannelClearsStalePrefetch(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	storage := NewMockScannerStorage()
	fetcher := newRollbackPrefetchFetcher(12)
	scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		StartBlockHeight:           9,
		BlockHeightDiscoverBackoff: 5 * time.Millisecond,
		ChainID:                    common.ETHChain,
		PrefetchBlocks:             3,
		MaxHealthyLag:              time.Hour,
	}, storage, m, bridge, fetcher)
	c.Assert(err, IsNil)
	// Buffer the test's rollback channel so the request can be queued
	// deterministically while scanBlocks is parked waiting for persistence.
	// Production delivery semantics are exercised elsewhere; this test is about
	// clearing stale prefetch after a queued rollback is consumed.
	scanner.rollbackChan = make(chan int64, 1)

	scanner.Start(make(chan stypes.TxIn, 4), make(chan common.NetworkFee, 4))
	defer scanner.Stop()
	c.Assert(waitForFetchCalls(fetcher.callCount, 12, 1), Equals, true)

	// The scanner loop is parked in waitTxInPersistence for height 10, so it cannot
	// consume the height-11/12 prefetch until we release persistence below. Queue
	// the rollback first so the loop's top-level select cannot race with a sender
	// that has not yet been scheduled.
	scanner.rollbackChan <- 9
	fetcher.persisted <- nil

	// The prefetched height 12 from before rollback must not survive the move
	// back to height 9.
	c.Assert(waitForFetchCalls(fetcher.callCount, 12, 2), Equals, true)
	c.Check(fetcher.discardedAborts.Load(), Equals, int64(1))
}

type abortingErrorFetcher struct {
	aborts atomic.Int64
}

type persistenceFailureFetcher struct {
	mode            string
	calls           atomic.Int64
	failureObserved chan struct{}
	failureOnce     sync.Once
}

type emptyPersistenceFetcher struct {
	persisted chan error
	finalized atomic.Bool
}

func (f *emptyPersistenceFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *emptyPersistenceFetcher) FetchTxs(fetchHeight, _ int64) (stypes.TxIn, error) {
	if fetchHeight != 2 {
		return stypes.TxIn{}, nil
	}
	return stypes.TxIn{
		Chain:     common.ETHChain,
		Persisted: f.persisted,
		FinalizePersistence: func() error {
			f.finalized.Store(true)
			return nil
		},
	}, nil
}

func (f *emptyPersistenceFetcher) GetHeight() (int64, error) {
	return 2, nil
}

func (f *emptyPersistenceFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (s *BlockScannerExtraSuite) TestEmptyTxInPersistenceIsDeliveredAndAcknowledged(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	fetcher := &emptyPersistenceFetcher{persisted: make(chan error, 1)}
	scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		StartBlockHeight:           1,
		BlockHeightDiscoverBackoff: time.Millisecond,
		ChainID:                    common.ETHChain,
		MaxHealthyLag:              time.Hour,
	}, NewMockScannerStorage(), m, bridge, fetcher)
	c.Assert(err, IsNil)

	txQueue := make(chan stypes.TxIn, 1)
	scanner.Start(txQueue, make(chan common.NetworkFee, 1))
	defer scanner.Stop()

	select {
	case txIn := <-txQueue:
		c.Check(len(txIn.TxArray), Equals, 0)
		c.Assert(txIn.Persisted, NotNil)
		txIn.Persisted <- nil
	case <-time.After(time.Second):
		c.Fatal("empty TxIn persistence handshake was not enqueued")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && scanner.PreviousHeight() != 2 {
		time.Sleep(time.Millisecond)
	}
	c.Check(scanner.PreviousHeight(), Equals, int64(2))
	c.Check(fetcher.finalized.Load(), Equals, true)
}

func (f *persistenceFailureFetcher) signalFailure() {
	f.failureOnce.Do(func() { close(f.failureObserved) })
}

func (f *persistenceFailureFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *persistenceFailureFetcher) FetchTxs(int64, int64) (stypes.TxIn, error) {
	f.calls.Add(1)
	switch f.mode {
	case "observer_ack":
		persisted := make(chan error, 1)
		persisted <- errors.New("forced observer persistence failure")
		return stypes.TxIn{
			Chain:   common.ETHChain,
			TxArray: []*stypes.TxInItem{{Tx: "persistence-failure"}},
			AbortPersistence: func() {
				f.signalFailure()
			},
			Persisted: persisted,
		}, nil
	case "finalize":
		return stypes.TxIn{
			FinalizePersistence: func() error {
				f.signalFailure()
				return errors.New("forced finalize failure")
			},
		}, nil
	default:
		return stypes.TxIn{}, nil
	}
}

func (f *persistenceFailureFetcher) GetHeight() (int64, error) {
	return 2, nil
}

func (f *persistenceFailureFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (s *BlockScannerExtraSuite) TestPersistenceFailuresBackOffAndReportUnhealthy(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	for _, mode := range []string{"observer_ack", "finalize", "scan_pos"} {
		fetcher := &persistenceFailureFetcher{mode: mode, failureObserved: make(chan struct{})}
		var scannerStorage ScannerStorage = NewMockScannerStorage()
		if mode == "scan_pos" {
			scannerStorage = &failingRewindStorage{
				MockScannerStorage: NewMockScannerStorage(),
				err:                errors.New("forced scan position failure"),
				onFailure:          fetcher.signalFailure,
			}
		}
		scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
			StartBlockHeight:           1,
			BlockHeightDiscoverBackoff: 200 * time.Millisecond,
			ChainID:                    common.ETHChain,
			MaxHealthyLag:              time.Hour,
		}, scannerStorage, m, bridge, fetcher)
		c.Assert(err, IsNil, Commentf("mode %s", mode))

		scanner.Start(make(chan stypes.TxIn, 4), make(chan common.NetworkFee, 4))
		select {
		case <-fetcher.failureObserved:
		case <-time.After(time.Second):
			c.Fatalf("mode %s did not reach persistence failure", mode)
		}
		time.Sleep(30 * time.Millisecond)
		c.Check(fetcher.calls.Load(), Equals, int64(1), Commentf("mode %s retried without backoff", mode))
		c.Check(scanner.IsHealthy(), Equals, false, Commentf("mode %s reported healthy", mode))
		scanner.Stop()
	}
}

type terminalRecoveryFetcher struct {
	calls   atomic.Int64
	asValue bool
}

func (f *terminalRecoveryFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *terminalRecoveryFetcher) FetchTxs(fetchHeight, _ int64) (stypes.TxIn, error) {
	f.calls.Add(1)
	recovery := btypes.ScannerRecoveryRequiredError{
		CurrentHeight: fetchHeight,
		NextHeight:    fetchHeight - btypes.MaxCursorRewindBlocks - 1,
		Reason:        "test discontinuity",
	}
	if f.asValue {
		return stypes.TxIn{}, recovery
	}
	return stypes.TxIn{}, &recovery
}

func (f *terminalRecoveryFetcher) GetHeight() (int64, error) {
	return 1_000, nil
}

func (f *terminalRecoveryFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (s *BlockScannerExtraSuite) TestOperatorRecoveryErrorStopsRetryLoop(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	for _, asValue := range []bool{false, true} {
		fetcher := &terminalRecoveryFetcher{asValue: asValue}
		scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
			StartBlockHeight:           999,
			BlockHeightDiscoverBackoff: time.Millisecond,
			ChainID:                    common.XMRChain,
			MaxHealthyLag:              time.Hour,
		}, NewMockScannerStorage(), m, bridge, fetcher)
		c.Assert(err, IsNil)

		scanner.Start(make(chan stypes.TxIn, 1), make(chan common.NetworkFee, 1))
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) && fetcher.calls.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		time.Sleep(30 * time.Millisecond)
		c.Check(fetcher.calls.Load(), Equals, int64(1), Commentf("asValue=%t", asValue))
		c.Check(scanner.IsHealthy(), Equals, false, Commentf("asValue=%t", asValue))
		scanner.Stop()
	}
}

func (f *abortingErrorFetcher) FetchMemPool(int64) (stypes.TxIn, error) {
	return stypes.TxIn{}, nil
}

func (f *abortingErrorFetcher) FetchTxs(int64, int64) (stypes.TxIn, error) {
	return stypes.TxIn{
		AbortPersistence: func() {
			f.aborts.Add(1)
		},
	}, errors.New("fetch failed after reserving replay delivery")
}

func (f *abortingErrorFetcher) GetHeight() (int64, error) {
	return 2, nil
}

func (f *abortingErrorFetcher) GetNetworkFee() (uint64, uint64) {
	return 250, 30
}

func (s *BlockScannerExtraSuite) TestFetchErrorAbortsPersistenceReservation(c *C) {
	server, bridge := s.newRewindTestBridge(c)
	defer server.Close()

	fetcher := &abortingErrorFetcher{}
	scanner, err := NewBlockScanner(config.BifrostBlockScannerConfiguration{
		StartBlockHeight:           1,
		BlockHeightDiscoverBackoff: 5 * time.Millisecond,
		ChainID:                    common.ETHChain,
	}, NewMockScannerStorage(), m, bridge, fetcher)
	c.Assert(err, IsNil)

	scanner.Start(make(chan stypes.TxIn, 1), make(chan common.NetworkFee, 1))
	defer scanner.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && fetcher.aborts.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	c.Check(fetcher.aborts.Load() > 0, Equals, true)
}
