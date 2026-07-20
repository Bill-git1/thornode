package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// buildReorgJournal walks downward from the scan height and SKIPS heights with
// no stored metadata as long as no mismatch has been seen yet. If a mismatch is
// then found deeper, the skipped height sits inside the replay range, and the
// resulting Heights slice never reaches height-1.
//
// replayReorgJournal requires Heights to be exactly the contiguous range
// (anchor, height), so such a journal fails its contiguity check and returns
// ErrUnavailableBlock on EVERY attempt: the scanner stalls on this height
// forever, silently, because ErrUnavailableBlock is the "block not ready yet"
// signal and is deliberately not logged as an error.
//
// Seed a gap at 100 with a mismatch at 99 and assert we fail closed with a
// diagnosable error rather than stalling.
func TestBuildReorgJournalFailsClosedOnLeadingMetadataGap(t *testing.T) {
	t.Parallel()

	h97 := testXMRHash("gap-h97")
	h98 := testXMRHash("gap-h98")
	old99 := testXMRHash("gap-old99")
	new99 := testXMRHash("gap-new99")
	new100 := testXMRHash("gap-new100")
	h101 := testXMRHash("gap-h101")

	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, h101, new100),
	}

	continuity := newTestScannerContinuity(101)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if continuity.handle(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/"):
			height, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/"), 10, 64)
			if err != nil {
				http.Error(w, "invalid height", http.StatusBadRequest)
				return
			}
			block, ok := blocks[height]
			if !ok {
				http.Error(w, "unknown height", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(block))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/tx/"):
			_ = json.NewEncoder(w).Encode(sidecar.TxExistsResp{Exists: false})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{SignerExpectedNetwork: "mocknet"},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new sidecar client: %v", err)
	}

	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatalf("open in-memory leveldb: %v", err)
	}
	defer db.Close()

	// 98 matches canonical (the common ancestor), 99 is a mismatch (old fork
	// hash), and 100 is DELIBERATELY absent: the leading metadata gap.
	store := NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{Height: 98, BlockHash: h98, PreviousHash: h97},
		{Height: 99, BlockHash: old99, PreviousHash: h98},
	} {
		if err = store.Set(meta); err != nil {
			t.Fatalf("seed reorg metadata at %d: %v", meta.Height, err)
		}
	}

	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)

	// Reached in production via the rediscover paths (a journal that no longer
	// matches canonical) rather than top-level detection, which needs metadata
	// at height-1 and so cannot see this state. Drive the builder directly.
	journal, err := scanner.buildReorgJournal(101)
	if err == nil {
		t.Fatalf("expected a fail-closed error for the truncated replay range, got journal %#v", journal)
	}
	// The scanner must not treat this as "block not ready": that is the silent,
	// unlogged retry path, and it would stall this height forever.
	if errors.Is(err, btypes.ErrUnavailableBlock) {
		t.Fatalf("metadata gap must not stall as ErrUnavailableBlock, got %v", err)
	}
	if !strings.Contains(err.Error(), "not contiguous") {
		t.Fatalf("expected a diagnosable contiguity error, got %v", err)
	}

	// A journal built with no gap must still succeed, so the new guard cannot
	// be satisfied by simply rejecting every reorg.
	if err = store.Set(&reorgBlockMeta{
		Height: 100, BlockHash: testXMRHash("gap-old100"), PreviousHash: old99,
	}); err != nil {
		t.Fatalf("seed reorg metadata at 100: %v", err)
	}
	journal, err = scanner.buildReorgJournal(101)
	if err != nil {
		t.Fatalf("contiguous reorg must still build a journal: %v", err)
	}
	if journal == nil {
		t.Fatal("expected a journal for the contiguous reorg")
	}
	if want := []int64{99, 100}; len(journal.Heights) != len(want) ||
		journal.Heights[0] != want[0] || journal.Heights[1] != want[1] {
		t.Fatalf("expected replay heights %v, got %v", want, journal.Heights)
	}
	if journal.AnchorHeight != 98 {
		t.Fatalf("expected common ancestor 98, got %d", journal.AnchorHeight)
	}
}

func TestValidateReorgReplayRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		heights    []int64
		nextHeight int64
		wantReason string
	}{
		{
			name:       "contiguous range joins next block",
			heights:    []int64{99, 100},
			nextHeight: 101,
		},
		{
			name:       "truncated range cannot join next block",
			heights:    []int64{99},
			nextHeight: 101,
			wantReason: "cannot join scan height 101",
		},
		{
			name:       "internal gap is structural",
			heights:    []int64{99, 101},
			nextHeight: 102,
			wantReason: "non-contiguous",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			journal := &reorgJournal{
				Phase:        reorgPhaseDiscovered,
				AnchorHeight: 98,
				AnchorHash:   testXMRHash("range-anchor"),
				Heights:      tc.heights,
			}
			err := validateReorgReplayRange(journal, tc.nextHeight)
			if tc.wantReason == "" {
				if err != nil {
					t.Fatalf("valid replay range rejected: %v", err)
				}
				return
			}

			var recovery *btypes.ScannerRecoveryRequiredError
			if !errors.As(err, &recovery) {
				t.Fatalf("structural mismatch must require operator recovery, got %v", err)
			}
			if errors.Is(err, btypes.ErrUnavailableBlock) {
				t.Fatalf("structural mismatch must not be classified as transient: %v", err)
			}
			if !strings.Contains(recovery.Reason, tc.wantReason) {
				t.Fatalf("expected reason containing %q, got %q", tc.wantReason, recovery.Reason)
			}
		})
	}
}

func TestReplayReorgJournalRequiresRecoveryWhenSidecarReturnsWrongHeight(t *testing.T) {
	t.Parallel()

	anchorHash := testXMRHash("wrong-height-anchor")
	wrongBlock := withBlockHeader(
		100,
		defaultCoinbaseValue,
		testXMRHash("wrong-height-block"),
		anchorHash,
	)
	continuity := newTestScannerContinuity(101)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if continuity.handle(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/chain/block/99":
			// A sidecar must return the exact requested height. Returning a
			// valid-looking adjacent block is structural corruption, not a
			// transient reorg, and must stop BlockScanner recovery retries.
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(wrongBlock))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{SignerExpectedNetwork: "mocknet"},
		server.URL,
	)
	if err != nil {
		t.Fatalf("new sidecar client: %v", err)
	}
	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	journal := &reorgJournal{
		Phase:        reorgPhaseDiscovered,
		AnchorHeight: 98,
		AnchorHash:   anchorHash,
		Heights:      []int64{99, 100},
	}
	nextBlock := withBlockHeader(
		101,
		defaultCoinbaseValue,
		testXMRHash("wrong-height-next"),
		testXMRHash("wrong-height-parent"),
	)

	_, err = scanner.replayReorgJournal(journal, 101, nextBlock)
	var recovery *btypes.ScannerRecoveryRequiredError
	if !errors.As(err, &recovery) {
		t.Fatalf("wrong replay height must require operator recovery, got %v", err)
	}
	if recovery.CurrentHeight != 101 || recovery.NextHeight != 99 {
		t.Fatalf("unexpected recovery bounds: %+v", recovery)
	}
	if !strings.Contains(recovery.Reason, "requested height 99") ||
		!strings.Contains(recovery.Reason, "returned height 100") {
		t.Fatalf("unexpected recovery reason: %q", recovery.Reason)
	}
	if errors.Is(err, btypes.ErrUnavailableBlock) {
		t.Fatalf("wrong replay height must not be retried as transient: %v", err)
	}
	rewindCalls, _ := continuity.snapshot()
	if rewindCalls != 1 {
		t.Fatalf("expected one guarded sidecar rewind before replay, got %d", rewindCalls)
	}
}
