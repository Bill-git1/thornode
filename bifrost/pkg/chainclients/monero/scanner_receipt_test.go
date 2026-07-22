package monero

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	btypes "gitlab.com/thorchain/thornode/v3/bifrost/blockscanner/types"
	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
)

func newScannerReceiptStore(t *testing.T) *ReorgStore {
	t.Helper()
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewReorgStore(db)
}

func scannerReceiptItem(seed string, height int64) *stypes.TxInItem {
	return &stypes.TxInItem{
		BlockHeight: height,
		Tx:          testXMRHash(seed),
		Memo:        "OUT:BTC:bc1qreceipt",
		Sender:      "receipt-source",
		To:          "receipt-destination",
		Coins:       common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(5))),
	}
}

func TestScannerCatchupReceiptsAccumulateAndRequireStoredTerminal(t *testing.T) {
	store := newScannerReceiptStore(t)
	marker := scannerCatchupMarker{StartHeight: 10, TargetHeight: 10, AnchorHeight: 9, AnchorHash: "h9"}
	if err := store.BeginCatchup(marker); err != nil {
		t.Fatal(err)
	}
	first := scannerReceiptItem("catchup-first-vault", 10)
	completed := scannerCatchupMarker{StartHeight: 11, TargetHeight: 10, AnchorHeight: 10, AnchorHash: "h10"}
	meta := &reorgBlockMeta{Height: 10, BlockHash: "h10", PreviousHash: "h9"}
	if err := store.CheckpointCatchup(completed, meta, []*stypes.TxInItem{first}); err != nil {
		t.Fatal(err)
	}

	// A second older-vault enrollment scans only that new vault. Same-block
	// checkpointing must merge, not replace, the first vault's receipt.
	if err := store.BeginCatchup(marker); err != nil {
		t.Fatal(err)
	}
	second := scannerReceiptItem("catchup-second-vault", 10)
	if err := store.CheckpointCatchup(completed, meta, []*stypes.TxInItem{second}); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ListObservationReceipts()
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || len(claims[0].entry.Items) != 2 {
		t.Fatalf("overlapping registration lost accumulated observations: %#v", claims)
	}

	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	items := append([]*stypes.TxInItem(nil), claims[0].entry.Items...)
	scanner.attachObservationReceiptBarrier(items, claims, &completed)
	items[0].OnObservedTxStored()
	items[1].OnObservedTxRejected(errors.New("observer filter rejected item"))
	claims, err = store.ListObservationReceipts()
	if err != nil || len(claims) != 1 {
		t.Fatalf("rejection cleared durable catch-up receipt: claims=%d err=%v", len(claims), err)
	}
	if got, markerErr := store.GetCatchupMarker(); markerErr != nil || got == nil || !got.completed() {
		t.Fatalf("rejection cleared completed marker: marker=%#v err=%v", got, markerErr)
	}

	// A fresh Observer attempt which fsyncs every item clears receipts and the
	// completed marker only at the shared terminal barrier.
	items = append([]*stypes.TxInItem(nil), claims[0].entry.Items...)
	scanner.attachObservationReceiptBarrier(items, claims, &completed)
	for _, item := range items {
		item.OnObservedTxStored()
	}
	claims, err = store.ListObservationReceipts()
	if err != nil || len(claims) != 0 {
		t.Fatalf("stored terminal did not clear receipts: claims=%d err=%v", len(claims), err)
	}
	if got, markerErr := store.GetCatchupMarker(); markerErr != nil || got != nil {
		t.Fatalf("stored terminal did not clear marker: marker=%#v err=%v", got, markerErr)
	}
}

