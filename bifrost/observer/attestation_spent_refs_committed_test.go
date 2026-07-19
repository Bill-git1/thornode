package observer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
)

// Test 3 (partial): handleQuorumTxCommitted applies spent output refs for
// XMR outbounds regardless of whether this node attested.
func TestQuorumTxCommittedAppliesXMRSpentRefs(t *testing.T) {
	ag, _, _, _, _, _ := setupTestGossip(t)

	t.Run("applies spent refs for XMR outbound on committed event", func(t *testing.T) {
		var callbackTx *common.ObservedTx
		ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx, onOutcome func(applied bool)) {
			callbackTx = &tx
			if onOutcome != nil {
				onOutcome(true)
			}
		})

		refs := []common.OutputRef{
			{TxHash: "spent1", OutputIndex: 0, KeyImage: "ki_spent1", SpendTxHash: "xmr-committed-1"},
			{TxHash: "spent2", OutputIndex: 1, KeyImage: "ki_spent2", SpendTxHash: "xmr-committed-1"},
		}
		tx := common.ObservedTx{
			Tx: common.Tx{
				ID:    "xmr-committed-1",
				Chain: common.XMRChain,
			},
			ObservedPubKey:  "pk-frost",
			SpentOutputRefs: refs,
		}

		qtx := common.QuorumTx{
			ObsTx:   tx,
			Inbound: false,
			Attestations: []*common.Attestation{
				{PubKey: []byte("other-node"), Signature: []byte("sig")},
			},
		}

		payload, err := qtx.Marshal()
		require.NoError(t, err)

		// Pre-populate the observedTxs map so handleQuorumTxCommitted doesn't
		// return early at the "!ok" check. We need a matching txKey.
		k := txKey{
			Chain:     common.XMRChain,
			ID:        "xmr-committed-1",
			Finalized: tx.IsFinal(),
		}
		ag.mu.Lock()
		ag.observedTxs[k] = ag.observedTxsPool.NewAttestationState(&attestableObservedTx{ObservedTx: &tx, inbound: false})
		ag.mu.Unlock()

		ag.handleQuorumTxCommitted(&ebifrost.EventNotification{
			Payload: payload,
		})

		time.Sleep(50 * time.Millisecond)

		require.NotNil(t, callbackTx, "spent refs callback should fire for XMR outbound committed event")
		assert.Equal(t, 2, len(callbackTx.SpentOutputRefs))
		assert.Equal(t, "spent1", callbackTx.SpentOutputRefs[0].TxHash)
		assert.Equal(t, "spent2", callbackTx.SpentOutputRefs[1].TxHash)
	})

	t.Run("does not apply spent refs for non-XMR committed event", func(t *testing.T) {
		var called bool
		ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx, onOutcome func(applied bool)) {
			called = true
			if onOutcome != nil {
				onOutcome(true)
			}
		})

		tx := common.ObservedTx{
			Tx: common.Tx{
				ID:    "btc-committed-1",
				Chain: common.BTCChain,
			},
			SpentOutputRefs: []common.OutputRef{{TxHash: "x", OutputIndex: 0}},
		}

		qtx := common.QuorumTx{
			ObsTx:   tx,
			Inbound: false,
			Attestations: []*common.Attestation{
				{PubKey: ag.pubKey, Signature: []byte("sig")},
			},
		}

		payload, err := qtx.Marshal()
		require.NoError(t, err)

		k := txKey{
			Chain:     common.BTCChain,
			ID:        "btc-committed-1",
			Finalized: tx.IsFinal(),
		}
		ag.mu.Lock()
		ag.observedTxs[k] = ag.observedTxsPool.NewAttestationState(&attestableObservedTx{ObservedTx: &tx, inbound: false})
		ag.mu.Unlock()

		ag.handleQuorumTxCommitted(&ebifrost.EventNotification{
			Payload: payload,
		})

		time.Sleep(50 * time.Millisecond)
		assert.False(t, called, "spent refs callback should NOT fire for non-XMR chain")
	})

	t.Run("does not apply spent refs for inbound XMR committed event", func(t *testing.T) {
		var called bool
		ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx, onOutcome func(applied bool)) {
			called = true
			if onOutcome != nil {
				onOutcome(true)
			}
		})

		tx := common.ObservedTx{
			Tx: common.Tx{
				ID:    "xmr-inbound-1",
				Chain: common.XMRChain,
			},
			SpentOutputRefs: []common.OutputRef{{TxHash: "x", OutputIndex: 0, KeyImage: "ki_x", SpendTxHash: "xmr-inbound-1"}},
		}

		qtx := common.QuorumTx{
			ObsTx:   tx,
			Inbound: true,
			Attestations: []*common.Attestation{
				{PubKey: ag.pubKey, Signature: []byte("sig")},
			},
		}

		payload, err := qtx.Marshal()
		require.NoError(t, err)

		k := txKey{
			Chain:     common.XMRChain,
			ID:        "xmr-inbound-1",
			Inbound:   true,
			Finalized: tx.IsFinal(),
		}
		ag.mu.Lock()
		ag.observedTxs[k] = ag.observedTxsPool.NewAttestationState(&attestableObservedTx{ObservedTx: &tx, inbound: true})
		ag.mu.Unlock()

		ag.handleQuorumTxCommitted(&ebifrost.EventNotification{
			Payload: payload,
		})

		time.Sleep(50 * time.Millisecond)
		assert.False(t, called, "spent refs callback should NOT fire for inbound XMR")
	})

	t.Run("does not apply spent refs when no refs present", func(t *testing.T) {
		var called bool
		ag.SetObserverApplySpentOutputRefs(func(tx common.ObservedTx, onOutcome func(applied bool)) {
			called = true
			if onOutcome != nil {
				onOutcome(true)
			}
		})

		tx := common.ObservedTx{
			Tx: common.Tx{
				ID:    "xmr-norefs-1",
				Chain: common.XMRChain,
			},
		}

		qtx := common.QuorumTx{
			ObsTx:   tx,
			Inbound: false,
			Attestations: []*common.Attestation{
				{PubKey: ag.pubKey, Signature: []byte("sig")},
			},
		}

		payload, err := qtx.Marshal()
		require.NoError(t, err)

		k := txKey{
			Chain:     common.XMRChain,
			ID:        "xmr-norefs-1",
			Finalized: tx.IsFinal(),
		}
		ag.mu.Lock()
		ag.observedTxs[k] = ag.observedTxsPool.NewAttestationState(&attestableObservedTx{ObservedTx: &tx, inbound: false})
		ag.mu.Unlock()

		ag.handleQuorumTxCommitted(&ebifrost.EventNotification{
			Payload: payload,
		})

		time.Sleep(50 * time.Millisecond)
		assert.False(t, called, "spent refs callback should NOT fire when no refs present")
	})
}
