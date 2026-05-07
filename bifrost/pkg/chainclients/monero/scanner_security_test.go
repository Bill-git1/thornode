package monero

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/storage"
	"go.uber.org/atomic"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"
	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestFetchTxsAggregatesDuplicateIncomingTxIDs(t *testing.T) {
	t.Parallel()

	dupTxID := strings.Repeat("a", 64)
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:    dupTxID,
				Address: vaultAddr.String(),
				Amount:  uint64(2 * moneroAtomicToThorchain),
				Height:  100,
				Note:    "SWAP:XMR.XMR:thor1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0l5m4g",
			},
			{
				TxID:    dupTxID,
				Address: vaultAddr.String(),
				Amount:  uint64(3 * moneroAtomicToThorchain),
				Height:  100,
				Note:    "SWAP:XMR.XMR:thor1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq0l5m4g",
			},
		},
		nil,
		[]string{vaultAddr.String()},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{PubKeyEddsa: vaultPubKey, PubKey: vaultPubKey, PubKeyFrost: vaultPubKey}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 1 {
		t.Fatalf("expected duplicate txid entries to collapse to one observation, got %d", len(txIn.TxArray))
	}
	amount := txIn.TxArray[0].Coins.GetCoin(common.XMRAsset).Amount
	if !amount.Equal(cosmos.NewUint(5)) {
		t.Fatalf("expected merged amount 5, got %s", amount)
	}
}

func TestFetchTxsSkipsIncomingTransfersWithMismatchedExplicitVaultAttribution(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("b", 64)
	currentVaultPubKey, currentVaultAddr := testVaultPubKeyAndAddress(t)
	otherVaultPubKey, _ := testVaultPubKeyAndAddress(t)
	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     currentVaultAddr.String(),
				VaultPubKey: currentVaultPubKey.String(),
				Amount:      uint64(2 * moneroAtomicToThorchain),
				Height:      100,
				Note:        "MIGRATE:100",
			},
			{
				TxID:        txID,
				Address:     currentVaultAddr.String(),
				VaultPubKey: otherVaultPubKey.String(),
				Amount:      uint64(2 * moneroAtomicToThorchain),
				Height:      100,
				Note:        "MIGRATE:100",
			},
		},
		nil,
		[]string{currentVaultAddr.String()},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{PubKeyEddsa: currentVaultPubKey, PubKey: currentVaultPubKey, PubKeyFrost: currentVaultPubKey}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 1 {
		t.Fatalf("expected only the current asgard attribution to survive, got %d items", len(txIn.TxArray))
	}
	amount := txIn.TxArray[0].Coins.GetCoin(common.XMRAsset).Amount
	if !amount.Equal(cosmos.NewUint(2)) {
		t.Fatalf("expected mismatched explicit vault attribution to be skipped, got %s", amount)
	}
	if !txIn.TxArray[0].ObservedVaultPubKey.Equals(currentVaultPubKey) {
		t.Fatalf("expected current asgard vault pubkey %s, got %s", currentVaultPubKey, txIn.TxArray[0].ObservedVaultPubKey)
	}
}

func TestFetchTxsAcceptsCurrentAsgardWithPubKeyFrost(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("d", 64)
	currentVaultPubKey, currentVaultAddr := testVaultPubKeyAndAddress(t)
	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     currentVaultAddr.String(),
				VaultPubKey: currentVaultPubKey.String(),
				Amount:      uint64(2 * moneroAtomicToThorchain),
				Height:      100,
				Note:        "MIGRATE:100",
			},
		},
		nil,
		[]string{currentVaultAddr.String()},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{
				PubKeyEddsa: currentVaultPubKey,
				PubKey:      currentVaultPubKey,
				PubKeyFrost: currentVaultPubKey,
			}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 1 {
		t.Fatalf("expected current asgard transfer to survive with PubKeyFrost, got %d items", len(txIn.TxArray))
	}
	if !txIn.TxArray[0].ObservedVaultPubKey.Equals(currentVaultPubKey) {
		t.Fatalf("expected current asgard vault pubkey %s, got %s", currentVaultPubKey, txIn.TxArray[0].ObservedVaultPubKey)
	}
}

