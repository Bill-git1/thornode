package thorchain

import (
	"fmt"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type HelperAffiliateSuite struct{}

var _ = Suite(&HelperAffiliateSuite{})

func (HelperAffiliateSuite) TestSkimAffiliateFees(c *C) {
	ctx, mgr := setupManagerForTest(c)
	affAddr1 := GetRandomTHORAddress()
	affAddr2 := GetRandomTHORAddress()
	tx := GetRandomTx()
	signer, _ := GetRandomTHORAddress().AccAddress()

	// Check affiliate balances before skimming fee
	affAcctAddr1, err := affAddr1.AccAddress()
	c.Assert(err, IsNil)
	acct := mgr.Keeper().GetBalance(ctx, affAcctAddr1)
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "0")

	affAcctAddr2, err := affAddr2.AccAddress()
	c.Assert(err, IsNil)
	acct2 := mgr.Keeper().GetBalance(ctx, affAcctAddr2)
	c.Assert(acct2.AmountOf(common.RuneNative.Native()).String(), Equals, "0")

	memo := fmt.Sprintf("=:THOR.RUNE:%s::%s/%s:100/50", GetRandomTHORAddress(), affAddr1, affAddr2)
	coin := common.NewCoin(common.RuneNative, cosmos.NewUint(10*common.One))

	feeSkimmed, err := skimAffiliateFees(ctx, mgr, tx, signer, coin, memo)
	c.Assert(err, IsNil)
	c.Assert(feeSkimmed.String(), Equals, "15000000") // 150 basis points of 10 RUNE

	// Check affiliate balances after skimming fee
	acct = mgr.Keeper().GetBalance(ctx, affAcctAddr1)
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "10000000")
	acct2 = mgr.Keeper().GetBalance(ctx, affAcctAddr2)
	c.Assert(acct2.AmountOf(common.RuneNative.Native()).String(), Equals, "5000000")

	// Use one thorname and one rune address
	tn := types.THORName{Name: "t", Owner: affAcctAddr1, ExpireBlockHeight: 10000000, Aliases: []types.THORNameAlias{{Chain: common.THORChain, Address: affAddr1}}}
	mgr.Keeper().SetTHORName(ctx, tn)
	memo = fmt.Sprintf("=:THOR.RUNE:%s::t/%s:100/50", GetRandomTHORAddress(), affAddr2)

	feeSkimmed, err = skimAffiliateFees(ctx, mgr, tx, signer, coin, memo)
	c.Assert(err, IsNil)
	c.Assert(feeSkimmed.String(), Equals, "15000000")

	// Check affiliate balances after skimming fee
	acct = mgr.Keeper().GetBalance(ctx, affAcctAddr1)
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "20000000")
	acct2 = mgr.Keeper().GetBalance(ctx, affAcctAddr2)
	c.Assert(acct2.AmountOf(common.RuneNative.Native()).String(), Equals, "10000000")

	// Set a preferred asset, make sure affiliate collector is updated
	tn.PreferredAsset = common.BTCAsset
	mgr.Keeper().SetTHORName(ctx, tn)
	tn, err = mgr.Keeper().GetTHORName(ctx, "t")
	c.Assert(err, IsNil)
	c.Assert(tn.PreferredAsset.String(), Equals, "BTC.BTC")
	c.Assert(mgr.Keeper().THORNameExists(ctx, "t"), Equals, true)
	// Must have BTC alias
	c.Assert(tn.CanReceiveAffiliateFee(), Equals, false)
	tn.Aliases = append(tn.Aliases, types.THORNameAlias{Chain: common.BTCChain, Address: GetRandomBTCAddress()})
	mgr.Keeper().SetTHORName(ctx, tn)
	c.Assert(mgr.Keeper().THORNameExists(ctx, "t"), Equals, true)
	c.Assert(tn.CanReceiveAffiliateFee(), Equals, true)

	feeSkimmed, err = skimAffiliateFees(ctx, mgr, tx, signer, coin, memo)
	c.Assert(err, IsNil)
	c.Assert(feeSkimmed.String(), Equals, "15000000")

	// Check affiliate balances after skimming fee, affAcctAddr1's balance should be same
	// as before + affiliate collector module updated
	acct = mgr.Keeper().GetBalance(ctx, affAcctAddr1)
	c.Assert(acct.AmountOf(common.RuneNative.Native()).String(), Equals, "20000000")

	// ac, err := mgr.Keeper().GetAffiliateCollector(ctx, affAcctAddr1)
	// c.Assert(err, IsNil)
	// c.Assert(ac.RuneAmount.String(), Equals, "10000000")

	// affAcctAddr2's balance should be updated as normal
	acct2 = mgr.Keeper().GetBalance(ctx, affAcctAddr2)
	c.Assert(acct2.AmountOf(common.RuneNative.Native()).String(), Equals, "15000000")
}

// fixes: https://gitlab.com/thorchain/thornode/-/issues/2238
func (HelperAffiliateSuite) TestEnsureAffiliateFromAddressSecuredAsset(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// External address should be rewritten for secured assets
	externalAddr := common.Address("0xe140683571184092a252116918abd7c6f18451a1")
	securedAsset, err := common.NewAsset("ETH-BTC")
	c.Assert(err, IsNil)
	c.Assert(securedAsset.IsSecuredAsset(), Equals, true)

	tx := common.NewTx(
		GetRandomTxHash(),
		externalAddr,
		GetRandomTHORAddress(),
		common.NewCoins(common.NewCoin(securedAsset, cosmos.NewUint(100*common.One))),
		common.Gas{},
		"",
	)

	var asgard common.Address
	asgard, err = mgr.Keeper().GetModuleAddress(AsgardName)
	c.Assert(err, IsNil)

	c.Assert(ensureAffiliateFromAddress(ctx, mgr, &tx), IsNil)
	c.Assert(tx.FromAddress.String(), Equals, asgard.String())
}

