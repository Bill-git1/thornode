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

func (HandlerSuite) TestSkimAffiliateFees(c *C) {
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
