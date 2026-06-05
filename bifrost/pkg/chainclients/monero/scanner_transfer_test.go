package monero

import (
	"testing"

	"gitlab.com/thorchain/thornode/v3/bifrost/pkg/chainclients/monero/sidecar"

	"github.com/rs/zerolog/log"
	"go.uber.org/atomic"

	"gitlab.com/thorchain/thornode/v3/common"
)

func TestUnknownSenderAddress(t *testing.T) {
	addr := unknownSenderAddress("tx-hash-a", "8abcd")
	parsed, err := common.NewAddress(addr)
	if err != nil {
		t.Fatalf("expected parseable unknown sender marker address: %v", err)
	}
	if parsed.IsChain(common.XMRChain) {
		t.Fatalf("expected unknown sender marker to not be chain-valid XMR")
	}
	if !common.IsXMRUnknownSenderAddress(parsed) {
		t.Fatalf("expected unknown sender marker to be detectable")
	}

	mockAddr := unknownSenderAddress("tx-hash-b", "4abcd")
	parsedMock, err := common.NewAddress(mockAddr)
	if err != nil {
		t.Fatalf("expected parseable unknown sender marker for alternate tx hash: %v", err)
	}
	if !common.IsXMRUnknownSenderAddress(parsedMock) {
		t.Fatalf("expected alternate unknown sender marker to be detectable")
	}
	if addr == mockAddr {
		t.Fatalf("expected unique marker addresses per tx hash")
	}
}

func TestTransferToTxInItemUsesFeeFallback(t *testing.T) {
	pk := common.PubKey("thorpub1testvault")

	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(123 * moneroAtomicToThorchain),
	}
	item, ok := s.transferToTxInItem(sidecar.Transfer{
		TxID:   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		Amount: 10000,
		Fee:    0,
	}, unknownSenderAddress("sender", "8abc"), unknownSenderAddress("receiver", "8def"), pk, "", 100)
	if !ok {
		t.Fatal("expected transfer to convert to tx in item")
	}
	if item.Gas[0].Amount.Uint64() != 123 {
		t.Fatalf("expected fallback fee 123, got %d", item.Gas[0].Amount.Uint64())
	}
}

func TestResolveOutgoingSenderPrefersExplicitMetadata(t *testing.T) {
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(0),
	}

	sender, pk := s.resolveOutgoingSender(sidecar.Transfer{
		TxID:        "tx-1",
		FromAddress: vaultAddr.String(),
		VaultPubKey: vaultPubKey.String(),
	})
	if sender != vaultAddr.String() {
		t.Fatalf("expected explicit sender %s, got %s", vaultAddr, sender)
	}
	if !pk.Equals(vaultPubKey) {
		t.Fatalf("expected explicit vault pubkey %s, got %s", vaultPubKey, pk)
	}
}

func TestGetNetworkFeeUsesTypicalWeightAndPerWeightRate(t *testing.T) {
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(30_000 * typicalVaultOutboundWeight),
	}

	txSize, feeRate := s.GetNetworkFee()
	if txSize != typicalVaultOutboundWeight {
		t.Fatalf("expected tx size %d, got %d", typicalVaultOutboundWeight, txSize)
	}
	if feeRate != 30_000 {
		t.Fatalf("expected fee rate 30000 piconero/weight, got %d", feeRate)
	}
}

func TestGetNetworkFeePreservesNativePiconeroUnits(t *testing.T) {
	expectedTotalFee := uint64(120_000 * typicalVaultOutboundWeight)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(expectedTotalFee),
	}

	txSize, feeRate := s.GetNetworkFee()
	if txSize != typicalVaultOutboundWeight {
		t.Fatalf("expected tx size %d, got %d", typicalVaultOutboundWeight, txSize)
	}
	if feeRate != 120_000 {
		t.Fatalf("expected fee rate 120000 piconero/weight, got %d", feeRate)
	}
	if txSize*feeRate != expectedTotalFee {
		t.Fatalf("expected total network fee %d piconero, got %d", expectedTotalFee, txSize*feeRate)
	}
	expectedThorchainFee := expectedTotalFee / moneroAtomicToThorchain
	if totalFeeAtomicToThorchain(txSize*feeRate) != expectedThorchainFee {
		t.Fatalf("expected total network fee %d thorchain units, got %d", expectedThorchainFee, totalFeeAtomicToThorchain(txSize*feeRate))
	}
}

func TestResolveOutgoingSenderRejectsMissingExplicitMetadata(t *testing.T) {
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(0),
	}

	sender, pk := s.resolveOutgoingSender(sidecar.Transfer{
		TxID:        "tx-missing-sender",
		FromAddress: "",
		VaultPubKey: vaultPubKey.String(),
	})
	if sender != "" || !pk.IsEmpty() {
		t.Fatalf("expected missing sender to fail closed, got sender=%q pk=%s", sender, pk.String())
	}

	sender, pk = s.resolveOutgoingSender(sidecar.Transfer{
		TxID:        "tx-missing-vault",
		FromAddress: vaultAddr.String(),
		VaultPubKey: "",
	})
	if sender != "" || !pk.IsEmpty() {
		t.Fatalf("expected missing vault key to fail closed, got sender=%q pk=%s", sender, pk.String())
	}
}

func TestResolveOutgoingSenderRejectsSenderVaultMismatch(t *testing.T) {
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	_, otherAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(0),
	}

	sender, pk := s.resolveOutgoingSender(sidecar.Transfer{
		TxID:        "tx-mismatch",
		FromAddress: otherAddr.String(),
		VaultPubKey: vaultPubKey.String(),
	})
	if sender != "" || !pk.IsEmpty() {
		t.Fatalf("expected sender/vault mismatch to fail closed, got sender=%q pk=%s", sender, pk.String())
	}
}
