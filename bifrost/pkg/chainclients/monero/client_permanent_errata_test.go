package monero

import (
	"errors"
	"testing"

	"gitlab.com/thorchain/thornode/v3/common"
)

func TestClientPermanentErrataQuarantinesSignedArtifactThenScannerTxID(t *testing.T) {
	const txID = "abababababababababababababababababababababababababababababababab"
	store := newStandaloneSignedStore(t)
	plan := standaloneClaimPlan("logical-permanent-errata")
	if err := store.Reserve(plan); err != nil {
		t.Fatal(err)
	}
	if err := store.Set(plan, txID, standalonePayload(txID)); err != nil {
		t.Fatal(err)
	}

	errata := common.ErrataTx{Chain: common.XMRChain, Id: common.TxID(txID)}
	client := &Client{signedTxPayloadStore: store}
	if err := client.QuarantinePermanentErrata(errata, 77); err == nil {
		t.Fatal("missing scanner quarantine did not fail closed")
	}
	if _, found, lookupErr := store.GetOwned(plan.CacheVault, plan.CacheHash, plan.LogicalHash); !errors.Is(lookupErr, errXMRSigningClaimQuarantined) || found {
		t.Fatalf("first phase left signed transaction broadcastable: found=%t err=%v", found, lookupErr)
	}

	reorgStore := NewReorgStore(store.db)
	client.scanner = &Scanner{reorgStore: reorgStore}
	if err := client.QuarantinePermanentErrata(errata, 77); err != nil {
		t.Fatalf("retry permanent errata quarantine: %v", err)
	}
	reverted, err := reorgStore.IsRevertedTx(txID)
	if err != nil || !reverted {
		t.Fatalf("scanner transaction was not permanently quarantined: reverted=%t err=%v", reverted, err)
	}

	// Both phases are durable and idempotent, which is required for Observer's
	// unconditional startup replay of old and partially applied tombstones.
	restartedSignedStore := newSignedTxPayloadStore(store.db)
	restartedReorgStore := NewReorgStore(store.db)
	restarted := &Client{
		signedTxPayloadStore: restartedSignedStore,
		scanner:              &Scanner{reorgStore: restartedReorgStore},
	}
	if err = restarted.QuarantinePermanentErrata(errata, 77); err != nil {
		t.Fatalf("restart replay permanent errata quarantine: %v", err)
	}
	if _, found, lookupErr := restartedSignedStore.GetOwned(plan.CacheVault, plan.CacheHash, plan.LogicalHash); !errors.Is(lookupErr, errXMRSigningClaimQuarantined) || found {
		t.Fatalf("restart reopened signed transaction: found=%t err=%v", found, lookupErr)
	}
	reverted, err = restartedReorgStore.IsRevertedTx(txID)
	if err != nil || !reverted {
		t.Fatalf("restart reopened scanner transaction: reverted=%t err=%v", reverted, err)
	}
}