func TestScannerReceiptOldCallbackCannotDeleteReplacement(t *testing.T) {
	store := newScannerReceiptStore(t)
	oldMeta := &reorgBlockMeta{Height: 20, BlockHash: "old20", PreviousHash: "h19"}
	if err := store.PutObservationReceipt(oldMeta, []*stypes.TxInItem{scannerReceiptItem("old-receipt", 20)}); err != nil {
		t.Fatal(err)
	}
	oldClaims, err := store.ListObservationReceipts()
	if err != nil {
		t.Fatal(err)
	}

	newMeta := &reorgBlockMeta{Height: 20, BlockHash: "new20", PreviousHash: "h19"}
	newItem := scannerReceiptItem("replacement-receipt", 20)
	if err = store.PutObservationReceipt(newMeta, []*stypes.TxInItem{newItem}); err != nil {
		t.Fatal(err)
	}
	if err = store.AckObservationReceipts(oldClaims, nil); err == nil {
		t.Fatal("stale callback unexpectedly acknowledged a replacement receipt")
	}
	claims, err := store.ListObservationReceipts()
	if err != nil || len(claims) != 1 || claims[0].entry.BlockHash != "new20" || claims[0].entry.Items[0].Tx != newItem.Tx {
		t.Fatalf("stale callback deleted replacement receipt: %#v err=%v", claims, err)
	}
}

func TestReorgJournalPruneRetainsReceiptAndCatchupAnchor(t *testing.T) {
	store := newScannerReceiptStore(t)
	for height := int64(1); height <= 100; height++ {
		if err := store.Set(&reorgBlockMeta{
			Height: height, BlockHash: "h" + strconv.FormatInt(height, 10), PreviousHash: "parent",
		}); err != nil {
			t.Fatal(err)
		}
	}
	protectedMeta := &reorgBlockMeta{Height: 2, BlockHash: "h2", PreviousHash: "parent"}
	if err := store.PutObservationReceipt(
		protectedMeta, []*stypes.TxInItem{scannerReceiptItem("protected-receipt", 2)},
	); err != nil {
		t.Fatal(err)
	}
	marker := scannerCatchupMarker{StartHeight: 4, TargetHeight: 5, AnchorHeight: 3, AnchorHash: "h3"}
	if err := store.BeginCatchup(marker); err != nil {
		t.Fatal(err)
	}

	pruned, err := store.PruneBefore(90)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 87 { // 1..89, excluding receipt height 2 and marker anchor 3.
		t.Fatalf("unexpected prune count: %d", pruned)
	}
	for _, height := range []int64{2, 3, 90, 100} {
		meta, getErr := store.Get(height)
		if getErr != nil || meta == nil {
			t.Fatalf("protected/current height %d was pruned: meta=%#v err=%v", height, meta, getErr)
		}
	}
	for _, height := range []int64{1, 4, 50, 89} {
		meta, getErr := store.Get(height)
		if getErr != nil || meta != nil {
			t.Fatalf("obsolete height %d survived prune: meta=%#v err=%v", height, meta, getErr)
		}
	}
}

