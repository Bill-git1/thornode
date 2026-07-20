package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestFetchTxsReturnsCursorRewindBeforeReorgMutation(t *testing.T) {
	nextHeight := uint64(50)
	block100Calls := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/chain/block/101":
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp{
				Height:       101,
				Reward:       defaultCoinbaseValue,
				Hash:         testBlockHash(101),
				PreviousHash: testBlockHash(100),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/chain/block/100":
			block100Calls <- struct{}{}
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp{
				Height:       100,
				Reward:       defaultCoinbaseValue,
				Hash:         testBlockHash(100),
				PreviousHash: testBlockHash(99),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "scanner continuity changed",
				"code":  "scanner_continuity_conflict",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/scanner/state":
			_ = json.NewEncoder(w).Encode(sidecar.ScannerState{
				ResetEpoch:       0,
				NextHeight:       &nextHeight,
				StrictAfterReset: false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new sidecar client: %v", err)
	}
	memStorage := storage.NewMemStorage()
	db, err := leveldb.Open(memStorage, nil)
	if err != nil {
		t.Fatalf("open reorg store: %v", err)
	}
	defer db.Close()
	reorgStore := NewReorgStore(db)
	stale := &reorgBlockMeta{
		Height:               100,
		BlockHash:            testStaleBlockHash(100),
		PreviousHash:         testBlockHash(99),
		CustomerTransactions: []string{"orphaned-tx"},
	}
	if err = reorgStore.Set(stale); err != nil {
		t.Fatalf("seed stale block metadata: %v", err)
	}

	scanner := NewScanner(
		log.Logger,
		client,
		&scannerMemoBridge{asgards: ttypes.Vaults{}},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)
	scanner.SetReorgStore(reorgStore)
	scanner.setCurrentHeight(100)
	errata := make(chan stypes.ErrataBlock, 1)
	scanner.SetErrataQueue(errata)

	txIn, err := scanner.FetchTxs(101, 101)
	var rewind *btypes.RewindRequiredError
	if !errors.As(err, &rewind) {
		t.Fatalf("expected typed cursor rewind, got %v", err)
	}
	if rewind.NextHeight != int64(nextHeight) {
		t.Fatalf("expected next height %d, got %d", nextHeight, rewind.NextHeight)
	}
	if len(txIn.TxArray) != 0 || txIn.Persisted != nil || txIn.FinalizePersistence != nil {
		t.Fatalf("rewind returned partial delivery state: %#v", txIn)
	}
	if scanner.CurrentHeight() != 100 {
		t.Fatalf("rewind advanced scanner height to %d", scanner.CurrentHeight())
	}
	select {
	case <-block100Calls:
		t.Fatal("reorg replay ran before cursor rewind was returned")
	default:
	}
	select {
	case got := <-errata:
		t.Fatalf("rewind emitted errata before rollback: %#v", got)
	default:
	}
	journal, err := reorgStore.GetJournal()
	if err != nil {
		t.Fatalf("load reorg journal: %v", err)
	}
	if journal != nil {
		t.Fatalf("rewind left a reorg journal: %#v", journal)
	}
	meta, err := reorgStore.Get(100)
	if err != nil {
		t.Fatalf("load stale metadata: %v", err)
	}
	if meta == nil || meta.BlockHash != stale.BlockHash {
		t.Fatalf("rewind mutated existing block metadata: %#v", meta)
	}
}

func TestMapScannerContinuityErrorRequiresOperatorForUnsafeDeficit(t *testing.T) {
	t.Parallel()

	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	for _, nextHeight := range []int64{
		0,
		1_000 - btypes.MaxCursorRewindBlocks - 1,
		1_000,
		1_001,
	} {
		err := scanner.mapScannerContinuityError(1_000, &sidecar.ScannerRewindRequiredError{
			RequestedHeight: 1_000,
			NextHeight:      nextHeight,
			ResetEpoch:      7,
		})
		var recovery *btypes.ScannerRecoveryRequiredError
		if !errors.As(err, &recovery) {
			t.Fatalf("next height %d must require operator recovery, got %v", nextHeight, err)
		}
		if recovery.CurrentHeight != 1_000 || recovery.NextHeight != nextHeight {
			t.Fatalf("unexpected operator recovery error: %+v", recovery)
		}
	}

	err := scanner.mapScannerContinuityError(1_000, &sidecar.ScannerRewindRequiredError{
		RequestedHeight: 1_000,
		NextHeight:      1_000 - btypes.MaxCursorRewindBlocks,
		ResetEpoch:      7,
	})
	var rewind *btypes.RewindRequiredError
	if !errors.As(err, &rewind) {
		t.Fatalf("bounded deficit must remain automatically recoverable, got %v", err)
	}
}

func TestClientVaultRegistrationSerializesWithScanner(t *testing.T) {
	requestSeen := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/monero/vaults/register" {
			http.NotFound(w, r)
			return
		}
		requestSeen <- struct{}{}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"registered":           true,
			"reset_epoch":          1,
			"expected_next_height": 100,
		})
	}))
	defer server.Close()

	sidecarClient, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatalf("new sidecar client: %v", err)
	}
	scanner := NewScanner(log.Logger, sidecarClient, nil, common.XMRChain, common.EmptyPubKey, 72)
	client := &Client{
		logger:  log.Logger,
		sidecar: sidecarClient,
		scanner: scanner,
		chain:   common.XMRChain,
	}
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)

	scanner.scanMu.Lock()
	done := make(chan error, 1)
	go func() { done <- client.ensureVaultRegistered(vaultPubKey) }()
	select {
	case <-requestSeen:
		t.Fatal("vault registration raced an in-progress scan")
	case <-time.After(100 * time.Millisecond):
	}
	scanner.scanMu.Unlock()

	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("vault registration did not resume after scan completed")
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("serialized vault registration: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serialized vault registration did not return")
	}
}
