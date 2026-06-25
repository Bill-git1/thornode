package observer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	tctypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	memo "gitlab.com/thorchain/thornode/v3/x/thorchain/memo"
	thortypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// observerTestHash gives tests deterministic 32-byte hex values that satisfy
// XMR spent-ref validation without depending on real Monero fixtures.
func observerTestHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// observerTestPubKey converts a generated secp256k1 key into the bech32/common
// pubkey format used by vault membership. The gossip attestation itself carries
// the raw secp bytes, so tests need both representations.
func observerTestPubKey(t *testing.T, priv *secp256k1.PrivKey) common.PubKey {
	t.Helper()

	pk, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, priv.PubKey())
	require.NoError(t, err)
	pubKey, err := common.NewPubKey(pk)
	require.NoError(t, err)
	return pubKey
}

// observerTestOutputRef builds a self-consistent spent ref whose SpendTxHash
// matches the observed XMR tx. The sidecar import gate rejects mismatched spend
// hashes before it ever reaches sidecar verification.
func observerTestOutputRef(seed, spend string) common.OutputRef {
	return common.OutputRef{
		TxHash:      observerTestHash(seed + "-tx"),
		OutputIndex: 0,
		KeyImage:    observerTestHash(seed + "-ki"),
		SpendTxHash: spend,
	}
}

// observerTestXMRPlan returns a matching observed XMR outbound and planned
// THORChain txout. Individual tests mutate one field at a time to make sure the
// peer-import gate fails closed on mismatches.
func observerTestXMRPlan(t *testing.T) (common.ObservedTx, plannedXMROutbound) {
	t.Helper()

	txID := common.TxID(observerTestHash("xmr-peer-spent-ref-spend"))
	vaultPubKey := randomXMRObservedPubKey(t)
	vaultAddr, err := vaultPubKey.GetAddress(common.XMRChain)
	require.NoError(t, err)

	to := common.Address(common.XMRSeededDummyAddress('8', []byte("xmr-peer-spent-ref-dest")))
	memo := "MIGRATE:123"
	coins := common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(500_000_000)))
	gas := common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(2))}

	obsTx := common.NewObservedTx(
		common.Tx{
			ID:          txID,
			Chain:       common.XMRChain,
			FromAddress: vaultAddr,
			ToAddress:   to,
			Coins:       coins,
			Gas:         gas,
			Memo:        memo,
		},
		7,
		vaultPubKey,
		8,
	)
	obsTx.SpentOutputRefs = []common.OutputRef{observerTestOutputRef("xmr-peer-spent-ref", txID.String())}

	planned := plannedXMROutbound{
		chain:            common.XMRChain,
		toAddress:        to,
		vaultPubKeyFrost: vaultPubKey,
		coins:            coins,
		memo:             memo,
		maxGas:           common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(5))},
	}

	return obsTx, planned
}