func (HelperAffiliateSuite) TestEnsureAffiliateFromAddressTradeAsset(c *C) {
	ctx, mgr := setupManagerForTest(c)

	externalAddr := common.Address("0xe140683571184092a252116918abd7c6f18451a1")
	tradeAsset, err := common.NewAsset("ETH~BTC")
	c.Assert(err, IsNil)
	c.Assert(tradeAsset.IsTradeAsset(), Equals, true)

	tx := common.NewTx(
		GetRandomTxHash(),
		externalAddr,
		GetRandomTHORAddress(),
		common.NewCoins(common.NewCoin(tradeAsset, cosmos.NewUint(100*common.One))),
		common.Gas{},
		"",
	)

	var asgard common.Address
	asgard, err = mgr.Keeper().GetModuleAddress(AsgardName)
	c.Assert(err, IsNil)

	c.Assert(ensureAffiliateFromAddress(ctx, mgr, &tx), IsNil)
	c.Assert(tx.FromAddress.String(), Equals, asgard.String())
}

func (HelperAffiliateSuite) TestEnsureAffiliateFromAddressPreservesTHORChainAddress(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Get Asgard address (a valid THORChain address)
	asgardAddr, err := mgr.Keeper().GetModuleAddress(AsgardName)
	c.Assert(err, IsNil)

	securedAsset, err := common.NewAsset("ETH-BTC")
	c.Assert(err, IsNil)
	c.Assert(securedAsset.IsSecuredAsset(), Equals, true)

	// Create a transaction with a THORChain address as FromAddress
	tx := common.NewTx(
		GetRandomTxHash(),
		asgardAddr, // Already a valid THORChain address
		GetRandomTHORAddress(),
		common.NewCoins(common.NewCoin(securedAsset, cosmos.NewUint(100*common.One))),
		common.Gas{},
		"",
	)

	originalAddr := tx.FromAddress.String()

	// Call ensureAffiliateFromAddress - it should NOT change the address
	c.Assert(ensureAffiliateFromAddress(ctx, mgr, &tx), IsNil)

	// Verify the address was preserved (not changed)
	c.Assert(tx.FromAddress.String(), Equals, originalAddr)
	c.Assert(tx.FromAddress.String(), Equals, asgardAddr.String())
}

func (HelperAffiliateSuite) TestEnsureAffiliateFromAddressNoOpForRegularAssets(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// External address with a regular (non-secured, non-trade) asset
	externalAddr := common.Address("0xe140683571184092a252116918abd7c6f18451a1")
	regularAsset := common.ETHAsset // Regular layer 1 asset

	tx := common.NewTx(
		GetRandomTxHash(),
		externalAddr,
		GetRandomTHORAddress(),
		common.NewCoins(common.NewCoin(regularAsset, cosmos.NewUint(100*common.One))),
		common.Gas{},
		"",
	)

	originalAddr := tx.FromAddress.String()

	// Call ensureAffiliateFromAddress - should be a no-op for regular assets
	c.Assert(ensureAffiliateFromAddress(ctx, mgr, &tx), IsNil)

	// Verify the address was NOT changed (even though it's an external address)
	c.Assert(tx.FromAddress.String(), Equals, originalAddr)
	c.Assert(tx.FromAddress.String(), Equals, externalAddr.String())
}

func (HelperAffiliateSuite) TestGetEffectiveMultiplier(c *C) {
	ctx, mgr := setupManagerForTest(c)

	// Global default is 100 (from constants_mocknet.go)
	tn := types.THORName{Name: "test"}

	// THORName multiplier 0 (unset) → use global default (100)
	tn.PreferredAssetOutboundFeeMultiplier = 0
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(100))

	// THORName multiplier >= 1 → use custom
	tn.PreferredAssetOutboundFeeMultiplier = 500
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(500))

	// THORName multiplier exactly 1 → use custom (boundary)
	tn.PreferredAssetOutboundFeeMultiplier = 1
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(1))

	// THORName multiplier 10000 → use custom (upper boundary)
	tn.PreferredAssetOutboundFeeMultiplier = 10000
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(10000))

	// Negative mimir falls back to constant default (100) via GetConfigInt64
	mgr.Keeper().SetMimir(ctx, "PreferredAssetOutboundFeeMultiplier", -5)
	tn.PreferredAssetOutboundFeeMultiplier = 0 // fall through to global
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(100))

	// Zero mimir → GetConfigInt64 returns 0 → clamp to 1
	mgr.Keeper().SetMimir(ctx, "PreferredAssetOutboundFeeMultiplier", 0)
	tn.PreferredAssetOutboundFeeMultiplier = 0
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(1))

	// Custom multiplier overrides even when global config is zero
	mgr.Keeper().SetMimir(ctx, "PreferredAssetOutboundFeeMultiplier", 0)
	tn.PreferredAssetOutboundFeeMultiplier = 200
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(200))

	// Mimir override with positive value
	mgr.Keeper().SetMimir(ctx, "PreferredAssetOutboundFeeMultiplier", 50)
	tn.PreferredAssetOutboundFeeMultiplier = 0
	c.Check(getEffectiveMultiplier(ctx, mgr, tn), Equals, int64(50))
}