func TestDurableErrataWaitsForObserverTerminal(t *testing.T) {
	queue := make(chan stypes.ErrataBlock, 1)
	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetErrataQueue(queue)
	errata := []stypes.ErrataTx{{TxID: common.TxID(testXMRHash("durable-errata")), Chain: common.XMRChain}}

	result := make(chan error, 1)
	go func() { result <- scanner.deliverReorgErrata(42, errata) }()
	block := <-queue
	select {
	case err := <-result:
		t.Fatalf("errata returned before Observer durability terminal: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	rejected := errors.New("attestation persistence failed")
	block.OnRejected(rejected)
	if err := <-result; err == nil || !strings.Contains(err.Error(), rejected.Error()) {
		t.Fatalf("observer rejection was not propagated: %v", err)
	}

	go func() { result <- scanner.deliverReorgErrata(43, errata) }()
	block = <-queue
	block.OnStored()
	if err := <-result; err != nil {
		t.Fatalf("stored errata did not complete: %v", err)
	}
}

func TestPendingErrataDeliveryDoesNotPermanentlyQuarantine(t *testing.T) {
	queue := make(chan stypes.ErrataBlock, 1)
	store := newScannerReceiptStore(t)
	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetErrataQueue(queue)
	scanner.SetReorgStore(store)
	quarantines := 0
	scanner.SetClaimQuarantineHandler(func(string) error {
		quarantines++
		return nil
	})
	txID := testXMRHash("pending-errata-does-not-quarantine")

	result := make(chan error, 1)
	go func() {
		result <- scanner.deliverReorgErrata(44, []stypes.ErrataTx{{
			TxID: common.TxID(txID), Chain: common.XMRChain,
		}})
	}()
	block := <-queue
	block.OnStored()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if quarantines != 0 {
		t.Fatalf("pending errata permanently quarantined signed claim: calls=%d", quarantines)
	}
	if reverted, err := store.IsRevertedTx(txID); err != nil || reverted {
		t.Fatalf("pending errata permanently quarantined scanner TxID: reverted=%t err=%v", reverted, err)
	}
}

func TestExactOrphanReceiptAcknowledgesWithoutPermanentQuarantine(t *testing.T) {
	store := newScannerReceiptStore(t)
	item := scannerReceiptItem("exact-orphan-terminal", 45)
	meta := &reorgBlockMeta{Height: 45, BlockHash: "h45", PreviousHash: "h44"}
	if err := store.PutObservationReceipt(meta, []*stypes.TxInItem{item}); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ListObservationReceipts()
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	quarantines := 0
	scanner.SetClaimQuarantineHandler(func(string) error {
		quarantines++
		return nil
	})
	handoff := append([]*stypes.TxInItem(nil), claims[0].entry.Items...)
	scanner.attachObservationReceiptBarrier(handoff, claims, nil)
	handoff[0].OnObservedTxRejected(fmt.Errorf("observer removed orphan: %w", stypes.ErrObservedTxOrphaned))

	remaining, err := store.ListObservationReceipts()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("exact orphan receipt was not acknowledged: remaining=%d err=%v", len(remaining), err)
	}
	if quarantines != 0 {
		t.Fatalf("exact orphan rejection permanently quarantined signed claim: calls=%d", quarantines)
	}
	if reverted, err := store.IsRevertedTx(item.Tx); err != nil || reverted {
		t.Fatalf("exact orphan rejection permanently quarantined scanner TxID: reverted=%t err=%v", reverted, err)
	}
}

func TestNotApplicableReceiptAcknowledgesWithoutPermanentQuarantine(t *testing.T) {
	store := newScannerReceiptStore(t)
	item := scannerReceiptItem("not-applicable-terminal", 46)
	meta := &reorgBlockMeta{Height: 46, BlockHash: "h46", PreviousHash: "h45"}
	if err := store.PutObservationReceipt(meta, []*stypes.TxInItem{item}); err != nil {
		t.Fatal(err)
	}
	claims, err := store.ListObservationReceipts()
	if err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	quarantines := 0
	scanner.SetClaimQuarantineHandler(func(string) error {
		quarantines++
		return nil
	})
	handoff := append([]*stypes.TxInItem(nil), claims[0].entry.Items...)
	scanner.attachObservationReceiptBarrier(handoff, claims, nil)
	handoff[0].OnObservedTxRejected(fmt.Errorf(
		"historical vault did not include this node: %w",
		stypes.ErrObservedTxNotApplicable,
	))

	remaining, err := store.ListObservationReceipts()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("not-applicable receipt was not acknowledged: remaining=%d err=%v", len(remaining), err)
	}
	if quarantines != 0 {
		t.Fatalf("not-applicable rejection quarantined signed claim: calls=%d", quarantines)
	}
	if reverted, err := store.IsRevertedTx(item.Tx); err != nil || reverted {
		t.Fatalf("not-applicable rejection quarantined scanner TxID: reverted=%t err=%v", reverted, err)
	}
}