// TestXMRPlannedOutboundMatcherRejectsMismatches covers the THORChain-intent
// half of the peer import gate. The sidecar can verify Monero key images, but
// only Bifrost can verify that the observation matches a planned outbound.
func TestXMRPlannedOutboundMatcherRejectsMismatches(t *testing.T) {
	obsTx, planned := observerTestXMRPlan(t)

	t.Run("matches exact planned outbound", func(t *testing.T) {
		assert.True(t, xmrObservedTxMatchesPlanned(obsTx, planned))
	})

	t.Run("accepts migration amount with exact unused max gas sweep", func(t *testing.T) {
		mutated := obsTx
		plannedCoin := planned.coins.GetCoin(common.XMRAsset)
		observedGas := mutated.Tx.Gas.ToCoins().GetCoin(common.XMRAsset)
		plannedMaxGas := planned.maxGas.ToCoins().GetCoin(common.XMRAsset)
		unusedMaxGas := common.SafeSub(plannedMaxGas.Amount, observedGas.Amount)
		mutated.Tx.Coins = common.NewCoins(common.NewCoin(common.XMRAsset, plannedCoin.Amount.Add(unusedMaxGas)))
		assert.True(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("accepts migration amount rounded down for non-aligned atomic fee", func(t *testing.T) {
		mutated := obsTx
		plannedCoin := planned.coins.GetCoin(common.XMRAsset)
		mutated.Tx.Gas = common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(1))}
		// feeAtomic=12345 floors to observed gas 1, while the swept unused
		// MaxGas is floor((5*10000 - 12345)/10000) = 3.
		mutated.Tx.Coins = common.NewCoins(common.NewCoin(common.XMRAsset, plannedCoin.Amount.Add(cosmos.NewUint(3))))
		assert.True(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects migration amount above unused max gas sweep", func(t *testing.T) {
		mutated := obsTx
		plannedCoin := planned.coins.GetCoin(common.XMRAsset)
		mutated.Tx.Coins = common.NewCoins(common.NewCoin(common.XMRAsset, plannedCoin.Amount.Add(cosmos.NewUint(4))))
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects wrong destination", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.ToAddress = common.Address(common.XMRSeededDummyAddress('8', []byte("wrong-xmr-dest")))
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects wrong amount", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.Coins = common.NewCoins(common.NewCoin(common.XMRAsset, cosmos.NewUint(1)))
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects wrong memo", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.Memo = "MIGRATE:456"
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects wrong vault", func(t *testing.T) {
		mutated := obsTx
		mutated.ObservedPubKey = randomXMRObservedPubKey(t)
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects wrong source address", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.FromAddress = common.Address(common.XMRSeededDummyAddress('8', []byte("wrong-xmr-source")))
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects zero gas", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.Gas = common.Gas{common.NewCoin(common.XMRAsset, cosmos.ZeroUint())}
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("rejects gas above planned max", func(t *testing.T) {
		mutated := obsTx
		mutated.Tx.Gas = common.Gas{common.NewCoin(common.XMRAsset, cosmos.NewUint(6))}
		assert.False(t, xmrObservedTxMatchesPlanned(mutated, planned))
	})

	t.Run("accepts missing planned max gas", func(t *testing.T) {
		mutatedPlan := planned
		mutatedPlan.maxGas = nil
		assert.True(t, xmrObservedTxMatchesPlanned(obsTx, mutatedPlan))
	})
}

// TestPeerXMRSpentRefsRequirePlannedOutboundAndSourceVaultMember verifies that
// pre-quorum peer refs are importable only when the peer is allowed to introduce
// refs for this source vault and the observed tx matches THORChain's plan.
//
// These tests intentionally assert that no local attestation is added: peer
// import only seeds the sidecar so the scanner can later observe independently.
func TestPeerXMRSpentRefsRequirePlannedOutboundAndSourceVaultMember(t *testing.T) {
	origVerifySignature := verifySignature
	defer func() { verifySignature = origVerifySignature }()
	// The tests below focus on the authorization gates that run after
	// AddAttestation. Signature verification itself is covered elsewhere, so
	// make every synthetic attestation valid for this file.
	verifySignature = func(signBz, signature, attester []byte) error { return nil }

	t.Run("without planned outbound match", func(t *testing.T) {
		ag, _, _, _, bridge, _ := setupTestGossip(t)
		obsTx, _ := observerTestXMRPlan(t)
		attester := secp256k1.GenPrivKey()
		attesterPubKey := observerTestPubKey(t, attester)
		ag.setActiveValidators(common.PubKeys{attesterPubKey})
		// The attester is active and belongs to the source vault, but the
		// keysign lookup returns no planned txout. This proves source membership
		// alone is not enough to import peer refs.
		bridge.getVaultFunc = observerTestVaultFunc(obsTx.ObservedPubKey, attesterPubKey)
		bridge.getKeysignFunc = func(height int64, pk string) (tctypes.TxOut, error) {
			return tctypes.TxOut{Height: height}, nil
		}

		callback := assertNoSpentRefCallback(t, ag)
		ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, attester))
		callback()
		assertObserverStateHasOnlyPeerAttestation(t, ag, obsTx)
	})

	t.Run("active non-source-vault member", func(t *testing.T) {
		ag, _, _, _, bridge, _ := setupTestGossip(t)
		obsTx, planned := observerTestXMRPlan(t)
		attester := secp256k1.GenPrivKey()
		attesterPubKey := observerTestPubKey(t, attester)
		otherMember := observerTestPubKey(t, secp256k1.GenPrivKey())
		ag.setActiveValidators(common.PubKeys{attesterPubKey})
		// The planned outbound exists, but this active peer is not in the source
		// vault membership. Newly churned-in validators may import refs, but
		// they cannot introduce refs for an old-vault spend.
		bridge.getVaultFunc = observerTestVaultFunc(obsTx.ObservedPubKey, otherMember)
		bridge.getKeysignFunc = observerTestKeysignFunc(planned)

		callback := assertNoSpentRefCallback(t, ag)
		ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, attester))
		callback()
		assertObserverStateHasOnlyPeerAttestation(t, ag, obsTx)
	})

	t.Run("inactive source-vault member", func(t *testing.T) {
		ag, _, _, _, bridge, _ := setupTestGossip(t)
		obsTx, planned := observerTestXMRPlan(t)
		attester := secp256k1.GenPrivKey()
		attesterPubKey := observerTestPubKey(t, attester)
		// The peer belongs to the source vault and the tx matches the plan, but
		// it is not active. Churned-out vault members must not introduce refs.
		ag.setActiveValidators(nil)
		bridge.getVaultFunc = observerTestVaultFunc(obsTx.ObservedPubKey, attesterPubKey)
		bridge.getKeysignFunc = observerTestKeysignFunc(planned)

		callback := assertNoSpentRefCallback(t, ag)
		ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, attester))
		callback()
		assertObserverStateHasOnlyPeerAttestation(t, ag, obsTx)
	})

	t.Run("source-vault member with planned outbound match", func(t *testing.T) {
		ag, _, _, _, bridge, _ := setupTestGossip(t)
		obsTx, planned := observerTestXMRPlan(t)
		attester := secp256k1.GenPrivKey()
		attesterPubKey := observerTestPubKey(t, attester)
		ag.setActiveValidators(common.PubKeys{attesterPubKey})
		// This is the permitted peer path: an active source-vault member signs
		// the exact observation and that observation matches a planned txout.
		bridge.getVaultFunc = observerTestVaultFunc(obsTx.ObservedPubKey, attesterPubKey)
		bridge.getKeysignFunc = observerTestKeysignFunc(planned)

		applied := make(chan common.ObservedTx, 1)
		ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx) {
			applied <- tx
		})

		ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, attester))

		select {
		case got := <-applied:
			assert.Equal(t, obsTx.Tx.ID, got.Tx.ID)
			assert.Equal(t, obsTx.SpentOutputRefs, got.SpentOutputRefs)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for peer spent-ref import")
		}
		assertObserverStateHasOnlyPeerAttestation(t, ag, obsTx)
	})
}

