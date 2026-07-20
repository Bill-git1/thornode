package monero

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"

	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// newXMRReorgJournalTestDB opens a throwaway in-memory leveldb, mirroring how
// the scanner reorg tests build a ReorgStore.
func newXMRReorgJournalTestDB(t *testing.T) *leveldb.DB {
	t.Helper()
	db, err := leveldb.Open(storage.NewMemStorage(), nil)
	if err != nil {
		t.Fatalf("open in-memory leveldb: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Errorf("close in-memory leveldb: %v", closeErr)
		}
	})
	return db
}

func newXMRReorgJournalTestItem(t *testing.T, height int64, label string) *stypes.TxInItem {
	t.Helper()
	pubKey := testVaultPubKey(t, "reorg-journal-vault")
	address, err := pubKey.GetAddress(common.XMRChain)
	if err != nil {
		t.Fatalf("derive monero address: %v", err)
	}
	item := stypes.NewTxInItem(
		height,
		testXMRHash(label),
		"=:THOR.RUNE:tthor1xmrjournal",
		testOutboundXMRDestination().String(),
		address.String(),
		common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(1_500_000))),
		common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1_000))},
		pubKey,
		"", "", nil,
	)
	item.SpentOutputRefs = []*common.OutputRef{testXMROutputRef(label, 3, testXMRHash(label+":spend"))}
	return item
}

// TestXMRReorgStoreJournalRoundTripPreservesReplayedItems pins that the replay
// payload the scanner must hand to Observer after a restart survives a
// SetJournal/GetJournal round trip through leveldb.
func TestXMRReorgStoreJournalRoundTripPreservesReplayedItems(t *testing.T) {
	t.Parallel()

	db := newXMRReorgJournalTestDB(t)
	store := NewReorgStore(db)

	if journal, err := store.GetJournal(); err != nil || journal != nil {
		t.Fatalf("expected no journal on a fresh store, got journal=%#v err=%v", journal, err)
	}

	original := &reorgJournal{
		Version:      2,
		Phase:        reorgPhaseReplayed,
		AnchorHeight: 98,
		AnchorHash:   testXMRHash("journal-anchor-98"),
		Heights:      []int64{99, 100},
		Errata: []reorgErrataRecord{
			{Height: 100, TxIDs: []string{testXMRHash("journal-dropped-tx")}},
		},
		ReplayedMetas: []*reorgBlockMeta{
			{
				Height:       99,
				BlockHash:    testXMRHash("journal-new99"),
				PreviousHash: testXMRHash("journal-anchor-98"),
			},
			{
				Height:               100,
				BlockHash:            testXMRHash("journal-new100"),
				PreviousHash:         testXMRHash("journal-new99"),
				CustomerTransactions: []string{testXMRHash("journal-replayed-tx")},
			},
		},
		ReplayedItems: []*stypes.TxInItem{
			newXMRReorgJournalTestItem(t, 100, "journal-replayed-tx"),
		},
	}
	if err := store.SetJournal(original); err != nil {
		t.Fatalf("set journal: %v", err)
	}

	loaded, err := store.GetJournal()
	if err != nil {
		t.Fatalf("get journal: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected journal after SetJournal")
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Fatalf("journal round trip lost data:\n got: %#v\nwant: %#v", loaded, original)
	}
	if len(loaded.ReplayedItems) != 1 {
		t.Fatalf("expected 1 replayed item, got %d", len(loaded.ReplayedItems))
	}
	item := loaded.ReplayedItems[0]
	if item.Tx != original.ReplayedItems[0].Tx || item.BlockHeight != 100 {
		t.Fatalf("replayed item corrupted by round trip: %#v", item)
	}
	if len(item.Coins) != 1 || !item.Coins[0].Amount.Equal(cosmos.NewUint(1_500_000)) {
		t.Fatalf("replayed item coins corrupted by round trip: %#v", item.Coins)
	}
	if len(item.SpentOutputRefs) != 1 ||
		item.SpentOutputRefs[0].KeyImage != original.ReplayedItems[0].SpentOutputRefs[0].KeyImage {
		t.Fatalf("replayed item spent output refs corrupted by round trip: %#v", item.SpentOutputRefs)
	}

	// SetJournal must refuse anything that cannot be replayed.
	for name, invalid := range map[string]*reorgJournal{
		"nil":          nil,
		"zero version": {Version: 0, Phase: reorgPhaseDiscovered, Heights: []int64{99}},
		"future version": {
			Version: 3, Phase: reorgPhaseDiscovered, Heights: []int64{99},
		},
		"no heights": {Version: 2, Phase: reorgPhaseDiscovered},
	} {
		if err = store.SetJournal(invalid); err == nil {
			t.Fatalf("SetJournal accepted invalid journal (%s)", name)
		}
	}

	// The rejected writes must not have clobbered the durable journal.
	reloaded, err := store.GetJournal()
	if err != nil || reloaded == nil || !reflect.DeepEqual(reloaded, original) {
		t.Fatalf("rejected SetJournal calls mutated the durable journal: %#v err=%v", reloaded, err)
	}

	if err = store.DeleteJournal(); err != nil {
		t.Fatalf("delete journal: %v", err)
	}
	if journal, jErr := store.GetJournal(); jErr != nil || journal != nil {
		t.Fatalf("expected journal removed, got journal=%#v err=%v", journal, jErr)
	}
	// DeleteJournal is idempotent: a second delete must not error.
	if err = store.DeleteJournal(); err != nil {
		t.Fatalf("second delete journal: %v", err)
	}
}

