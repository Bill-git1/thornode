package monero

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

func TestScannerFetchTxsDetectsReorgAndEmitsErrata(t *testing.T) {
	t.Parallel()
	sourceVault := testVaultPubKey(t, "scanner-reorg-source")
	sourceAddress, err := sourceVault.GetAddress(common.XMRChain)
	if err != nil {
		t.Fatalf("derive source address: %v", err)
	}
	replayedTxID := testXMRHash("scanner-reorg-replayed-observation")
	canonicalRelocatedTxID := testXMRHash("scanner-reorg-canonical-relocated")
	orphanedTxID := testXMRHash("scanner-reorg-orphaned-mempool")
	// ScannerState continuity validation requires 32-byte hex hashes, so these
	// fixtures must be canonical (the sibling no-common-ancestor test already is).
	h97 := testXMRHash("scanner-reorg-h97")
	h98 := testXMRHash("scanner-reorg-h98")
	h101 := testXMRHash("scanner-reorg-h101")
	new99 := testXMRHash("scanner-reorg-new99")
	new100 := testXMRHash("scanner-reorg-new100")
	old99 := testXMRHash("scanner-reorg-old99")
	old100 := testXMRHash("scanner-reorg-old100")

	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, h101, new100),
	}
	nextHeight := uint64(101)
	lastHeight := uint64(100)
	resetEpoch := uint64(0)
	lastHash := old100
	// last_parent_hash is validated alongside last_block_hash whenever
	// last_height is set, so the fake must keep the pair consistent.
	lastParentHash := old99
	var rewindReq sidecar.ScannerRewindReq
	var syncReqs []sidecar.SyncReq

	frostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/"):
			rawHeight := strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/")
			height, parseErr := strconv.ParseInt(rawHeight, 10, 64)
			if parseErr != nil {
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
			if strings.HasSuffix(path, "/exists") {
				_ = json.NewEncoder(w).Encode(sidecar.TxExistsResp{Exists: false})
				return
			}
			path = strings.TrimSuffix(path, "/location")
			path, _ = url.PathUnescape(path)
			if path != canonicalRelocatedTxID {
				http.Error(w, "transaction is not mined canonically", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.CanonicalTxLocationResp{
				TxHash:       canonicalRelocatedTxID,
				Height:       97,
				BlockHash:    testXMRHash("scanner-reorg-relocated-block"),
				PreviousHash: testXMRHash("scanner-reorg-relocated-parent"),
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/scanner/state":
			_ = json.NewEncoder(w).Encode(sidecar.ScannerState{
				ResetEpoch:       resetEpoch,
				NextHeight:       &nextHeight,
				LastHeight:       &lastHeight,
				LastBlockHash:    lastHash,
				LastParentHash:   lastParentHash,
				StrictAfterReset: resetEpoch > 0,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/rewind":
			if decodeErr := json.NewDecoder(r.Body).Decode(&rewindReq); decodeErr != nil {
				http.Error(w, decodeErr.Error(), http.StatusBadRequest)
				return
			}
			if rewindReq.ExpectedResetEpoch != resetEpoch ||
				rewindReq.ExpectedNextHeight != nextHeight ||
				rewindReq.TargetNextHeight != 99 ||
				rewindReq.AnchorHeight != 98 ||
				rewindReq.AnchorHash != h98 {
				http.Error(w, "invalid rewind request", http.StatusConflict)
				return
			}
			previous := nextHeight
			resetEpoch++
			nextHeight = 99
			lastHeight = 98
			lastHash = h98
			lastParentHash = h97
			_ = json.NewEncoder(w).Encode(sidecar.ScannerRewindResp{
				Rewound:            true,
				PreviousNextHeight: previous,
				NextHeight:         nextHeight,
				ResetEpoch:         resetEpoch,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			var req sidecar.SyncReq
			if decodeErr := json.NewDecoder(r.Body).Decode(&req); decodeErr != nil {
				http.Error(w, decodeErr.Error(), http.StatusBadRequest)
				return
			}
			syncReqs = append(syncReqs, req)
			if req.ResetEpoch == nil || *req.ResetEpoch != resetEpoch ||
				req.ExpectedNextHeight == nil || *req.ExpectedNextHeight != uint64(req.Height) ||
				uint64(req.Height) != nextHeight {
				http.Error(w, "scanner cursor mismatch", http.StatusConflict)
				return
			}
			blockAtHeight := blocks[req.Height]
			nextHeight = uint64(req.Height + 1)
			lastHeight = uint64(req.Height)
			lastHash = blockAtHeight.Hash
			lastParentHash = blockAtHeight.PreviousHash
			resp := sidecar.Transfers{}
			if req.Height == 99 {
				resp.Outgoing = []sidecar.Transfer{{
					TxID:                replayedTxID,
					Address:             testOutboundXMRDestination().String(),
					FromAddress:         sourceAddress.String(),
					VaultPubKey:         sourceVault.String(),
					Amount:              2 * moneroAtomicToThorchain,
					Fee:                 moneroAtomicToThorchain,
					Height:              99,
					Note:                "OUT:BTC:bc1qreplacement",
					Origin:              sidecar.OutgoingOriginLocalCombined,
					ObservationEligible: true,
				}}
			}
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	memStorage := storage.NewMemStorage()
	db, err := leveldb.Open(memStorage, nil)
	if err != nil {
		t.Fatalf("open in-memory leveldb: %v", err)
	}
	defer db.Close()

	reorgStore := NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{
			Height:               98,
			BlockHash:            h98,
			PreviousHash:         h97,
			CustomerTransactions: nil,
			ProvenanceComplete:   true,
		},
		{
			Height:               99,
			BlockHash:            old99,
			PreviousHash:         h98,
			CustomerTransactions: []string{canonicalRelocatedTxID},
			ProvenanceComplete:   true,
		},
		{
			Height:               100,
			BlockHash:            old100,
			PreviousHash:         old99,
			CustomerTransactions: []string{orphanedTxID},
			ProvenanceComplete:   true,
		},
	} {
		if setErr := reorgStore.Set(meta); setErr != nil {
			t.Fatalf("set reorg meta: %v", setErr)
		}
	}

	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)
	scanner.SetReorgStore(reorgStore)
	scanner.SetLocalSignedTxHandler(func(string) (bool, error) { return false, nil })
	scanner.SetClaimQuarantineHandler(func(string) error { return nil })
	errataQueue := make(chan stypes.ErrataBlock, 2)
	scanner.SetErrataQueue(errataQueue)
	storedErrata := make(chan stypes.ErrataBlock, 2)
	go func() {
		for errata := range errataQueue {
			if errata.OnStored != nil {
				errata.OnStored()
			}
			storedErrata <- errata
		}
	}()

	txIn, err := scanner.FetchTxs(101, 101)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 1 || txIn.TxArray[0].Tx != replayedTxID || txIn.TxArray[0].BlockHeight != 99 {
		t.Fatalf("expected replacement-height observation to be returned, got %#v", txIn.TxArray)
	}
	if amount := txIn.TxArray[0].Coins.GetCoin(common.XMRAsset).Amount.Uint64(); amount != 2 {
		t.Fatalf("replacement observation was handed off more than once: amount=%d", amount)
	}
	if got := []int64{syncReqs[0].Height, syncReqs[1].Height, syncReqs[2].Height}; !reflect.DeepEqual(got, []int64{99, 100, 101}) {
		t.Fatalf("expected bounded ascending replacement replay then current block, got %v", got)
	}
	if nextHeight != 102 || resetEpoch != 1 {
		t.Fatalf("expected scanner to advance without stalling, epoch=%d next=%d", resetEpoch, nextHeight)
	}

	select {
	case errata := <-storedErrata:
		if errata.Height != 100 {
			t.Fatalf("expected errata height 100, got %d", errata.Height)
		}
		expected := []stypes.ErrataTx{{
			TxID:  common.TxID(orphanedTxID),
			Chain: common.XMRChain,
		}}
		if !reflect.DeepEqual(errata.Txs, expected) {
			t.Fatalf("unexpected errata txs: %#v", errata.Txs)
		}
	default:
		t.Fatal("expected errata for dropped tx")
	}

	select {
	case extra := <-storedErrata:
		t.Fatalf("did not expect second errata block: %#v", extra)
	default:
	}

	meta100, err := reorgStore.Get(100)
	if err != nil {
		t.Fatalf("get meta 100: %v", err)
	}
	if meta100 == nil || meta100.BlockHash != new100 || meta100.PreviousHash != new99 {
		t.Fatalf("unexpected updated meta 100: %#v", meta100)
	}
	meta99, err := reorgStore.Get(99)
	if err != nil {
		t.Fatalf("get meta 99: %v", err)
	}
	if meta99 == nil || meta99.BlockHash != new99 || meta99.PreviousHash != h98 {
		t.Fatalf("unexpected updated meta 99: %#v", meta99)
	}
}

func TestScannerReorgWithoutCommonAncestorFailsClosed(t *testing.T) {
	old98 := testXMRHash("no-ancestor-old98")
	old99 := testXMRHash("no-ancestor-old99")
	old100 := testXMRHash("no-ancestor-old100")
	new98 := testXMRHash("no-ancestor-new98")
	new99 := testXMRHash("no-ancestor-new99")
	new100 := testXMRHash("no-ancestor-new100")
	new101 := testXMRHash("no-ancestor-new101")
	blocks := map[int64]sidecar.ChainBlock{
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, new98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, new101, new100),
	}
	continuity := newTestScannerContinuity(101)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if continuity.handle(t, w, r) {
			return
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/") {
			http.NotFound(w, r)
			return
		}
		height, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/"), 10, 64)
		if err != nil {
			http.Error(w, "invalid height", http.StatusBadRequest)
			return
		}
		block, ok := blocks[height]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(block))
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerExpectedNetwork: "mocknet",
	}, server.URL)
	if err != nil {
		t.Fatalf("new signer client: %v", err)
	}
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatalf("open leveldb: %v", err)
	}
	defer db.Close()
	store := NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{Height: 99, BlockHash: old99, PreviousHash: old98},
		{Height: 100, BlockHash: old100, PreviousHash: old99},
	} {
		if err = store.Set(meta); err != nil {
			t.Fatalf("set metadata: %v", err)
		}
	}

	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 2)
	scanner.SetReorgStore(store)
	_, err = scanner.FetchTxs(101, 101)
	if err == nil || !strings.Contains(err.Error(), "no common ancestor") {
		t.Fatalf("expected no-common-ancestor failure, got %v", err)
	}
	meta, err := store.Get(100)
	if err != nil || meta == nil || meta.BlockHash != old100 {
		t.Fatalf("old metadata must remain intact, meta=%#v err=%v", meta, err)
	}
}