func TestFetchTxsSkipsCurrentAsgardWithoutPubKeyFrost(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("f", 64)
	currentVaultPubKey, currentVaultAddr := testVaultPubKeyAndAddress(t)
	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     currentVaultAddr.String(),
				VaultPubKey: currentVaultPubKey.String(),
				Amount:      uint64(2 * moneroAtomicToThorchain),
				Height:      100,
				Note:        "MIGRATE:100",
			},
		},
		nil,
		[]string{currentVaultAddr.String()},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{
				PubKeyEddsa: currentVaultPubKey,
				PubKey:      currentVaultPubKey,
			}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 0 {
		t.Fatalf("expected current asgard transfer to be skipped without PubKeyFrost, got %d items", len(txIn.TxArray))
	}
}

func TestFetchTxsSkipsIncomingChangeForKnownOutgoingSameVault(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("c", 64)
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	vaultAddress := vaultAddr.String()
	destinationAddress := common.XMRChecksummedDummyAddress('8')

	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:    txID,
				Address: vaultAddress,
				Amount:  uint64(4 * moneroAtomicToThorchain),
				Height:  100,
			},
		},
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     destinationAddress,
				FromAddress: vaultAddress,
				VaultPubKey: vaultPubKey.String(),
				Amount:      uint64(1 * moneroAtomicToThorchain),
				Fee:         uint64(1 * moneroAtomicToThorchain),
				Height:      100,
			},
		},
		[]string{vaultAddress},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{PubKeyEddsa: vaultPubKey, PubKey: vaultPubKey, PubKeyFrost: vaultPubKey}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 1 {
		t.Fatalf("expected only the outbound observation, got %d items", len(txIn.TxArray))
	}
	if txIn.TxArray[0].Sender != vaultAddress {
		t.Fatalf("expected outbound sender %s, got %s", vaultAddress, txIn.TxArray[0].Sender)
	}
	if txIn.TxArray[0].To != destinationAddress {
		t.Fatalf("expected outbound destination %s, got %s", destinationAddress, txIn.TxArray[0].To)
	}
	amount := txIn.TxArray[0].Coins.GetCoin(common.XMRAsset).Amount
	if !amount.Equal(cosmos.NewUint(1)) {
		t.Fatalf("expected outbound amount 1, got %s", amount)
	}
}

func TestFetchTxsPreservesInboundAndOutboundViewsForSameTxIDAcrossVaults(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("d", 64)
	sourceVaultPubKey, sourceVaultAddr := testVaultPubKeyAndAddress(t)
	destVaultPubKey, destVaultAddr := testVaultPubKeyAndAddress(t)

	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:    txID,
				Address: destVaultAddr.String(),
				Amount:  uint64(2 * moneroAtomicToThorchain),
				Height:  100,
				Note:    "MIGRATE:100",
			},
		},
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     destVaultAddr.String(),
				FromAddress: sourceVaultAddr.String(),
				VaultPubKey: sourceVaultPubKey.String(),
				Amount:      uint64(2 * moneroAtomicToThorchain),
				Fee:         uint64(1 * moneroAtomicToThorchain),
				Height:      100,
				Note:        "MIGRATE:100",
			},
		},
		[]string{sourceVaultAddr.String(), destVaultAddr.String()},
	)
	defer frostServer.Close()

	frostSigner, err := sidecar.NewClientWithEndpoint(
		config.BifrostChainConfiguration{},
		frostServer.URL,
	)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{
				{PubKeyEddsa: sourceVaultPubKey, PubKey: sourceVaultPubKey, PubKeyFrost: sourceVaultPubKey},
				{PubKeyEddsa: destVaultPubKey, PubKey: destVaultPubKey, PubKeyFrost: destVaultPubKey},
			},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	txIn, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("fetch txs: %v", err)
	}
	if len(txIn.TxArray) != 2 {
		t.Fatalf("expected both inbound and outbound observations, got %d items", len(txIn.TxArray))
	}

	var inbound, outbound *stypes.TxInItem
	for _, item := range txIn.TxArray {
		switch {
		case item.Sender == sourceVaultAddr.String():
			outbound = item
		case item.To == destVaultAddr.String():
			inbound = item
		}
	}
	if outbound == nil {
		t.Fatal("expected outbound observation to be preserved")
	}
	if inbound == nil {
		t.Fatal("expected inbound observation to be preserved")
	}
	if !outbound.ObservedVaultPubKey.Equals(sourceVaultPubKey) {
		t.Fatalf("expected outbound vault pubkey %s, got %s", sourceVaultPubKey, outbound.ObservedVaultPubKey)
	}
	if !inbound.ObservedVaultPubKey.Equals(destVaultPubKey) {
		t.Fatalf("expected inbound vault pubkey %s, got %s", destVaultPubKey, inbound.ObservedVaultPubKey)
	}
}