// TestXMRReorgStoreGetJournalRejectsUnknownVersion pins the current fail-closed
// behaviour: an on-disk journal whose Version is not 2 hard-errors instead of
// being ignored. FetchTxs propagates this error on every block, so a
// corrupt or future-version journal wedges the XMR scanner until an operator
// intervenes. That is deliberate (silently dropping a journal could lose errata
// or double-observe), but it means a downgrade after a journal-format bump is a
// permanent stall, not a degraded scan.
func TestXMRReorgStoreGetJournalRejectsUnknownVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		version int
	}{
		{name: "future version", version: 3},
		{name: "legacy version", version: 1},
		{name: "missing version", version: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := newXMRReorgJournalTestDB(t)
			store := NewReorgStore(db)

			raw, err := json.Marshal(&reorgJournal{
				Version:      tc.version,
				Phase:        reorgPhaseDelivered,
				AnchorHeight: 98,
				AnchorHash:   testXMRHash("unsupported-anchor"),
				Heights:      []int64{99},
				ReplayedMetas: []*reorgBlockMeta{
					{Height: 99, BlockHash: testXMRHash("unsupported-99"), PreviousHash: testXMRHash("unsupported-anchor")},
				},
			})
			if err != nil {
				t.Fatalf("encode journal: %v", err)
			}
			if err = db.Put([]byte(reorgJournalKey), raw, nil); err != nil {
				t.Fatalf("write raw journal: %v", err)
			}

			journal, err := store.GetJournal()
			if err == nil {
				t.Fatalf("expected unsupported version error, got journal=%#v", journal)
			}
			if journal != nil {
				t.Fatalf("unsupported journal must not be returned: %#v", journal)
			}
			if !strings.Contains(err.Error(), "unsupported xmr reorg journal version") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}

	// Non-JSON garbage must also fail closed rather than be treated as absent.
	db := newXMRReorgJournalTestDB(t)
	store := NewReorgStore(db)
	if err := db.Put([]byte(reorgJournalKey), []byte("{not-json"), nil); err != nil {
		t.Fatalf("write corrupt journal: %v", err)
	}
	if journal, err := store.GetJournal(); err == nil || journal != nil {
		t.Fatalf("expected decode failure for corrupt journal, got journal=%#v err=%v", journal, err)
	}
}