// TestPeerXMRSpentRefsUnauthorizedFirstDoesNotBlockAuthorizedImport protects
// the state-machine detail that spentRefsApplied is set only after a peer
// attestation passes authorization. A bad early peer should not be able to DoS a
// later valid source-vault peer import for the same observed tx/ref set.
func TestPeerXMRSpentRefsUnauthorizedFirstDoesNotBlockAuthorizedImport(t *testing.T) {
	origVerifySignature := verifySignature
	defer func() { verifySignature = origVerifySignature }()
	// This test cares about the ordering of the XMR authorization gate, not
	// cryptographic signature mechanics. Use distinct fake signatures so both
	// peer attestations are accepted into the same state bucket.
	verifySignature = func(signBz, signature, attester []byte) error { return nil }

	ag, _, _, _, bridge, _ := setupTestGossip(t)
	obsTx, planned := observerTestXMRPlan(t)
	unauthorized := secp256k1.GenPrivKey()
	authorized := secp256k1.GenPrivKey()
	authorizedPubKey := observerTestPubKey(t, authorized)
	ag.setActiveValidators(common.PubKeys{authorizedPubKey})
	bridge.getVaultFunc = observerTestVaultFunc(obsTx.ObservedPubKey, authorizedPubKey)
	bridge.getKeysignFunc = observerTestKeysignFunc(planned)

	applied := make(chan common.ObservedTx, 1)
	ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx) {
		applied <- tx
	})

	// This first peer is neither active nor in the source vault. It should not
	// set spentRefsApplied, otherwise the authorized peer below would be blocked.
	ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, unauthorized))
	assertNoSpentRefImportDuring(t, applied, 100*time.Millisecond)

	ag.handleObservedTxAttestation(context.Background(), observerTestAttestTx(obsTx, authorized))

	select {
	case got := <-applied:
		assert.Equal(t, obsTx.Tx.ID, got.Tx.ID)
		assert.Equal(t, obsTx.SpentOutputRefs, got.SpentOutputRefs)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for authorized peer spent-ref import")
	}
}

