package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	. "gopkg.in/check.v1"
)

type TradeManagerVCURSuite struct{}

var _ = Suite(&TradeManagerVCURSuite{})

func (s *TradeManagerVCURSuite) SetUpSuite(_ *C) {
	SetupConfigForTest()
}

func (s *TradeManagerVCURSuite) TestDepositAndWithdrawal(c *C) {
	ctx, k := setupKeeperForTest(c)
	mgr := newTradeMgrVCUR(k)

	asset := common.BTCAsset.GetTradeAsset()
	addr1 := GetRandomBech32Addr()
	addr2 := GetRandomBech32Addr()
	// addr3 := GetRandomBech32Addr()

	amt, err := mgr.Deposit(ctx, asset, cosmos.NewUint(100*common.One), addr1)
	c.Assert(err, IsNil)
	c.Check(amt.String(), Equals, cosmos.NewUint(100*common.One).String())

	bal := mgr.BalanceOf(ctx, asset, addr1)
	c.Check(bal.String(), Equals, cosmos.NewUint(100*common.One).String())

	amt, err = mgr.Deposit(ctx, asset, cosmos.NewUint(50*common.One), addr2)
	c.Assert(err, IsNil)
	c.Check(amt.String(), Equals, cosmos.NewUint(50*common.One).String())

	bal = mgr.BalanceOf(ctx, asset, addr2)
	c.Check(bal.String(), Equals, cosmos.NewUint(50*common.One).String())
	bal = mgr.BalanceOf(ctx, asset, addr1)
	c.Check(bal.String(), Equals, cosmos.NewUint(100*common.One).String())

	// withdrawal
	amt, err = mgr.Withdrawal(ctx, asset, cosmos.NewUint(30*common.One), addr2)
	c.Assert(err, IsNil)
	c.Check(amt.String(), Equals, cosmos.NewUint(30*common.One).String())
	bal = mgr.BalanceOf(ctx, asset, addr2)
	c.Check(bal.String(), Equals, cosmos.NewUint(20*common.One).String())
	amt, err = mgr.Withdrawal(ctx, asset, cosmos.NewUint(30*common.One), addr2)
	c.Assert(err, IsNil)
	c.Check(amt.String(), Equals, cosmos.NewUint(20*common.One).String())
	bal = mgr.BalanceOf(ctx, asset, addr2)
	c.Check(bal.String(), Equals, cosmos.NewUint(0).String())
}