// TestXMRReorgStoreCommitDeliveryRejectsInvalidInput ensures the final,
// destructive step of the reorg pipeline (replace canonical metadata + drop the
// journal) refuses to run on anything that is not a complete delivered journal.
func TestXMRReorgStoreCommitDeliveryRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	staleHash := testStaleBlockHash(99)
	canonicalHash := testBlockHash(99)
	anchorHash := testBlockHash(98)

	delivered := func() *reorgJournal {
		return &reorgJournal{
			Version:      2,
			Phase:        reorgPhaseDelivered,
			AnchorHeight: 98,
			AnchorHash:   anchorHash,
			Heights:      []int64{99},
			ReplayedMetas: []*reorgBlockMeta{
				{Height: 99, BlockHash: canonicalHash, PreviousHash: anchorHash},
			},
		}
	}

	cases := map[string]*reorgJournal{
		"nil journal": nil,
		"discovered phase": func() *reorgJournal {
			j := delivered()
			j.Phase = reorgPhaseDiscovered
			return j
		}(),
		"replayed phase": func() *reorgJournal {
			j := delivered()
			j.Phase = reorgPhaseReplayed
			return j
		}(),
		"empty phase": func() *reorgJournal {
			j := delivered()
			j.Phase = ""
			return j
		}(),
		"no replayed metas": func() *reorgJournal {
			j := delivered()
			j.ReplayedMetas = nil
			return j
		}(),
		"nil meta entry": func() *reorgJournal {
			j := delivered()
			j.ReplayedMetas = []*reorgBlockMeta{nil}
			return j
		}(),
		"negative meta height": func() *reorgJournal {
			j := delivered()
			j.ReplayedMetas[0].Height = -1
			return j
		}(),
		"empty meta hash": func() *reorgJournal {
			j := delivered()
			j.ReplayedMetas[0].BlockHash = ""
			return j
		}(),
	}

	for name, journal := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := newXMRReorgJournalTestDB(t)
			store := NewReorgStore(db)

			// Seed the pre-reorg canonical metadata and a durable journal so a
			// buggy CommitDelivery would be caught mutating either.
			if err := store.Set(&reorgBlockMeta{Height: 99, BlockHash: staleHash, PreviousHash: anchorHash}); err != nil {
				t.Fatalf("seed stale metadata: %v", err)
			}
			seeded := delivered()
			if err := store.SetJournal(seeded); err != nil {
				t.Fatalf("seed journal: %v", err)
			}

			if err := store.CommitDelivery(journal); err == nil {
				t.Fatal("expected CommitDelivery to reject invalid input")
			}
			meta, err := store.Get(99)
			if err != nil {
				t.Fatalf("get metadata: %v", err)
			}
			if meta == nil || meta.BlockHash != staleHash {
				t.Fatalf("rejected CommitDelivery must not publish metadata, got %#v", meta)
			}
			stillThere, err := store.GetJournal()
			if err != nil {
				t.Fatalf("get journal: %v", err)
			}
			if stillThere == nil || stillThere.Phase != reorgPhaseDelivered {
				t.Fatalf("rejected CommitDelivery must not drop the journal, got %#v", stillThere)
			}
		})
	}
}

