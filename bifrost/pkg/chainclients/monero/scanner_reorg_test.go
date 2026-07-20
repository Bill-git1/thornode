package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
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

func TestScannerFetchTxsDetectsReorgAndEmitsErrata(t *testing.T) {
	t.Parallel()

	h97 := testXMRHash("h97")
	h98 := testXMRHash("h98")
	old99 := testXMRHash("old99")
	old100 := testXMRHash("old100")
	new99 := testXMRHash("new99")
	new100 := testXMRHash("new100")
	h101 := testXMRHash("h101")
	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, h101, new100),
	}
	txExists := map[string]bool{
		"tx-keep": true,
		"tx-drop": false,
	}

	continuity := newTestScannerContinuity(101)
	var reorgStore *ReorgStore
	var sawPersistedRewindIntent atomic.Bool
	continuity.onRewind = func(_ sidecar.ScannerRewindReq) {
		if reorgStore == nil {
			t.Errorf("sidecar rewind ran before reorg store initialization")
			return
		}
		journal, err := reorgStore.GetJournal()
		if err != nil || journal == nil || journal.Phase != reorgPhaseDiscovered {
			t.Errorf("sidecar rewind ran without durable discovered intent: journal=%#v err=%v", journal, err)
			return
		}
		meta, err := reorgStore.Get(100)
		if err != nil || meta == nil || meta.BlockHash != old100 {
			t.Errorf("sidecar rewind ran after canonical metadata mutation: meta=%#v err=%v", meta, err)
			return
		}
		sawPersistedRewindIntent.Store(true)
	}
	frostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if continuity.handle(t, w, r) {
			return
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/"):
			rawHeight := strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/")
			height, err := strconv.ParseInt(rawHeight, 10, 64)
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
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{SignerExpectedNetwork: "mocknet"},
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

	reorgStore = NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{
			Height:               98,
			BlockHash:            h98,
			PreviousHash:         h97,
			CustomerTransactions: nil,
		},
		{
			Height:               99,
			BlockHash:            old99,
			PreviousHash:         h98,
			CustomerTransactions: []string{"tx-keep"},
		},
		{
			Height:               100,
			BlockHash:            old100,
			PreviousHash:         old99,
			CustomerTransactions: []string{"tx-drop"},
		},
	} {
		if setErr := reorgStore.Set(meta); setErr != nil {
			t.Fatalf("set reorg meta: %v", setErr)
		}
	}

	scanner := NewScanner(log.Logger, frostSigner, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(reorgStore)
	errataQueue := make(chan stypes.ErrataBlock, 2)
	scanner.SetErrataQueueWithAck(errataQueue)

	type fetchResult struct {
		txIn stypes.TxIn
		err  error
	}
	fetch := func() <-chan fetchResult {
		result := make(chan fetchResult, 1)
		go func() {
			txIn, fetchErr := scanner.FetchTxs(101, 101)
			result <- fetchResult{txIn: txIn, err: fetchErr}
		}()
		return result
	}

	// Production uses the acknowledged queue. A durable observer error must
	// fail FetchTxs and leave the replay journal ready to redrive.
	firstResult := fetch()
	firstErrata := <-errataQueue
	if firstErrata.Persisted == nil {
		t.Fatal("production errata delivery omitted acknowledgement channel")
	}
	select {
	case result := <-firstResult:
		t.Fatalf("FetchTxs returned before errata acknowledgement: %v", result.err)
	default:
	}
	firstErrata.Persisted <- errors.New("forced observer persistence failure")
	result := <-firstResult
	if result.err == nil || !strings.Contains(result.err.Error(), "forced observer persistence failure") {
		t.Fatalf("expected observer persistence failure, got %v", result.err)
	}
	failedJournal, err := reorgStore.GetJournal()
	if err != nil {
		t.Fatalf("load failed-delivery journal: %v", err)
	}
	if failedJournal == nil || failedJournal.Phase != reorgPhaseReplayed {
		t.Fatalf("failed acknowledged delivery must remain replayable: %#v", failedJournal)
	}

	// The next FetchTxs must redrive the same errata and remain blocked until
	// cleanup persistence is acknowledged.
	secondResult := fetch()
	secondErrata := <-errataQueue
	if secondErrata.DeliveryID != firstErrata.DeliveryID || !reflect.DeepEqual(secondErrata.Txs, firstErrata.Txs) {
		t.Fatalf("retry did not redrive exact errata: first=%#v second=%#v", firstErrata, secondErrata)
	}
	select {
	case result = <-secondResult:
		t.Fatalf("retry returned before errata acknowledgement: %v", result.err)
	default:
	}
	secondErrata.Persisted <- nil
	result = <-secondResult
	if result.err != nil {
		t.Fatalf("fetch txs after acknowledged retry: %v", result.err)
	}
	txIn := result.txIn
	if len(txIn.TxArray) != 0 {
		t.Fatalf("expected no observed transfers, got %d", len(txIn.TxArray))
	}
	if !sawPersistedRewindIntent.Load() {
		t.Fatal("expected durable discovered intent before sidecar rewind")
	}
	if rewindCalls, syncHeights := continuity.snapshot(); rewindCalls != 1 || !reflect.DeepEqual(syncHeights, []int64{99, 100, 101}) {
		t.Fatalf("unexpected sidecar reorg ordering: rewinds=%d sync_heights=%v", rewindCalls, syncHeights)
	}
	if txIn.FinalizePersistence == nil {
		t.Fatal("expected durable reorg checkpoint acknowledgement")
	}
	preAckMeta, preAckErr := reorgStore.Get(100)
	if preAckErr != nil {
		t.Fatalf("get pre-ack metadata: %v", preAckErr)
	}
	if preAckMeta == nil || preAckMeta.BlockHash != old100 {
		t.Fatalf("canonical metadata changed before delivery checkpoint: %#v", preAckMeta)
	}
	if _, pendingErr := scanner.FetchTxs(102, 102); !errors.Is(pendingErr, btypes.ErrUnavailableBlock) {
		t.Fatalf("expected future scan to wait for reorg delivery finalization, got %v", pendingErr)
	}
	if err = txIn.FinalizePersistence(); err != nil {
		t.Fatalf("finalize reorg checkpoint: %v", err)
	}

	if secondErrata.Height != 100 {
		t.Fatalf("expected errata height 100, got %d", secondErrata.Height)
	}
	expected := []stypes.ErrataTx{{
		TxID:  common.TxID("tx-drop"),
		Chain: common.XMRChain,
	}}
	if !reflect.DeepEqual(secondErrata.Txs, expected) {
		t.Fatalf("unexpected errata txs: %#v", secondErrata.Txs)
	}

	select {
	case extra := <-errataQueue:
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

func TestScannerReorgReplayFailureKeepsOldMetadataAndResumesJournal(t *testing.T) {
	h97 := testXMRHash("resume-h97")
	h98 := testXMRHash("resume-h98")
	old99 := testXMRHash("resume-old99")
	old100 := testXMRHash("resume-old100")
	new99 := testXMRHash("resume-new99")
	new100 := testXMRHash("resume-new100")
	h101 := testXMRHash("resume-h101")
	blocks := map[int64]sidecar.ChainBlock{
		98:  withBlockHeader(98, defaultCoinbaseValue, h98, h97),
		99:  withBlockHeader(99, defaultCoinbaseValue, new99, h98),
		100: withBlockHeader(100, defaultCoinbaseValue, new100, new99),
		101: withBlockHeader(101, defaultCoinbaseValue, h101, new100),
	}
	var failReplay atomic.Bool
	failReplay.Store(true)
	var height100Calls atomic.Int32
	continuity := newTestScannerContinuity(101)
	frostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
		if height == 100 {
			calls := height100Calls.Add(1)
			if failReplay.Load() && calls > 1 {
				http.Error(w, "transient replay failure", http.StatusServiceUnavailable)
				return
			}
		}
		block, ok := blocks[height]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(block))
	}))
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{
		SignerExpectedNetwork: "mocknet",
	}, frostServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatalf("open leveldb: %v", err)
	}
	defer db.Close()
	store := NewReorgStore(db)
	for _, meta := range []*reorgBlockMeta{
		{Height: 98, BlockHash: h98, PreviousHash: h97},
		{Height: 99, BlockHash: old99, PreviousHash: h98},
		{Height: 100, BlockHash: old100, PreviousHash: old99},
	} {
		if err = store.Set(meta); err != nil {
			t.Fatalf("set meta: %v", err)
		}
	}

	scanner := NewScanner(log.Logger, frostSigner, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	if _, err = scanner.FetchTxs(101, 101); err == nil {
		t.Fatal("expected replay failure")
	}
	meta100, err := store.Get(100)
	if err != nil {
		t.Fatalf("get meta: %v", err)
	}
	if meta100 == nil || meta100.BlockHash != old100 {
		t.Fatalf("replay failure must preserve old metadata, got %#v", meta100)
	}
	journal, err := store.GetJournal()
	if err != nil {
		t.Fatalf("get journal: %v", err)
	}
	if journal == nil || journal.Phase != reorgPhaseDiscovered {
		t.Fatalf("expected discovered journal after failure, got %#v", journal)
	}

	failReplay.Store(false)
	resumed, resumeErr := scanner.FetchTxs(101, 101)
	if resumeErr != nil {
		err = resumeErr
		t.Fatalf("resume replay: %v", err)
	}
	if resumed.FinalizePersistence == nil {
		t.Fatal("expected resumed replay checkpoint")
	}
	if err = resumed.FinalizePersistence(); err != nil {
		t.Fatalf("finalize resumed replay: %v", err)
	}
	meta100, err = store.Get(100)
	if err != nil {
		t.Fatalf("get resumed meta: %v", err)
	}
	if meta100 == nil || meta100.BlockHash != new100 {
		t.Fatalf("expected canonical metadata after complete replay, got %#v", meta100)
	}
	if rewindCalls, syncHeights := continuity.snapshot(); rewindCalls != 2 || !reflect.DeepEqual(syncHeights, []int64{99, 99, 100, 101}) {
		t.Fatalf("unexpected resumed sidecar replay: rewinds=%d sync_heights=%v", rewindCalls, syncHeights)
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
	journal, journalErr := store.GetJournal()
	if journalErr != nil {
		t.Fatalf("load journal: %v", journalErr)
	}
	if journal != nil {
		t.Fatalf("partial replay journal must not be committed: %#v", journal)
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
