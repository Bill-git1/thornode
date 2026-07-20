package monero

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
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

// TestProcessReorgRetainsIntermediateForkOnSecondReorg covers A -> B -> C
// before B's delivery checkpoint commits. Observer may already have persisted
// B transactions, while their only recoverable description is the pending
// journal. Superseding that journal from A's published metadata would omit B
// errata. The safe near-launch behavior is therefore a terminal, inspectable
// stop with every A/B artifact left untouched.
func TestProcessReorgRetainsIntermediateForkOnSecondReorg(t *testing.T) {
	t.Parallel()

	anchorHash := testXMRHash("sup-anchor-98")
	a99 := testXMRHash("sup-a-99")
	a100 := testXMRHash("sup-a-100")
	b99 := testXMRHash("sup-b-99")
	b100 := testXMRHash("sup-b-100")
	c99 := testXMRHash("sup-c-99")
	c100 := testXMRHash("sup-c-100")
	c101 := testXMRHash("sup-c-101")
	aTx := testXMRHash("sup-a-tx")
	bTx := testXMRHash("sup-b-tx")

	canonical := map[int64]sidecar.ChainBlock{
		99:  withBlockHeader(99, defaultCoinbaseValue, c99, anchorHash),
		100: withBlockHeader(100, defaultCoinbaseValue, c100, c99),
		101: withBlockHeader(101, defaultCoinbaseValue, c101, c100),
	}
	continuity := newTestScannerContinuity(101)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if continuity.handle(t, w, r) {
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/") {
			http.NotFound(w, r)
			return
		}
		height, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/"), 10, 64)
		if err != nil {
			http.Error(w, "bad height", http.StatusBadRequest)
			return
		}
		block, ok := canonical[height]
		if !ok {
			http.Error(w, "unknown height", http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp(block))
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
	publishedA := []*reorgBlockMeta{
		{Height: 98, BlockHash: anchorHash, PreviousHash: testXMRHash("sup-a-97")},
		{Height: 99, BlockHash: a99, PreviousHash: anchorHash},
		{Height: 100, BlockHash: a100, PreviousHash: a99, CustomerTransactions: []string{aTx}},
	}
	for _, meta := range publishedA {
		if err = store.Set(meta); err != nil {
			t.Fatalf("seed A metadata at %d: %v", meta.Height, err)
		}
	}

	intermediateB := &reorgJournal{
		Version:      2,
		Phase:        reorgPhaseDelivered,
		AnchorHeight: 98,
		AnchorHash:   anchorHash,
		Heights:      []int64{99, 100},
		Errata: []reorgErrataRecord{
			{Height: 100, TxIDs: []string{aTx}},
		},
		ReplayedMetas: []*reorgBlockMeta{
			{Height: 99, BlockHash: b99, PreviousHash: anchorHash},
			{Height: 100, BlockHash: b100, PreviousHash: b99, CustomerTransactions: []string{bTx}},
		},
		ReplayedItems: []*stypes.TxInItem{
			{BlockHeight: 100, Tx: bTx, Sender: "intermediate-b"},
		},
	}
	if err = store.SetJournal(intermediateB); err != nil {
		t.Fatalf("seed B journal: %v", err)
	}
	wantJournal, err := store.GetJournal()
	if err != nil {
		t.Fatalf("reload seeded B journal: %v", err)
	}

	scanner := NewScanner(log.Logger, client, nil, common.XMRChain, common.EmptyPubKey, 72)
	scanner.SetReorgStore(store)
	errataQueue := make(chan stypes.ErrataBlock, 1)
	scanner.SetErrataQueue(errataQueue)

	for attempt := 1; attempt <= 2; attempt++ {
		_, processErr := scanner.processReorg(canonical[101], 101)
		if !errors.Is(processErr, ErrReorgSupersessionUnsafe) {
			t.Fatalf("attempt %d: expected unsafe-supersession stop, got %v", attempt, processErr)
		}

		gotJournal, loadErr := store.GetJournal()
		if loadErr != nil {
			t.Fatalf("attempt %d: reload B journal: %v", attempt, loadErr)
		}
		if !reflect.DeepEqual(gotJournal, wantJournal) {
			t.Fatalf("attempt %d mutated the only durable B history:\n got: %#v\nwant: %#v", attempt, gotJournal, wantJournal)
		}
		if len(gotJournal.ReplayedItems) != 1 || gotJournal.ReplayedItems[0].Tx != bTx {
			t.Fatalf("attempt %d lost intermediate B transaction %s: %#v", attempt, bTx, gotJournal.ReplayedItems)
		}
		for _, wantMeta := range publishedA {
			gotMeta, metaErr := store.Get(wantMeta.Height)
			if metaErr != nil {
				t.Fatalf("attempt %d: reload A metadata at %d: %v", attempt, wantMeta.Height, metaErr)
			}
			if !reflect.DeepEqual(gotMeta, wantMeta) {
				t.Fatalf("attempt %d replaced published A metadata at %d: got %#v want %#v", attempt, wantMeta.Height, gotMeta, wantMeta)
			}
		}
		select {
		case errata := <-errataQueue:
			t.Fatalf("attempt %d emitted errata while supersession was unsafe: %#v", attempt, errata)
		default:
		}
	}

	// The public FetchTxs path must convert the preservation sentinel into the
	// terminal control-flow error that stops BlockScanner and pages an operator,
	// rather than hammering the same unsafe supersession forever.
	_, fetchErr := scanner.FetchTxs(101, 101)
	var recoveryRequired *btypes.ScannerRecoveryRequiredError
	if !errors.As(fetchErr, &recoveryRequired) {
		t.Fatalf("expected terminal scanner recovery error, got %v", fetchErr)
	}
	if recoveryRequired.CurrentHeight != 101 || recoveryRequired.NextHeight != 101 {
		t.Fatalf("unexpected recovery heights: %#v", recoveryRequired)
	}
	if !strings.Contains(recoveryRequired.Reason, ErrReorgSupersessionUnsafe.Error()) ||
		!strings.Contains(recoveryRequired.Reason, "journal retained") {
		t.Fatalf("recovery reason does not explain the retained journal: %q", recoveryRequired.Reason)
	}
	gotJournal, err := store.GetJournal()
	if err != nil {
		t.Fatalf("reload B journal after public-path stop: %v", err)
	}
	if !reflect.DeepEqual(gotJournal, wantJournal) {
		t.Fatalf("public-path stop mutated the B journal: got %#v want %#v", gotJournal, wantJournal)
	}
}