// TestXMRReorgStoreDeliveredJournalSurvivesCrashBeforeCommitDelivery covers the
// crash window that CommitDelivery exists to close: the journal is durably in
// the delivered phase, but the process dies before CommitDelivery runs. On
// resume the errata and the replayed observations must still be recoverable,
// and the pre-reorg (stale) canonical metadata must still be in place so the
// replay is repeatable rather than silently skipped.
func TestXMRReorgStoreDeliveredJournalSurvivesCrashBeforeCommitDelivery(t *testing.T) {
	t.Parallel()

	db := newXMRReorgJournalTestDB(t)
	store := NewReorgStore(db)

	anchorHash := testBlockHash(98)
	stale99 := testStaleBlockHash(99)
	stale100 := testStaleBlockHash(100)
	new99 := testBlockHash(99)
	new100 := testBlockHash(100)
	droppedTx := testXMRHash("crash-dropped-tx")
	replayedTx := testXMRHash("crash-replayed-tx")

	for _, meta := range []*reorgBlockMeta{
		{Height: 98, BlockHash: anchorHash, PreviousHash: testBlockHash(97)},
		{Height: 99, BlockHash: stale99, PreviousHash: anchorHash},
		{Height: 100, BlockHash: stale100, PreviousHash: stale99, CustomerTransactions: []string{droppedTx}},
	} {
		if err := store.Set(meta); err != nil {
			t.Fatalf("seed pre-reorg metadata at %d: %v", meta.Height, err)
		}
	}

	discovered := &reorgJournal{
		Version:      2,
		Phase:        reorgPhaseDiscovered,
		AnchorHeight: 98,
		AnchorHash:   anchorHash,
		Heights:      []int64{99, 100},
		Errata:       []reorgErrataRecord{{Height: 100, TxIDs: []string{droppedTx}}},
	}
	if err := store.SetJournal(discovered); err != nil {
		t.Fatalf("set discovered journal: %v", err)
	}

	replayedMetas := []*reorgBlockMeta{
		{Height: 99, BlockHash: new99, PreviousHash: anchorHash},
		{Height: 100, BlockHash: new100, PreviousHash: new99, CustomerTransactions: []string{replayedTx}},
	}
	replayedItems := []*stypes.TxInItem{newXMRReorgJournalTestItem(t, 100, "crash-replayed-tx")}
	if err := store.CommitReplay(discovered, replayedMetas, replayedItems); err != nil {
		t.Fatalf("commit replay: %v", err)
	}

	// CommitReplay must not publish canonical metadata yet: the replay is not
	// durable in Observer until CommitDelivery.
	if meta, err := store.Get(100); err != nil || meta == nil || meta.BlockHash != stale100 {
		t.Fatalf("CommitReplay must not publish canonical metadata, got meta=%#v err=%v", meta, err)
	}

	// Scanner moves the journal to delivered once errata have been queued...
	replayed, err := store.GetJournal()
	if err != nil {
		t.Fatalf("get replayed journal: %v", err)
	}
	if replayed.Phase != reorgPhaseReplayed {
		t.Fatalf("expected replayed phase, got %q", replayed.Phase)
	}
	replayed.Phase = reorgPhaseDelivered
	if err = store.SetJournal(replayed); err != nil {
		t.Fatalf("set delivered journal: %v", err)
	}

	// ...and then the process crashes before CommitDelivery. Re-open the store
	// over the same durable db to model the restart.
	resumed := NewReorgStore(db)
	journal, err := resumed.GetJournal()
	if err != nil {
		t.Fatalf("get journal after crash: %v", err)
	}
	if journal == nil {
		t.Fatal("delivered journal lost across restart")
	}
	if journal.Phase != reorgPhaseDelivered {
		t.Fatalf("expected delivered phase after restart, got %q", journal.Phase)
	}
	expectedErrata := []reorgErrataRecord{{Height: 100, TxIDs: []string{droppedTx}}}
	if !reflect.DeepEqual(journal.Errata, expectedErrata) {
		t.Fatalf("errata silently lost across restart: %#v", journal.Errata)
	}
	if len(journal.ReplayedItems) != 1 || journal.ReplayedItems[0].Tx != replayedItems[0].Tx {
		t.Fatalf("replayed observations lost across restart: %#v", journal.ReplayedItems)
	}
	if !reflect.DeepEqual(journal.ReplayedMetas, replayedMetas) {
		t.Fatalf("replayed metadata lost across restart: %#v", journal.ReplayedMetas)
	}
	// The stale metadata still stands, so a resumed scan can re-derive the same
	// errata if delivery has to be repeated.
	for height, want := range map[int64]string{99: stale99, 100: stale100} {
		meta, metaErr := resumed.Get(height)
		if metaErr != nil || meta == nil || meta.BlockHash != want {
			t.Fatalf("pre-reorg metadata at %d must survive the crash, got meta=%#v err=%v", height, meta, metaErr)
		}
	}

	// Completing delivery after the restart publishes canonical metadata and
	// clears the journal in one synced batch.
	if err = resumed.CommitDelivery(journal); err != nil {
		t.Fatalf("commit delivery after restart: %v", err)
	}
	for height, want := range map[int64]string{99: new99, 100: new100} {
		meta, metaErr := resumed.Get(height)
		if metaErr != nil || meta == nil || meta.BlockHash != want {
			t.Fatalf("canonical metadata at %d not published, got meta=%#v err=%v", height, meta, metaErr)
		}
	}
	meta100, err := resumed.Get(100)
	if err != nil {
		t.Fatalf("get meta 100: %v", err)
	}
	if !reflect.DeepEqual(meta100.CustomerTransactions, []string{replayedTx}) {
		t.Fatalf("canonical customer transactions not published: %#v", meta100.CustomerTransactions)
	}
	if journal, err = resumed.GetJournal(); err != nil || journal != nil {
		t.Fatalf("CommitDelivery must drop the journal, got journal=%#v err=%v", journal, err)
	}
}
