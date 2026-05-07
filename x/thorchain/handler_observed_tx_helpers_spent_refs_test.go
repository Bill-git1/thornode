package thorchain

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
)

type SpentOutputRefsFlowSuite struct{}

var _ = Suite(&SpentOutputRefsFlowSuite{})

// Test 4: spent output refs accumulate in the bounded XMR store, keyed by the
// FROST pubkey used by XMR outbound observations.
func (s *SpentOutputRefsFlowSuite) TestSpentRefsAccumulateInKeeperStore(c *C) {
	ctx, mgr := setupManagerForTest(c)
	k := mgr.Keeper()

	ecdsaPubKey := GetRandomPubKey()
	eddsaPubKey := GetRandomPubKey()
	frostPubKey := GetRandomPubKey()

	vault := NewVaultV2(1, ActiveVault, AsgardVault, ecdsaPubKey, []string{common.XMRChain.String()}, nil, eddsaPubKey)
	vault.PubKeyFrost = frostPubKey
	c.Assert(k.SetVault(ctx, vault), IsNil)

	// Verify frost index was created — GetVault by frost key must resolve.
	resolved, err := k.GetVault(ctx, frostPubKey)
	c.Assert(err, IsNil)
	c.Assert(resolved.PubKey.Equals(ecdsaPubKey), Equals, true,
		Commentf("GetVault by frost key should resolve to ECDSA-keyed vault"))

	// Verify VaultExists works for all three key types.
	c.Assert(k.VaultExists(ctx, ecdsaPubKey), Equals, true)
	c.Assert(k.VaultExists(ctx, eddsaPubKey), Equals, true)
	c.Assert(k.VaultExists(ctx, frostPubKey), Equals, true)

	// Simulate an outbound observation keyed by the frost pubkey (as XMR does).
	refs := []common.OutputRef{
		{TxHash: "aaa", OutputIndex: 0, KeyImage: "ki_aaa", SpendTxHash: "spend_tx"},
		{TxHash: "bbb", OutputIndex: 1, KeyImage: "ki_bbb", SpendTxHash: "spend_tx"},
	}

	c.Assert(k.SetXMRSpentOutputRefs(ctx, frostPubKey, common.TxID("spend_tx"), refs, 10, 10), IsNil)

	// Verify refs were stored.
	updated, err := k.GetXMRSpentOutputRefs(ctx, frostPubKey)
	c.Assert(err, IsNil)
	c.Assert(len(updated), Equals, 2)

	// Apply again with a duplicate and a new ref — dedup should work.
	moreRefs := []common.OutputRef{
		{TxHash: "aaa", OutputIndex: 0, KeyImage: "ki_aaa", SpendTxHash: "spend_tx"},
		{TxHash: "ccc", OutputIndex: 2, KeyImage: "ki_ccc", SpendTxHash: "spend_tx"},
	}
	c.Assert(k.SetXMRSpentOutputRefs(ctx, frostPubKey, common.TxID("spend_tx"), moreRefs, 11, 11), IsNil)

	final, err := k.GetXMRSpentOutputRefs(ctx, frostPubKey)
	c.Assert(err, IsNil)
	c.Assert(len(final), Equals, 3, Commentf("duplicate 'aaa' should be deduped"))
}

// Test 5: spent output refs are read from the dedicated XMR store without
// inflating vault state.
func (s *SpentOutputRefsFlowSuite) TestSpentRefsCatchupFromKeeperStore(c *C) {
	ctx, mgr := setupManagerForTest(c)
	k := mgr.Keeper()

	ecdsaPubKey := GetRandomPubKey()
	eddsaPubKey := GetRandomPubKey()
	frostPubKey := GetRandomPubKey()

	vault := NewVaultV2(1, ActiveVault, AsgardVault, ecdsaPubKey, []string{common.XMRChain.String()}, nil, eddsaPubKey)
	vault.PubKeyFrost = frostPubKey
	refs := []common.OutputRef{
		{TxHash: "spent1", OutputIndex: 0, KeyImage: "ki_1", SpendTxHash: "spend_tx"},
		{TxHash: "spent2", OutputIndex: 1, KeyImage: "ki_2", SpendTxHash: "spend_tx"},
		{TxHash: "spent3", OutputIndex: 2, KeyImage: "ki_3", SpendTxHash: "spend_tx"},
	}
	c.Assert(k.SetVault(ctx, vault), IsNil)
	c.Assert(k.SetXMRSpentOutputRefs(ctx, frostPubKey, common.TxID("spend_tx"), refs, 10, 10), IsNil)

	got, err := k.GetXMRSpentOutputRefs(ctx, frostPubKey)
	c.Assert(err, IsNil)
	c.Assert(len(got), Equals, 3)

	// Verify individual refs match.
	want := make(map[string]uint32, len(refs))
	for _, ref := range refs {
		want[ref.TxHash] = ref.OutputIndex
	}
	for _, ref := range got {
		c.Assert(ref.OutputIndex, Equals, want[ref.TxHash])
	}
}

// Verify that the frost index is created when SetVault is called with a
// vault that has PubKeyFrost set. There is no lazy migration in GetVault to
// avoid non-deterministic state writes during read paths — the index is
// created the next time SetVault is called for the vault.
func (s *SpentOutputRefsFlowSuite) TestFrostIndexCreatedBySetVault(c *C) {
	ctx, mgr := setupManagerForTest(c)
	k := mgr.Keeper()

	ecdsaPubKey := GetRandomPubKey()
	eddsaPubKey := GetRandomPubKey()
	frostPubKey := GetRandomPubKey()

	// Create vault without frost key first (simulates pre-frost vault).
	vault := NewVaultV2(1, ActiveVault, AsgardVault, ecdsaPubKey, []string{common.XMRChain.String()}, nil, eddsaPubKey)
	c.Assert(k.SetVault(ctx, vault), IsNil)

	// Frost lookup should fail since no frost key was set.
	c.Assert(k.VaultExists(ctx, frostPubKey), Equals, false)

	// Now update the vault to add the frost key (simulates keygen completing).
	vault.PubKeyFrost = frostPubKey
	c.Assert(k.SetVault(ctx, vault), IsNil)

	// Frost lookup should now work via the index created by SetVault.
	c.Assert(k.VaultExists(ctx, frostPubKey), Equals, true)

	resolved, err := k.GetVault(ctx, frostPubKey)
	c.Assert(err, IsNil)
	c.Assert(resolved.PubKey.Equals(ecdsaPubKey), Equals, true)
}
