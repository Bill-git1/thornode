package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// TestScannerReorgAbortPersistenceReleasesDeliveryReservation drives a reorg to
// the point where a reorg checkpoint is attached to the TxIn, then takes the
// failure path BlockScanner takes when Observer cannot persist the scanned
// transactions: AbortPersistence() instead of FinalizePersistence().
//
// attachReorgCheckpoint sets reorgDeliveryPending, which hard-blocks every
// subsequent FetchTxs with ErrUnavailableBlock. If the abort closure ever
// stopped clearing that flag, the XMR scanner would wedge permanently after any
// observer hiccup, since BlockScanner leaves its scan position un-advanced and
// simply re-fetches the same block.
func TestScannerReorgAbortPersistenceReleasesDeliveryReservation(t *testing.T) {
	t.Parallel()

	h97 := testXMRHash("abort-h97")
	h98 := testXMRHash("abort-h98")
	old99 := testXMRHash("abort-old99")
	old100 := testXMRHash("abort-old100")
	new99 := testXMRHash("abort-new99")
	new100 := testXMRHash("abort-new100")
	h101 := testXMRHash("abort-h101")
	droppedTx := testXMRHash("abort-tx-drop")
	keptTx := testXMRHash("abort-tx-keep")

	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, h101, new100),
	}
	txExists := map[string]bool{
		keptTx:    true,
		droppedTx: false,
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
			path := strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/tx/")
			path = strings.TrimSuffix(path, "/exists")
			path, _ = url.PathUnescape(path)
			_ = json.NewEncoder(w).Encode(sidecar.TxExistsResp{Exists: txExists[path]})
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

	store := NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{Height: 98, BlockHash: h98, PreviousHash: h97},
		{Height: 99, BlockHash: old99, PreviousHash: h98, CustomerTransactions: []string{keptTx}},
		{Height: 100, BlockHash: old100, PreviousHash: old99, CustomerTransactions: []string{droppedTx}},
	} {
		if err = store.Set(meta); err != nil {
			t.Fatalf("seed reorg metadata at %d: %v", meta.Height, err)
		}
	}

	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	errataQueue := make(chan stypes.ErrataBlock, 4)
	scanner.SetErrataQueue(errataQueue)

	txIn, err := scanner.FetchTxs(101, 101)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if txIn.AbortPersistence == nil {
		t.Fatal("expected a reorg delivery checkpoint with an abort closure")
	}
	if txIn.FinalizePersistence == nil {
		t.Fatal("expected a reorg delivery checkpoint with a finalize closure")
	}

	// While the delivery reservation is held, no further scanning is allowed.
	if _, pendingErr := scanner.FetchTxs(101, 101); !errors.Is(pendingErr, btypes.ErrUnavailableBlock) {
		t.Fatalf("expected reorg delivery reservation to block re-fetch, got %v", pendingErr)
	}

	// Observer failed to persist: BlockScanner aborts and leaves its scan
	// position un-advanced, so the same height is fetched again.
	txIn.AbortPersistence()

	// Canonical metadata must still be unpublished; the journal is the only
	// durable record of the replay and stays retryable.
	preMeta, err := store.Get(100)
	if err != nil {
		t.Fatalf("get metadata after abort: %v", err)
	}
	if preMeta == nil || preMeta.BlockHash != old100 {
		t.Fatalf("abort must not publish canonical metadata, got %#v", preMeta)
	}
	journal, err := store.GetJournal()
	if err != nil {
		t.Fatalf("get journal after abort: %v", err)
	}
	if journal == nil || journal.Phase != reorgPhaseDelivered {
		t.Fatalf("abort must retain the delivered journal for retry, got %#v", journal)
	}

	retried, err := scanner.FetchTxs(101, 101)
	if err != nil {
		if errors.Is(err, btypes.ErrUnavailableBlock) {
			t.Fatal("AbortPersistence did not clear reorgDeliveryPending: scanner is wedged")
		}
		t.Fatalf("re-fetch after abort: %v", err)
	}
	if retried.FinalizePersistence == nil {
		t.Fatal("expected the retry to re-attach a reorg delivery checkpoint")
	}
	if err = retried.FinalizePersistence(); err != nil {
		t.Fatalf("finalize retried reorg checkpoint: %v", err)
	}

	// Delivery completed exactly once: canonical metadata published, journal gone.
	meta100, err := store.Get(100)
	if err != nil {
		t.Fatalf("get meta 100: %v", err)
	}
	if meta100 == nil || meta100.BlockHash != new100 || meta100.PreviousHash != new99 {
		t.Fatalf("unexpected canonical meta 100 after retry: %#v", meta100)
	}
	if journal, err = store.GetJournal(); err != nil || journal != nil {
		t.Fatalf("expected journal cleared after finalize, got journal=%#v err=%v", journal, err)
	}

	// The abort/retry cycle must not duplicate errata.
	select {
	case errata := <-errataQueue:
		if errata.Height != 100 {
			t.Fatalf("expected errata height 100, got %d", errata.Height)
		}
		if len(errata.Txs) != 1 || errata.Txs[0].TxID != common.TxID(droppedTx) {
			t.Fatalf("unexpected errata txs: %#v", errata.Txs)
		}
	default:
		t.Fatal("expected errata for the dropped tx")
	}
	select {
	case extra := <-errataQueue:
		t.Fatalf("abort/retry must not re-deliver errata: %#v", extra)
	default:
	}

	// And the scanner keeps making forward progress afterwards.
	if _, err = scanner.FetchTxs(101, 101); err != nil {
		t.Fatalf("scanner blocked after completed reorg delivery: %v", err)
	}
}
