package monero

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/edwards25519"
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"

	"gitlab.com/thorchain/thornode/v3/common"
)

func testVaultPubKey(t *testing.T, seed string) common.PubKey {
	t.Helper()
	hasher := sha512.New()
	hasher.Write([]byte("thorchain xmr test vault pubkey"))
	hasher.Write([]byte(seed))
	sum := hasher.Sum(nil)
	scalar, err := new(edwards25519.Scalar).SetUniformBytes(sum)
	if err != nil {
		t.Fatalf("test xmr scalar: %v", err)
	}
	point := new(edwards25519.Point).ScalarBaseMult(scalar)
	pk, err := common.NewPubKeyFromCrypto(cometed25519.PubKey(point.Bytes()))
	if err != nil {
		t.Fatalf("test xmr pubkey: %v", err)
	}
	return pk
}

func testOutboundXMRDestination() common.Address {
	if common.CurrentChainNetwork == common.MockNet {
		return common.Address(common.XMRChecksummedDummyAddress('8'))
	}
	return common.Address(common.XMRChecksummedDummyAddress('4'))
}

func testXMRHash(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(sum[:])
}

func testXMROutputRef(label string, outputIndex uint32, spendTxHash string) *common.OutputRef {
	return &common.OutputRef{
		TxHash:      testXMRHash(label + ":tx"),
		OutputIndex: outputIndex,
		KeyImage:    testXMRHash(label + ":key-image"),
		SpendTxHash: spendTxHash,
	}
}

var testVaultAddressCounter atomic.Uint64

func testVaultPubKeyAndAddress(t *testing.T) (common.PubKey, common.Address) {
	t.Helper()

	seed := fmt.Sprintf("vault-address:%s:%d", t.Name(), testVaultAddressCounter.Add(1))
	pubKey := testVaultPubKey(t, seed)
	address, err := pubKey.GetAddress(common.XMRChain)
	if err != nil {
		t.Fatalf("derive monero address: %v", err)
	}
	return pubKey, address
}

// expectedXMRVaultAddress is the pure helper behind expectedXMRVaultAddressFromKeygenRequest.
func expectedXMRVaultAddress(vaultPubKeyFrost common.PubKey) (common.Address, error) {
	if vaultPubKeyFrost.IsEmpty() {
		return common.NoAddress, fmt.Errorf("xmr keygen requires a non-empty frost vault pubkey")
	}
	expected, err := vaultPubKeyFrost.GetAddress(common.XMRChain)
	if err != nil {
		return common.NoAddress, fmt.Errorf("fail to derive monero address from vault pubkey %s: %w", vaultPubKeyFrost.String(), err)
	}
	if expected.IsEmpty() {
		return common.NoAddress, fmt.Errorf("monero vault pubkey %s derived an empty address", vaultPubKeyFrost.String())
	}
	return expected, nil
}

// expectedXMRVaultAddressFromKeygenRequest is exercised by client_keygen_test.go and
// scanner_test.go to verify that the keygen handshake computes the same address the
// chain expects from a vault pubkey.
func expectedXMRVaultAddressFromKeygenRequest(vaultPubKeyFrost string) (common.Address, error) {
	value := strings.TrimSpace(vaultPubKeyFrost)
	if value == "" {
		return expectedXMRVaultAddress(common.EmptyPubKey)
	}
	pubKey, err := common.NewPubKey(value)
	if err != nil {
		return common.NoAddress, fmt.Errorf("invalid vault pubkey %q: %w", value, err)
	}
	return expectedXMRVaultAddress(pubKey)
}
