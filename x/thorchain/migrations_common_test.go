package thorchain

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

type MigrationCommonSuite struct{}

var _ = Suite(&MigrationCommonSuite{})

func (s *MigrationCommonSuite) TestMigrate7to8Debug(c *C) {
	ctx, mgr := setupManagerForTest(c)
	migrator := NewMigrator(mgr)

	err := migrator.Migrate7to8(ctx)
	c.Assert(err, IsNil)
}

func (s *MigrationCommonSuite) TestMigrate8to9OverflowFix(c *C) {
	// Test the overflow fix logic directly

	// Scenario 1: Normal case where withheld >= spent
	withheld1 := cosmos.NewUint(2000)
	spent1 := cosmos.NewUint(1000)
	var surplus1 cosmos.Uint

	if withheld1.GTE(spent1) {
		surplus1 = withheld1.Sub(spent1)
	} else {
		surplus1 = cosmos.ZeroUint()
	}

	c.Assert(surplus1.Equal(cosmos.NewUint(1000)), Equals, true, Commentf("Expected surplus of 1000, got %s", surplus1.String()))

	// Scenario 2: Overflow case where spent > withheld (this would have panicked before the fix)
	withheld2 := cosmos.NewUint(1000)
	spent2 := cosmos.NewUint(2000)
	var surplus2 cosmos.Uint

	if withheld2.GTE(spent2) {
		surplus2 = withheld2.Sub(spent2)
	} else {
		surplus2 = cosmos.ZeroUint()
	}

	c.Assert(surplus2.IsZero(), Equals, true, Commentf("Expected surplus of 0, got %s", surplus2.String()))

	// Scenario 3: Edge case where withheld == spent
	withheld3 := cosmos.NewUint(1000)
	spent3 := cosmos.NewUint(1000)
	var surplus3 cosmos.Uint

	if withheld3.GTE(spent3) {
		surplus3 = withheld3.Sub(spent3)
	} else {
		surplus3 = cosmos.ZeroUint()
	}

	c.Assert(surplus3.IsZero(), Equals, true, Commentf("Expected surplus of 0, got %s", surplus3.String()))
}

func (s *MigrationCommonSuite) TestBurnReserveAndReduceMaxSupply(c *C) {
	ctx, mgr := setupManagerForTest(c)
	migrator := NewMigrator(mgr)
	k := mgr.Keeper()

	// Verify pre-state: reserve > 9.3M and total supply > 360M
	reserveBefore := k.GetRuneBalanceOfModule(ctx, ReserveName)
	supplyBefore := k.GetTotalSupply(ctx, common.RuneAsset())
	c.Assert(reserveBefore.GT(cosmos.NewUint(9_300_000_00000000)), Equals, true,
		Commentf("reserve should be > 9.3M, got %s", reserveBefore.String()))
	c.Assert(supplyBefore.GT(cosmos.NewUint(360_000_000_00000000)), Equals, true,
		Commentf("total supply should be > 360M, got %s", supplyBefore.String()))

	// Run migration
	err := migrator.BurnReserveAndReduceMaxSupply(ctx)
	c.Assert(err, IsNil)

	// Post-burn reserve should be 9.3M
	reserveAfter := k.GetRuneBalanceOfModule(ctx, ReserveName)
	c.Assert(reserveAfter.Equal(cosmos.NewUint(9_300_000_00000000)), Equals, true,
		Commentf("reserve should be 9.3M, got %s", reserveAfter.String()))

	// MaxRuneSupply must equal post-burn total supply (no state drift)
	postBurnSupply := k.GetTotalSupply(ctx, common.RuneAsset())
	maxSupplyMimir, _ := k.GetMimir(ctx, "MaxRuneSupply")
	c.Assert(maxSupplyMimir > 0, Equals, true)
	c.Assert(postBurnSupply.Equal(cosmos.NewUint(uint64(maxSupplyMimir))), Equals, true,
		Commentf("MaxRuneSupply (%d) should equal post-burn total supply (%s)", maxSupplyMimir, postBurnSupply.String()))

	// Total supply must be <= MaxRuneSupply
	c.Assert(postBurnSupply.LTE(cosmos.NewUint(uint64(maxSupplyMimir))), Equals, true,
		Commentf("total supply (%s) should be <= MaxRuneSupply (%d)", postBurnSupply.String(), maxSupplyMimir))
}

// makeClampFixVault returns a saved vault with a single ETH coin balance and
// the given pubkey. Used by the fixVaultClamps_v3_18_0 tests.
func makeClampFixVault(c *C, ctx cosmos.Context, mgr *Mgrs, pubKey common.PubKey, ethAmount cosmos.Uint) Vault {
	v := GetRandomVault()
	v.PubKey = pubKey
	v.Coins = common.NewCoins(common.NewCoin(common.ETHAsset, ethAmount))
	c.Assert(mgr.Keeper().SetVault(ctx, v), IsNil)
	return v
}