func withBlockHeader(height int64, reward uint64, hash, prevHash string) sidecar.ChainBlock {
	return sidecar.ChainBlock{
		Height:       height,
		Reward:       reward,
		Hash:         hash,
		PreviousHash: prevHash,
	}
}

// A reorg replay derives orphan errata by diffing recorded metadata against the
// canonical replacement. When metadata is missing for a replaced height the loop
// skips it, which is correct only when nothing still depends on that height:
// PruneBefore and Delete both refuse to drop metadata that an unacknowledged
// receipt, catch-up anchor, or relocation watch still needs. A hole underneath
// outstanding work therefore cannot come from ordinary retention, and silently
// skipping it would drop that height's orphan errata. It must fail closed.
func TestScannerReorgFailsClosedOnMetadataHoleWithOutstandingWork(t *testing.T) {
	h97 := testXMRHash("contiguity-h97")
	h98 := testXMRHash("contiguity-h98")
	new99 := testXMRHash("contiguity-new99")
	new100 := testXMRHash("contiguity-new100")
	new101 := testXMRHash("contiguity-new101")
	old99 := testXMRHash("contiguity-old99")
	old100 := testXMRHash("contiguity-old100")

	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, new101, new100),
	}
	continuity := newTestScannerContinuity(101)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if continuity.handle(t, w, r) {
			return
		}
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/") {
			http.NotFound(w, r)
			return
		}
		height, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/"), 10, 64)
		if err != nil {
			http.Error(w, "invalid height", http.StatusBadRequest)
			return
		}
		block, ok := blocks[height]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(block))
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerExpectedNetwork: "mocknet",
	}, server.URL)
	if err != nil {
		t.Fatalf("new signer client: %v", err)
	}
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatalf("open leveldb: %v", err)
	}
	defer db.Close()
	store := NewReorgStore(db)

	// 98 is the common ancestor, 100 is stale so the fork is discovered, and 99
	// is the hole: no metadata at all, but an unacknowledged observation receipt
	// still references it. PutObservationReceipt writes only the receipt key, so
	// height 99 keeps outstanding work with no metadata behind it.
	for _, meta := range []*reorgBlockMeta{
		{Height: 98, BlockHash: h98, PreviousHash: h97},
		{Height: 100, BlockHash: old100, PreviousHash: old99},
	} {
		if err = store.Set(meta); err != nil {
			t.Fatalf("set metadata: %v", err)
		}
	}
	if err = store.PutObservationReceipt(
		&reorgBlockMeta{Height: 99, BlockHash: old99, PreviousHash: h98},
		[]*stypes.TxInItem{scannerReceiptItem("contiguity-outstanding", 99)},
	); err != nil {
		t.Fatalf("seed outstanding receipt: %v", err)
	}
	if meta, metaErr := store.Get(99); metaErr != nil || meta != nil {
		t.Fatalf("height 99 must be a metadata hole, got meta=%#v err=%v", meta, metaErr)
	}

	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)

	_, err = scanner.FetchTxs(101, 101)
	if err == nil || !strings.Contains(err.Error(), "work is still outstanding") {
		t.Fatalf("metadata hole under outstanding work must fail closed, got %v", err)
	}
	if !strings.Contains(err.Error(), "99") {
		t.Fatalf("failure must name the offending height, got %v", err)
	}

	// Fail-closed means fail-clean: the stale metadata must survive for
	// inspection rather than being half-replaced by the aborted replay.
	meta100, metaErr := store.Get(100)
	if metaErr != nil || meta100 == nil || meta100.BlockHash != old100 {
		t.Fatalf("aborted replay must not publish canonical metadata: meta=%#v err=%v", meta100, metaErr)
	}
}