func TestFutureRelocationWatchBindsProcessedJournalAndErratasOnDisappearance(t *testing.T) {
	store := newScannerReceiptStore(t)
	txID := testXMRHash("future-remine")
	droppedTxID := testXMRHash("future-remine-dropped")
	blockHash := testXMRHash("future-block")
	previousHash := testXMRHash("future-parent")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/exists") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(sidecar.TxExistsResp{Exists: false})
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/location") {
			http.NotFound(w, r)
			return
		}
		if strings.Contains(r.URL.Path, droppedTxID) {
			http.Error(w, "not canonically mined", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sidecar.CanonicalTxLocationResp{
			TxHash: txID, Height: 50, BlockHash: blockHash, PreviousHash: previousHash,
		})
	}))
	defer server.Close()
	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	scanner.SetLocalSignedTxHandler(func(string) (bool, error) { return false, nil })
	scanner.SetClaimQuarantineHandler(func(string) error { return nil })

	for _, watched := range []string{txID, droppedTxID} {
		if err = store.WatchRelocation(scannerRelocationWatch{
			TxID: watched, OriginalHeight: 40, LocationHeight: 50,
			BlockHash: blockHash, PreviousHash: previousHash,
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The future transaction remains watched until its canonical height has a
	// processed journal entry; location alone must not make it disappear.
	if err = scanner.reconcileRelocationWatches(); err == nil {
		t.Fatal("missing errata queue should fail closed for the disappeared watch")
	}
	watches, err := store.ListRelocationWatches(0)
	if err != nil || len(watches) != 2 {
		t.Fatalf("failed reconciliation mutated watches: %#v err=%v", watches, err)
	}

	queue := make(chan stypes.ErrataBlock, 1)
	scanner.SetErrataQueue(queue)
	errataStored := make(chan struct{})
	go func() {
		block := <-queue
		block.OnStored()
		close(errataStored)
	}()
	if err = store.Set(&reorgBlockMeta{
		Height: 50, BlockHash: blockHash, PreviousHash: previousHash, ProvenanceComplete: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err = scanner.reconcileRelocationWatches(); err != nil {
		t.Fatal(err)
	}
	<-errataStored
	watches, err = store.ListRelocationWatches(0)
	if err != nil || len(watches) != 0 {
		t.Fatalf("terminal relocation watches not cleared: %#v err=%v", watches, err)
	}
	meta, err := store.Get(50)
	if err != nil || meta == nil || !reflect.DeepEqual(meta.CustomerTransactions, []string{txID}) {
		t.Fatalf("future remine was not carried into processed journal: %#v err=%v", meta, err)
	}
}

func TestMempoolOrphanWatchSurvivesRestartUntilCanonicalJournalBinds(t *testing.T) {
	store := newScannerReceiptStore(t)
	txID := testXMRHash("pool-orphan")
	blockHash := testXMRHash("pool-remine-block")
	previousHash := testXMRHash("pool-remine-parent")
	var stateMu sync.Mutex
	mined := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateMu.Lock()
		isMined := mined
		stateMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/location") && !isMined:
			http.Error(w, "pool-only", http.StatusNotFound)
		case strings.HasSuffix(r.URL.Path, "/location"):
			_ = json.NewEncoder(w).Encode(sidecar.CanonicalTxLocationResp{
				TxHash: txID, Height: 60, BlockHash: blockHash, PreviousHash: previousHash,
			})
		case strings.HasSuffix(r.URL.Path, "/exists"):
			_ = json.NewEncoder(w).Encode(sidecar.TxExistsResp{Exists: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	if err = scanner.emitReorgErrata(&reorgBlockMeta{
		Height: 40, BlockHash: "orphan", PreviousHash: "parent", CustomerTransactions: []string{txID},
	}, nil); err != nil {
		t.Fatal(err)
	}
	watches, err := store.ListRelocationWatches(0)
	if err != nil || len(watches) != 1 || !watches[0].InPool {
		t.Fatalf("pool orphan was not durably watched: %#v err=%v", watches, err)
	}

	// Recreate both wrappers over the same DB to model a Bifrost restart. Pool
	// presence alone must retain the watch and must not emit permanent errata.
	restartedStore := NewReorgStore(store.db)
	restarted := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	restarted.SetReorgStore(restartedStore)
	if err = restarted.reconcileRelocationWatches(); err != nil {
		t.Fatal(err)
	}
	watches, err = restartedStore.ListRelocationWatches(0)
	if err != nil || len(watches) != 1 || !watches[0].InPool {
		t.Fatalf("restart lost pool watch: %#v err=%v", watches, err)
	}

	if err = restartedStore.Set(&reorgBlockMeta{
		Height: 60, BlockHash: blockHash, PreviousHash: previousHash,
	}); err != nil {
		t.Fatal(err)
	}
	stateMu.Lock()
	mined = true
	stateMu.Unlock()
	if err = restarted.reconcileRelocationWatches(); err != nil {
		t.Fatal(err)
	}
	watches, err = restartedStore.ListRelocationWatches(0)
	if err != nil || len(watches) != 0 {
		t.Fatalf("processed remine did not clear pool watch: %#v err=%v", watches, err)
	}
	meta, err := restartedStore.Get(60)
	if err != nil || meta == nil || !reflect.DeepEqual(meta.CustomerTransactions, []string{txID}) {
		t.Fatalf("processed remine was not journal-bound: %#v err=%v", meta, err)
	}
}

func TestRevertedTransactionQuarantineSurvivesRestartAndBlocksReplayAliases(t *testing.T) {
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := NewReorgStore(db)
	pending := newAuthenticatedInternalInboundReplayStore(db)
	record := standaloneReplayRecord(t, "reverted-replay", 70)
	if err = pending.Enqueue(record); err != nil {
		t.Fatal(err)
	}

	// Model the permanent phase reached only after Thornode explicitly confirms
	// ObservedTxVoter.Reverted. Pending errata delivery must not write this key.
	if err = store.QuarantineRevertedTxs(70, []stypes.ErrataTx{{
		TxID: common.TxID(record.TxID), Chain: common.XMRChain,
	}}); err != nil {
		t.Fatal(err)
	}

	// Restart cleanup drops a replay which predates the errata, and new aliases
	// for the same TxID cannot be enrolled afterwards.
	restartedStore := NewReorgStore(db)
	restartedPending := newAuthenticatedInternalInboundReplayStore(db)
	if err = restartedPending.ResetClaims(); err != nil {
		t.Fatal(err)
	}
	if count, countErr := restartedPending.Count(); countErr != nil || count != 0 {
		t.Fatalf("reverted replay survived restart: count=%d err=%v", count, countErr)
	}
	if err = restartedPending.Enqueue(record); err == nil {
		t.Fatal("reverted TxID was re-enrolled through an authenticated replay alias")
	}

	restarted := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	restarted.SetReorgStore(restartedStore)
	item := scannerReceiptItem("unused", 71)
	item.Tx = record.TxID
	meta := &reorgBlockMeta{Height: 71, BlockHash: "h71", PreviousHash: "h70"}
	initial := scannerCatchupMarker{
		StartHeight: 71, TargetHeight: 71, AnchorHeight: 70, AnchorHash: "h70",
	}
	if err = restartedStore.BeginCatchup(initial); err != nil {
		t.Fatal(err)
	}
	completed := scannerCatchupMarker{
		StartHeight: 72, TargetHeight: 71, AnchorHeight: 71, AnchorHash: "h71",
	}
	if err = restartedStore.CheckpointCatchup(completed, meta, []*stypes.TxInItem{item}); err != nil {
		t.Fatal(err)
	}
	// Model an explicit higher-height reorg replay, not only ordinary catch-up.
	if err = restartedStore.PutObservationReceipt(meta, []*stypes.TxInItem{item}); err != nil {
		t.Fatal(err)
	}
	claims, err := restarted.canonicalObservationReceipts()
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := restarted.prepareCanonicalObservationHandoff(
		claims[0].entry.Items, claims, &completed,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("higher-height remine of Reverted TxID reached TxIn: %#v", filtered)
	}
	if remaining, listErr := restartedStore.ListObservationReceipts(); listErr != nil || len(remaining) != 0 {
		t.Fatalf("quarantined receipt did not drain: %#v err=%v", remaining, listErr)
	}
	if marker, markerErr := restartedStore.GetCatchupMarker(); markerErr != nil || marker != nil {
		t.Fatalf("quarantined completed marker did not drain: %#v err=%v", marker, markerErr)
	}
}

func TestRelocatedDedupeNeverSumsAndOrdersHighestFirst(t *testing.T) {
	scanner := NewScanner(log.Logger, nil, nil, common.XMRChain, common.EmptyPubKey, 72)
	older := scannerReceiptItem("same-relocated-tx", 30)
	newer := older.Copy()
	newer.BlockHeight = 32
	newer.Memo = "OUT:BTC:bc1qnew-metadata"
	newer.Gas = common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(2))}
	olderStored, newerStored := 0, 0
	older.OnObservedTxStored = func() { olderStored++ }
	newer.OnObservedTxStored = func() { newerStored++ }

	other := scannerReceiptItem("mixed-height-other", 31)
	items := scanner.dedupeTxInItemsByTxID([]*stypes.TxInItem{older, other, newer})
	orderTxInItemsHighestFirst(items)
	if len(items) != 2 || items[0].BlockHeight != 32 || items[1].BlockHeight != 31 {
		t.Fatalf("mixed-height observations are not highest-first: %#v", items)
	}
	coin := items[0].Coins.GetCoin(common.XMRAsset)
	if coin.Amount.Uint64() != 5 || items[0].Memo != newer.Memo {
		t.Fatalf("relocated observation was summed or retained stale metadata: %+v", items[0])
	}
	items[0].OnObservedTxStored()
	if olderStored != 1 || newerStored != 1 {
		t.Fatalf("relocated dedupe dropped a terminal callback: old=%d new=%d", olderStored, newerStored)
	}
}

func TestAdvanceSidecarCatchupChunksAndRecoversAmbiguousAdvance(t *testing.T) {
	store := newScannerReceiptStore(t)
	var stateMu sync.Mutex
	nextHeight := uint64(10)
	lastHeight := uint64(9)
	resetEpoch := uint64(1)
	lastHash := testBlockHash(9)
	lastParentHash := testBlockHash(8)
	failHeight11Once := true
	markerSeenBeforeAdvance := true
	syncHeights := make([]int64, 0)
	rewindTargets := make([]uint64, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/scanner/state":
			stateMu.Lock()
			resp := sidecar.ScannerState{
				ResetEpoch:       resetEpoch,
				NextHeight:       &nextHeight,
				LastHeight:       &lastHeight,
				LastBlockHash:    lastHash,
				LastParentHash:   lastParentHash,
				StrictAfterReset: true,
			}
			stateMu.Unlock()
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/"):
			height, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/"), 10, 64)
			if err != nil || height < 9 || height > 15 {
				http.Error(w, "unknown block", http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp{
				Height:       height,
				Hash:         testBlockHash(height),
				PreviousHash: testBlockHash(height - 1),
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/rewind":
			var req sidecar.ScannerRewindReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			stateMu.Lock()
			if req.ExpectedNextHeight != nextHeight || req.ExpectedResetEpoch != resetEpoch {
				stateMu.Unlock()
				http.Error(w, "stale rewind", http.StatusConflict)
				return
			}
			rewindTargets = append(rewindTargets, req.TargetNextHeight)
			previous := nextHeight
			nextHeight = req.TargetNextHeight
			lastHeight = req.AnchorHeight
			lastHash = req.AnchorHash
			lastParentHash = testBlockHash(int64(req.AnchorHeight) - 1)
			resetEpoch++
			resp := sidecar.ScannerRewindResp{
				Rewound:            true,
				PreviousNextHeight: previous,
				NextHeight:         nextHeight,
				ResetEpoch:         resetEpoch,
			}
			stateMu.Unlock()
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			var req sidecar.SyncReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			marker, markerErr := store.GetCatchupMarker()
			if markerErr != nil || marker == nil || marker.StartHeight != req.Height {
				markerSeenBeforeAdvance = false
			}
			stateMu.Lock()
			if req.ResetEpoch == nil || *req.ResetEpoch != resetEpoch ||
				req.ExpectedNextHeight == nil || *req.ExpectedNextHeight != uint64(req.Height) ||
				uint64(req.Height) != nextHeight {
				stateMu.Unlock()
				http.Error(w, "scanner cursor mismatch", http.StatusConflict)
				return
			}
			syncHeights = append(syncHeights, req.Height)
			nextHeight = uint64(req.Height + 1)
			lastHeight = uint64(req.Height)
			lastHash = req.BlockHash
			lastParentHash = req.ParentHash
			shouldFail := req.Height == 11 && failHeight11Once
			if shouldFail {
				failHeight11Once = false
			}
			stateMu.Unlock()
			if shouldFail {
				http.Error(w, "lost response after durable sidecar advance", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.Transfers{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client, err := sidecar.NewClientWithEndpoint(config.BifrostChainConfiguration{}, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ScannerState(); err != nil {
		t.Fatal(err)
	}
	scanner := NewScanner(log.Logger, client, &scannerMemoBridge{}, common.XMRChain, common.EmptyPubKey, 2)
	scanner.SetReorgStore(store)

	if handled, completed, catchErr := scanner.advanceSidecarCatchup(15, 15); !handled || completed != nil || catchErr == nil {
		t.Fatalf("expected injected ambiguous advance: handled=%v completed=%#v err=%v", handled, completed, catchErr)
	}
	marker, err := store.GetCatchupMarker()
	if err != nil || marker == nil || marker.StartHeight != 11 {
		t.Fatalf("first durable checkpoint was not retained: marker=%#v err=%v", marker, err)
	}
	if meta, metaErr := store.Get(10); metaErr != nil || meta == nil || meta.BlockHash != testBlockHash(10) {
		t.Fatalf("completed pre-crash height was not journaled: meta=%#v err=%v", meta, metaErr)
	}

	var completed *scannerCatchupMarker
	for attempts := 0; attempts < 4 && completed == nil; attempts++ {
		handled, candidate, catchErr := scanner.advanceSidecarCatchup(15, 15)
		if !handled {
			t.Fatal("durable catch-up unexpectedly returned to ordinary scan")
		}
		if catchErr != nil && !errors.Is(catchErr, btypes.ErrUnavailableBlock) {
			t.Fatalf("catch-up retry: %v", catchErr)
		}
		completed = candidate
	}
	if completed == nil || !completed.completed() {
		t.Fatal("arbitrary scanner gap did not complete across bounded chunks")
	}
	if !markerSeenBeforeAdvance {
		t.Fatal("sidecar advanced before a durable catch-up marker existed")
	}
	if !reflect.DeepEqual(rewindTargets, []uint64{11}) {
		t.Fatalf("ambiguous response was not rewound to its durable marker: %v", rewindTargets)
	}
	if !reflect.DeepEqual(syncHeights, []int64{10, 11, 11, 12, 13, 14, 15}) {
		t.Fatalf("catch-up was not bounded, ascending, and replay-safe: %v", syncHeights)
	}
	for height := int64(10); height <= 15; height++ {
		meta, metaErr := store.Get(height)
		if metaErr != nil || meta == nil || meta.BlockHash != testBlockHash(height) {
			t.Fatalf("height %d missing from accumulated journal: meta=%#v err=%v", height, meta, metaErr)
		}
	}
	if err = store.ClearCompletedCatchupMarker(*completed); err != nil {
		t.Fatal(err)
	}
}