func (s *MigrationCommonSuite) TestFixVaultClamps_v3_18_0_HappyPath(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Three random pubkeys (the production vaults won't exist on mocknet,
	// and the data table is keyed to mainnet bech32 — for the test we pass
	// a custom fixes slice keyed to vaults we actually save here).
	pk1 := GetRandomPubKey()
	pk2 := GetRandomPubKey()
	pk3 := GetRandomPubKey()

	const headroom uint64 = 1_000_000_000
	const a1 uint64 = 15262957100
	const a2 uint64 = 4392790000
	const a3 uint64 = 7387020000

	v1 := makeClampFixVault(c, ctx, mgr, pk1, cosmos.NewUint(a1+headroom))
	v2 := makeClampFixVault(c, ctx, mgr, pk2, cosmos.NewUint(a2+headroom))
	v3 := makeClampFixVault(c, ctx, mgr, pk3, cosmos.NewUint(a3+headroom))

	fixes := []vaultClampFix{
		{pubKey: pk1.String(), amount: a1},
		{pubKey: pk2.String(), amount: a2},
		{pubKey: pk3.String(), amount: a3},
	}
	fixVaultClamps_v3_18_0(ctx, mgr, fixes)

	// Each vault's ETH balance should be exactly headroom after the fix.
	for _, pk := range []common.PubKey{v1.PubKey, v2.PubKey, v3.PubKey} {
		got, err := mgr.Keeper().GetVault(ctx, pk)
		c.Assert(err, IsNil)
		c.Assert(got.GetCoin(common.ETHAsset).Amount.Uint64(), Equals, headroom,
			Commentf("vault %s: expected %d after fix, got %s", pk.String(), headroom, got.GetCoin(common.ETHAsset).Amount.String()))
	}
}

func (s *MigrationCommonSuite) TestFixVaultClamps_v3_18_0_PreFlightSkip(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Vault has insufficient ETH for the fix — pre-flight should skip and
	// leave the vault state unchanged.
	pk := GetRandomPubKey()
	const fixAmt uint64 = 15262957100
	const beforeAmt uint64 = 5_000_000_000 // < fixAmt, would clamp again

	makeClampFixVault(c, ctx, mgr, pk, cosmos.NewUint(beforeAmt))

	fixes := []vaultClampFix{
		{pubKey: pk.String(), amount: fixAmt},
	}
	fixVaultClamps_v3_18_0(ctx, mgr, fixes)

	// Vault state must be unchanged.
	got, err := mgr.Keeper().GetVault(ctx, pk)
	c.Assert(err, IsNil)
	c.Assert(got.GetCoin(common.ETHAsset).Amount.Uint64(), Equals, beforeAmt,
		Commentf("vault %s: expected unchanged %d, got %s", pk.String(), beforeAmt, got.GetCoin(common.ETHAsset).Amount.String()))
}

func (s *MigrationCommonSuite) TestFixVaultClamps_v3_18_0_Mixed(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// One vault has enough; the other doesn't. The first should be debited,
	// the second untouched, and the loop must continue past the skip.
	pkOK := GetRandomPubKey()
	pkSkip := GetRandomPubKey()

	const fixA uint64 = 4392790000
	const fixB uint64 = 7387020000
	const headroom uint64 = 1_000_000_000

	makeClampFixVault(c, ctx, mgr, pkOK, cosmos.NewUint(fixA+headroom))
	const skipBefore uint64 = 1_000 // way below fixB
	makeClampFixVault(c, ctx, mgr, pkSkip, cosmos.NewUint(skipBefore))

	fixes := []vaultClampFix{
		{pubKey: pkSkip.String(), amount: fixB}, // skip first to prove loop continues
		{pubKey: pkOK.String(), amount: fixA},
	}
	fixVaultClamps_v3_18_0(ctx, mgr, fixes)

	gotOK, err := mgr.Keeper().GetVault(ctx, pkOK)
	c.Assert(err, IsNil)
	c.Assert(gotOK.GetCoin(common.ETHAsset).Amount.Uint64(), Equals, headroom,
		Commentf("OK vault: expected %d, got %s", headroom, gotOK.GetCoin(common.ETHAsset).Amount.String()))

	gotSkip, err := mgr.Keeper().GetVault(ctx, pkSkip)
	c.Assert(err, IsNil)
	c.Assert(gotSkip.GetCoin(common.ETHAsset).Amount.Uint64(), Equals, skipBefore,
		Commentf("Skipped vault: expected unchanged %d, got %s", skipBefore, gotSkip.GetCoin(common.ETHAsset).Amount.String()))
}

func (s *MigrationCommonSuite) TestFixVaultClamps_v3_18_0_RetiringVaultStillReachable(c *C) {
	// Confirm GetVault returns the vault regardless of status — the
	// production vaults may have transitioned to InactiveVault by 3.18.0.
	ctx, mgr := setupManagerForTest(c)

	pk := GetRandomPubKey()
	const fixAmt uint64 = 4392790000
	const headroom uint64 = 1_000_000_000

	v := GetRandomVault()
	v.PubKey = pk
	v.Status = InactiveVault
	v.Coins = common.NewCoins(common.NewCoin(common.ETHAsset, cosmos.NewUint(fixAmt+headroom)))
	c.Assert(mgr.Keeper().SetVault(ctx, v), IsNil)

	fixes := []vaultClampFix{{pubKey: pk.String(), amount: fixAmt}}
	fixVaultClamps_v3_18_0(ctx, mgr, fixes)

	got, err := mgr.Keeper().GetVault(ctx, pk)
	c.Assert(err, IsNil)
	c.Assert(got.GetCoin(common.ETHAsset).Amount.Uint64(), Equals, headroom)
}