// TestXMRSpentRefGateFailsClosedForNilAttestation verifies the import gate does
// not treat malformed/empty gossip as source authority for sidecar mutation.
func TestXMRSpentRefGateFailsClosedForNilAttestation(t *testing.T) {
	ag, _, _, _, _, _ := setupTestGossip(t)
	obsTx, _ := observerTestXMRPlan(t)

	assert.False(t, ag.shouldApplyXMRSpentRefsFromAttestation(obsTx, nil))
}

// TestXMRPlannedOutboundMatcherUsesInboundVoterActions covers the OUT/REFUND
// style path, where THORChain intent comes from the inbound observed-tx voter
// actions instead of the keysign-height txout used by MIGRATE/RAGNAROK.
func TestXMRPlannedOutboundMatcherUsesInboundVoterActions(t *testing.T) {
	ag, _, _, _, bridge, _ := setupTestGossip(t)
	obsTx, planned := observerTestXMRPlan(t)
	inboundID := common.TxID(observerTestHash("xmr-peer-spent-ref-inbound"))
	obsTx.Tx.Memo = memo.NewOutboundMemo(inboundID).String()
	planned.memo = obsTx.Tx.Memo
	bridge.getObservedTxVoterFunc = observerTestObservedTxVoterFunc(inboundID, planned)

	assert.True(t, ag.observedXMRMatchesPlannedOutbound(obsTx))

	// Mutating the observed tx after the voter action is fixed should break the
	// match, proving this path compares the full planned outbound rather than
	// accepting the memo/inbound ID alone.
	obsTx.Tx.ToAddress = common.Address(common.XMRSeededDummyAddress('8', []byte("wrong-voter-action-dest")))
	assert.False(t, ag.observedXMRMatchesPlannedOutbound(obsTx))
}

// TestXMRPlannedOutboundMatcherUsesKeysignHeightForRagnarok verifies the
// RAGNAROK path uses the same keysign-height planned txout lookup as MIGRATE.
// Without this path, pre-quorum peer spent-ref imports would be disabled for
// XMR ragnarok outbounds even when the observation exactly matches the plan.
func TestXMRPlannedOutboundMatcherUsesKeysignHeightForRagnarok(t *testing.T) {
	ag, _, _, _, bridge, _ := setupTestGossip(t)
	obsTx, planned := observerTestXMRPlan(t)
	ragnarokMemo := memo.NewRagnarokMemo(123).String()
	obsTx.Tx.Memo = ragnarokMemo
	planned.memo = ragnarokMemo
	bridge.getKeysignFunc = observerTestKeysignFunc(planned)

	assert.True(t, ag.observedXMRMatchesPlannedOutbound(obsTx))

	// The same RAGNAROK memo must still fail closed when the planned txout no
	// longer matches the observed outbound.
	obsTx.Tx.ToAddress = common.Address(common.XMRSeededDummyAddress('8', []byte("wrong-ragnarok-dest")))
	assert.False(t, ag.observedXMRMatchesPlannedOutbound(obsTx))
}

// TestXMRPlannedOutboundMatcherAcceptsLegacyVaultPubKey keeps the matcher
// compatible with planned txouts that do not carry PubKeyFrost yet. XMR
// monovault records normally use PubKeyFrost, but the fallback avoids rejecting
// older/non-FROST txout shapes.
func TestXMRPlannedOutboundMatcherAcceptsLegacyVaultPubKey(t *testing.T) {
	obsTx, planned := observerTestXMRPlan(t)
	planned.vaultPubKey = planned.vaultPubKeyFrost
	planned.vaultPubKeyFrost = common.PubKey("")

	assert.True(t, xmrObservedTxMatchesPlanned(obsTx, planned))
}

// observerTestAttestTx builds the peer attestation envelope consumed by
// handleObservedTxAttestation. The fake signature is accepted by the overridden
// verifier in TestPeerXMRSpentRefsRequirePlannedOutboundAndSourceVaultMember.
func observerTestAttestTx(obsTx common.ObservedTx, attester *secp256k1.PrivKey) common.AttestTx {
	return common.AttestTx{
		ObsTx:   obsTx,
		Inbound: false,
		Attestation: &common.Attestation{
			PubKey:    attester.PubKey().Bytes(),
			Signature: append([]byte("sig-"), attester.PubKey().Bytes()...),
		},
	}
}

