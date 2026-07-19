package monero

import (
	"strings"
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

// TestTransferToTxInItemEchoesSpentOutputRefs verifies a non-signing observer
// mirrors the sidecar-verified refs of an outgoing transfer into its
// observation, in sidecar order with SpendTxHash set to the observed tx, so
// signer and peer attestations stay identical at quorum.
func TestTransferToTxInItemEchoesSpentOutputRefs(t *testing.T) {
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(123 * moneroAtomicToThorchain),
	}

	transfer := sidecar.Transfer{
		TxID:        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		FromAddress: vaultAddr.String(),
		VaultPubKey: vaultPubKey.String(),
		Amount:      10000,
		SpentOutputRefs: []sidecar.SpentRef{
			{TxHash: "funding-tx-b", OutputIndex: 2, KeyImage: "key-image-b"},
			{TxHash: "funding-tx-a", OutputIndex: 0, KeyImage: "key-image-a"},
		},
	}
	item, ok := s.transferToTxInItem(transfer, vaultAddr.String(), unknownSenderAddress("receiver", "8def"), vaultPubKey, "", 100)
	if !ok {
		t.Fatal("expected transfer to convert to tx in item")
	}
	if len(item.SpentOutputRefs) != 2 {
		t.Fatalf("expected 2 echoed spent refs, got %d", len(item.SpentOutputRefs))
	}
	// Sidecar order preserved, SpendTxHash bound to the observed tx.
	if item.SpentOutputRefs[0].TxHash != "funding-tx-b" || item.SpentOutputRefs[0].OutputIndex != 2 || item.SpentOutputRefs[0].KeyImage != "key-image-b" {
		t.Fatalf("unexpected first echoed ref: %+v", item.SpentOutputRefs[0])
	}
	if item.SpentOutputRefs[1].TxHash != "funding-tx-a" {
		t.Fatalf("unexpected second echoed ref: %+v", item.SpentOutputRefs[1])
	}
	for _, ref := range item.SpentOutputRefs {
		if ref.SpendTxHash != transfer.TxID {
			t.Fatalf("expected spend tx hash %s, got %s", transfer.TxID, ref.SpendTxHash)
		}
	}
}

// TestTransferToTxInItemDoesNotEchoRefsForNonVaultSender fails closed if the
// sidecar attaches refs to a transfer whose sender is not the vault: refs on
// an inbound observation would make thornode reject the whole observed tx.
func TestTransferToTxInItemDoesNotEchoRefsForNonVaultSender(t *testing.T) {
	vaultPubKey, _ := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(123 * moneroAtomicToThorchain),
	}

	item, ok := s.transferToTxInItem(sidecar.Transfer{
		TxID:   "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB",
		Amount: 10000,
		SpentOutputRefs: []sidecar.SpentRef{
			{TxHash: "funding-tx-a", OutputIndex: 0, KeyImage: "key-image-a"},
		},
	}, unknownSenderAddress("sender", "8abc"), unknownSenderAddress("receiver", "8def"), vaultPubKey, "", 100)
	if !ok {
		t.Fatal("expected transfer to convert to tx in item")
	}
	if len(item.SpentOutputRefs) != 0 {
		t.Fatalf("expected no echoed refs for non-vault sender, got %d", len(item.SpentOutputRefs))
	}
}

// TestTransferToTxInItemDoesNotEchoRefsForCaseVariantVaultSender verifies XMR
// address identity remains case-sensitive when deciding whether an observation
// is an outbound authorized to carry spent-output references.
func TestTransferToTxInItemDoesNotEchoRefsForCaseVariantVaultSender(t *testing.T) {
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(123 * moneroAtomicToThorchain),
	}

	caseVariant := strings.ToLower(vaultAddr.String())
	if caseVariant == vaultAddr.String() {
		caseVariant = strings.ToUpper(vaultAddr.String())
	}
	if caseVariant == vaultAddr.String() || !strings.EqualFold(caseVariant, vaultAddr.String()) {
		t.Fatal("expected a distinct case-only variant of the vault address")
	}

	item, ok := s.transferToTxInItem(sidecar.Transfer{
		TxID:        "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD",
		FromAddress: caseVariant,
		VaultPubKey: vaultPubKey.String(),
		Amount:      10000,
		SpentOutputRefs: []sidecar.SpentRef{
			{TxHash: "funding-tx-a", OutputIndex: 0, KeyImage: "key-image-a"},
		},
	}, caseVariant, unknownSenderAddress("receiver", "8def"), vaultPubKey, "", 100)
	if !ok {
		t.Fatal("expected transfer to convert to tx in item")
	}
	if len(item.SpentOutputRefs) != 0 {
		t.Fatalf("expected no echoed refs for case-variant vault sender, got %d", len(item.SpentOutputRefs))
	}
}

// TestTransferToTxInItemDropsInvalidEchoedRefs verifies a ref set that would
// not survive ObservedTx validation (missing key image) is dropped whole
// instead of poisoning the observation.
func TestTransferToTxInItemDropsInvalidEchoedRefs(t *testing.T) {
	vaultPubKey, vaultAddr := testVaultPubKeyAndAddress(t)
	s := &Scanner{
		logger:             log.Logger,
		currentBlockHeight: atomic.NewInt64(0),
		lastFee:            atomic.NewUint64(123 * moneroAtomicToThorchain),
	}

	item, ok := s.transferToTxInItem(sidecar.Transfer{
		TxID:        "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC",
		FromAddress: vaultAddr.String(),
		VaultPubKey: vaultPubKey.String(),
		Amount:      10000,
		SpentOutputRefs: []sidecar.SpentRef{
			{TxHash: "funding-tx-a", OutputIndex: 0, KeyImage: "key-image-a"},
			{TxHash: "funding-tx-b", OutputIndex: 1, KeyImage: ""},
		},
	}, vaultAddr.String(), unknownSenderAddress("receiver", "8def"), vaultPubKey, "", 100)
	if !ok {
		t.Fatal("expected transfer to convert to tx in item")
	}
	if len(item.SpentOutputRefs) != 0 {
		t.Fatalf("expected invalid echoed ref set to be dropped, got %d refs", len(item.SpentOutputRefs))
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
