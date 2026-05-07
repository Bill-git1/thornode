package monero

import (
	"testing"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"

	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func TestFetchMemPoolEmitsOutboundAndDedupesUntilConfirmed(t *testing.T) {
	t.Parallel()

	txID := "1111111111111111111111111111111111111111111111111111111111111111"
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	vaultAddress := vaultAddr.String()
	destinationAddress := common.XMRChecksummedDummyAddress('8')

	frostServer := newFrostTransfersServer(
		t,
		nil,
		[]sidecar.Transfer{
			{
				TxID:        txID,
				Address:     destinationAddress,
				FromAddress: vaultAddress,
				VaultPubKey: vaultPubKey.String(),
				Amount:      200000,
				Fee:         10000,
				Height:      100,
			},
		},
		[]string{vaultAddress},
	)
	defer frostServer.Close()

	cfg := config.BifrostChainConfiguration{}
	frostSigner, err := sidecar.NewClientWithEndpoint(cfg, frostServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}
	bridge := &scannerMemoBridge{
		asgards: ttypes.Vaults{{PubKeyEddsa: vaultPubKey, PubKey: vaultPubKey, PubKeyFrost: vaultPubKey}},
	}

	scanner := NewScanner(log.Logger, frostSigner, bridge, common.XMRChain, common.EmptyPubKey, 72)

	first, err := scanner.FetchMemPool(100)
	if err != nil {
		t.Fatalf("first mempool fetch: %v", err)
	}
	if !first.MemPool {
		t.Fatal("expected mempool flag to be true")
	}
	if len(first.TxArray) != 1 {
		t.Fatalf("expected 1 mempool tx item, got %d", len(first.TxArray))
	}

	second, err := scanner.FetchMemPool(101)
	if err != nil {
		t.Fatalf("second mempool fetch: %v", err)
	}
	if len(second.TxArray) != 0 {
		t.Fatalf("expected duplicate mempool tx to be suppressed, got %d items", len(second.TxArray))
	}

	confirmed, err := scanner.FetchTxs(100, 100)
	if err != nil {
		t.Fatalf("confirmed fetch: %v", err)
	}
	if len(confirmed.TxArray) != 1 {
		t.Fatalf("expected 1 confirmed tx item, got %d", len(confirmed.TxArray))
	}

	third, err := scanner.FetchMemPool(102)
	if err != nil {
		t.Fatalf("third mempool fetch: %v", err)
	}
	if len(third.TxArray) != 1 {
		t.Fatalf("expected mempool tx to be re-emittable after confirmed clear, got %d items", len(third.TxArray))
	}
}

func TestFetchMemPoolSkipsInvalidOrUnattributedOutgoing(t *testing.T) {
	t.Parallel()

	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	vaultAddress := vaultAddr.String()
	_, mismatchedAddr := testVaultPubKeyAndAddress(t)
	destinationAddress := common.XMRChecksummedDummyAddress('8')

	frostServer := newFrostTransfersServer(
		t,
		[]sidecar.Transfer{
			{
				TxID:    "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				Address: vaultAddress,
				Amount:  100000,
				Fee:     10000,
				Height:  77,
			},
		},
		[]sidecar.Transfer{
			{
				TxID:        "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
				Address:     destinationAddress,
				FromAddress: vaultAddress,
				VaultPubKey: vaultPubKey.String(),
				Amount:      100000,
				Fee:         10000,
				Height:      77,
			},
			{
				TxID:        "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
				Address:     destinationAddress,
				FromAddress: vaultAddress,
				VaultPubKey: "",
				Amount:      100000,
				Fee:         10000,
				Height:      77,
			},
			{
				TxID:        "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
				Address:     destinationAddress,
				FromAddress: mismatchedAddr.String(),
				VaultPubKey: vaultPubKey.String(),
				Amount:      100000,
				Fee:         10000,
				Height:      77,
			},
		},
		[]string{vaultAddress},
	)
	defer frostServer.Close()

	cfg := config.BifrostChainConfiguration{}
	frostSigner, err := sidecar.NewClientWithEndpoint(cfg, frostServer.URL)
	if err != nil {
		t.Fatalf("new frost signer: %v", err)
	}

	scanner := NewScanner(log.Logger, frostSigner, nil, common.XMRChain, common.EmptyPubKey, 72)
	txIn, err := scanner.FetchMemPool(200)
	if err != nil {
		t.Fatalf("fetch mempool: %v", err)
	}
	if len(txIn.TxArray) != 1 {
		t.Fatalf("expected only 1 valid outgoing mempool tx, got %d", len(txIn.TxArray))
	}
	if txIn.TxArray[0].Tx != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected tx emitted from mempool: %s", txIn.TxArray[0].Tx)
	}
}