// observerTestVaultFunc returns a source vault with explicit membership so the
// peer-import tests can distinguish "active validator" from "source-vault
// member". Both gates are required for peer refs.
func observerTestVaultFunc(vaultPubKey common.PubKey, members ...common.PubKey) func(string) (thortypes.Vault, error) {
	return func(pubkey string) (thortypes.Vault, error) {
		if pubkey != vaultPubKey.String() {
			return thortypes.Vault{}, nil
		}
		return thortypes.Vault{
			PubKey:      vaultPubKey,
			PubKeyFrost: vaultPubKey,
			Membership:  common.PubKeys(members).Strings(),
		}, nil
	}
}

// observerTestKeysignFunc returns a keysign block containing the planned XMR
// outbound. The matcher calls this with an empty pubkey to exercise the
// height-only /thorchain/keysign/{height} path used by newly churned-in nodes.
func observerTestKeysignFunc(planned plannedXMROutbound) func(int64, string) (tctypes.TxOut, error) {
	return func(height int64, pk string) (tctypes.TxOut, error) {
		return tctypes.TxOut{
			Height: height,
			TxArray: []tctypes.TxArrayItem{
				{
					Chain:            planned.chain,
					ToAddress:        planned.toAddress,
					VaultPubKey:      planned.vaultPubKey,
					VaultPubKeyFrost: planned.vaultPubKeyFrost,
					Coin:             planned.coins.GetCoin(common.XMRAsset),
					Memo:             planned.memo,
					MaxGas:           planned.maxGas,
				},
			},
		}, nil
	}
}

// observerTestObservedTxVoterFunc returns a voter with one planned action. It
// mirrors THORChain's OUT/REFUND lookup path while keeping the test independent
// from a full keeper/voter fixture.
func observerTestObservedTxVoterFunc(inboundID common.TxID, planned plannedXMROutbound) func(string) (*thortypes.QueryObservedTxVoter, error) {
	return func(txID string) (*thortypes.QueryObservedTxVoter, error) {
		if !strings.EqualFold(txID, inboundID.String()) {
			return nil, nil
		}
		return &thortypes.QueryObservedTxVoter{
			TxID: inboundID,
			Actions: []thortypes.TxOutItem{
				{
					Chain:            planned.chain,
					ToAddress:        planned.toAddress,
					VaultPubKey:      planned.vaultPubKey,
					VaultPubKeyFrost: planned.vaultPubKeyFrost,
					Coin:             planned.coins.GetCoin(common.XMRAsset),
					Memo:             planned.memo,
					MaxGas:           planned.maxGas,
				},
			},
		}, nil
	}
}

// assertNoSpentRefCallback installs a callback that fails the test if the
// pre-quorum peer import path fires. The short wait gives the async callback
// path time to run without making negative tests slow.
func assertNoSpentRefCallback(t *testing.T, ag *AttestationGossip) func() {
	t.Helper()

	applied := make(chan common.ObservedTx, 1)
	ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx) {
		applied <- tx
	})
	return func() {
		t.Helper()
		select {
		case tx := <-applied:
			t.Fatalf("unexpected spent-ref import for %s", tx.Tx.ID)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// assertNoSpentRefImportDuring is used when the same callback channel later
// needs to receive a valid import. Unlike assertNoSpentRefCallback, it does not
// install a new callback and therefore preserves the positive assertion path.
func assertNoSpentRefImportDuring(t *testing.T, applied <-chan common.ObservedTx, timeout time.Duration) {
	t.Helper()

	select {
	case tx := <-applied:
		t.Fatalf("unexpected spent-ref import for %s", tx.Tx.ID)
	case <-time.After(timeout):
	}
}

// assertObserverStateHasOnlyPeerAttestation checks the important safety
// invariant: accepting peer refs for possible sidecar import does not create a
// local attestation or blind co-vote.
func assertObserverStateHasOnlyPeerAttestation(t *testing.T, ag *AttestationGossip, obsTx common.ObservedTx) {
	t.Helper()

	k := observedTxKey(obsTx, false, false)
	ag.mu.Lock()
	state, exists := ag.observedTxs[k]
	ag.mu.Unlock()
	require.True(t, exists)

	state.mu.Lock()
	defer state.mu.Unlock()
	assert.Equal(t, 1, state.AttestationCount())
}