func TestFetchTxsSkipsDelayedIncomingChangeForPreviouslyObservedOutgoingSameVault(t *testing.T) {
	t.Parallel()

	txID := strings.Repeat("e", 64)
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	vaultAddress := vaultAddr.String()
	destinationAddress := common.XMRChecksummedDummyAddress('9')

	frostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/chain/block/"):
			rawHeight := strings.TrimPrefix(r.URL.Path, "/v1/monero/chain/block/")
			height, err := strconv.ParseInt(rawHeight, 10, 64)
			if err != nil {
				http.Error(w, "invalid block height", http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp{
				Height:       height,
				Reward:       defaultCoinbaseValue + 200000,
				Hash:         "hash-" + rawHeight,
				PreviousHash: "hash-" + strconv.FormatInt(height-1, 10),
				TxHashes:     []string{txID},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			var req sidecar.SyncReq
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}

			resp := sidecar.Transfers{}
			switch req.Height {
			case 100:
				resp.Outgoing = []sidecar.Transfer{
					{
						TxID:        txID,
						Address:     destinationAddress,
						FromAddress: vaultAddress,
						VaultPubKey: vaultPubKey.String(),
						Amount:      uint64(1 * moneroAtomicToThorchain),
						Fee:         uint64(1 * moneroAtomicToThorchain),
						Height:      100,
					},
				}
			case 101:
				resp.Incoming = []sidecar.Transfer{
					{
						TxID:        txID,
						Address:     vaultAddress,
						VaultPubKey: vaultPubKey.String(),
						Amount:      uint64(4 * moneroAtomicToThorchain),
						Height:      101,
					},
				}
			}
			_ = json.NewEncoder(w).Encode(resp)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/monero/addresses/"):
			_ = json.NewEncoder(w).Encode(sidecar.Addresses{Addresses: []string{vaultAddress}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/vaults/register":
			_ = json.NewEncoder(w).Encode(sidecar.RegisterVaultResp{Registered: true})
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
	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{
			asgards: ttypes.Vaults{{PubKeyEddsa: vaultPubKey, PubKey: vaultPubKey, PubKeyFrost: vaultPubKey}},
		},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)

	first, err := scanner.FetchTxs(100, 101)
	if err != nil {
		t.Fatalf("fetch txs at height 100: %v", err)
	}
	if len(first.TxArray) != 1 {
		t.Fatalf("expected outbound observation at height 100, got %d items", len(first.TxArray))
	}
	if first.TxArray[0].Sender != vaultAddress {
		t.Fatalf("expected outbound sender %s, got %s", vaultAddress, first.TxArray[0].Sender)
	}

	second, err := scanner.FetchTxs(101, 101)
	if err != nil {
		t.Fatalf("fetch txs at height 101: %v", err)
	}
	if len(second.TxArray) != 0 {
		buf := &bytes.Buffer{}
		_ = json.NewEncoder(buf).Encode(second.TxArray)
		t.Fatalf("expected delayed same-vault incoming change to be skipped, got %d items: %s", len(second.TxArray), buf.String())
	}
}

func TestFetchTxsFailsClosedOnReorgProcessingError(t *testing.T) {
	t.Parallel()

	syncCalls := atomic.NewInt32(0)
	frostServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/chain/block/101":
			_ = json.NewEncoder(w).Encode(sidecar.ChainBlockResp{
				Height:       101,
				Reward:       defaultCoinbaseValue,
				Hash:         "hash-101",
				PreviousHash: "canonical-100",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/monero/chain/block/100":
			http.Error(w, "reorg confirmation failed", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/monero/scanner/sync":
			syncCalls.Inc()
			_ = json.NewEncoder(w).Encode(sidecar.Transfers{})
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
	if setErr := reorgStore.Set(&reorgBlockMeta{
		Height:               100,
		BlockHash:            "stale-100",
		PreviousHash:         "hash-99",
		CustomerTransactions: []string{"tx-drop"},
	}); setErr != nil {
		t.Fatalf("set reorg meta: %v", setErr)
	}

	scanner := NewScanner(
		log.Logger,
		frostSigner,
		&scannerMemoBridge{asgards: ttypes.Vaults{}},
		common.XMRChain,
		common.EmptyPubKey,
		72,
	)
	scanner.SetReorgStore(reorgStore)

	_, err = scanner.FetchTxs(101, 101)
	if err == nil {
		t.Fatal("expected reorg processing error to abort block processing")
	}
	if syncCalls.Load() != 0 {
		t.Fatalf("expected scanner sync to be skipped after reorg failure, got %d calls", syncCalls.Load())
	}
}
