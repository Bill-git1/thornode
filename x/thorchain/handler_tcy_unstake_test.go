package thorchain

import (
	"cosmossdk.io/math"
	"gitlab.com/thorchain/thornode/v3/common"
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"

	. "gopkg.in/check.v1"
)

type HandlerTCYUnstake struct{}

var _ = Suite(&HandlerTCYUnstake{})

func (s *HandlerTCYUnstake) TestValidate(c *C) {
	ctx, k := setupKeeperForTest(c)

	// happy path
	k.SetMimir(ctx, "TCYUnstakingHalt", 0)
	fromAddr := GetRandomRUNEAddress()
	toAddr := GetRandomRUNEAddress()
	accSignerAddr, err := fromAddr.AccAddress()
	c.Assert(err, IsNil)
	bps := cosmos.NewUint(100_00)

	tx := common.NewTx(
		common.BlankTxID,
		fromAddr,
		toAddr,
		common.Coins{},
		common.Gas{},
		"",
	)

	msg := NewMsgTCYUnstake(tx, bps, accSignerAddr)
	handler := NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err, IsNil)

	// invalid msgs
	// empty signer
	msg = NewMsgTCYUnstake(tx, bps, cosmos.AccAddress{})
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)

	// empty bps
	msg = NewMsgTCYUnstake(tx, cosmos.ZeroUint(), cosmos.AccAddress{})
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)

	// bps more than 100%
	msg = NewMsgTCYUnstake(tx, cosmos.NewUint(200_00), cosmos.AccAddress{})
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)

	// tx from address not rune address
	tx = common.NewTx(
		common.BlankTxID,
		GetRandomBTCAddress(),
		toAddr,
		common.Coins{},
		common.Gas{},
		"",
	)
	msg = NewMsgTCYUnstake(tx, bps, accSignerAddr)
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)

	// happy path
	k.SetMimir(ctx, "TCYUnstakingHalt", 1)
	bps = cosmos.NewUint(100_00)

	tx = common.NewTx(
		common.BlankTxID,
		fromAddr,
		toAddr,
		common.Coins{},
		common.Gas{},
		"",
	)

	msg = NewMsgTCYUnstake(tx, bps, accSignerAddr)
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	err = handler.validate(ctx, *msg)
	c.Assert(err.Error(), Equals, "tcy unstaking is halt")

	// empty msg
	k.SetMimir(ctx, "TCYUnstakingHalt", 0)
	msg = &MsgTCYUnstake{}
	err = handler.validate(ctx, *msg)
	c.Assert(err, NotNil)
}

func (s *HandlerTCYStake) TestHandle(c *C) {
	ctx, k := setupKeeperForTest(c)

	addr1 := GetRandomRUNEAddress()
	accAddr1, err := addr1.AccAddress()
	c.Assert(err, IsNil)
	addr1Amount := cosmos.NewUint(100)
	addr2 := GetRandomRUNEAddress()
	accAddr2, err := addr2.AccAddress()
	c.Assert(err, IsNil)
	addr2Amount := cosmos.NewUint(200)

	tx := common.NewTx(
		common.BlankTxID,
		common.NoAddress,
		GetRandomRUNEAddress(),
		common.NewCoins(common.NewCoin(common.RuneNative, cosmos.NewUint(1))),
		common.Gas{},
		"",
	)

	// set stakers and staking module
	err = k.SetTCYStaker(ctx, TCYStaker{
		Address: addr1,
		Amount:  addr1Amount,
	})
	c.Assert(err, IsNil)
	err = k.SetTCYStaker(ctx, TCYStaker{
		Address: addr2,
		Amount:  addr2Amount,
	})
	c.Assert(err, IsNil)

	stakeAmount := addr1Amount.Add(addr2Amount)
	coin := common.NewCoin(common.TCY, stakeAmount)
	err = k.MintToModule(ctx, ModuleName, coin)
	c.Assert(err, IsNil)
	err = k.SendFromModuleToModule(ctx, ModuleName, TCYStakeName, common.NewCoins(coin))
	c.Assert(err, IsNil)
	tcyStakeAmount := k.GetBalanceOfModule(ctx, TCYStakeName, common.TCY.Native())
	c.Assert(tcyStakeAmount.Equal(stakeAmount), Equals, true)

	// check state before
	c.Assert(k.TCYStakerExists(ctx, addr1), Equals, true)
	c.Assert(k.TCYStakerExists(ctx, addr2), Equals, true)

	addr1Coin := k.GetBalanceOf(ctx, accAddr1, common.TCY)
	c.Assert(addr1Coin.IsZero(), Equals, true)
	addr2Coin := k.GetBalanceOf(ctx, accAddr2, common.TCY)
	c.Assert(addr2Coin.IsZero(), Equals, true)

	// unstake 100% from addr1
	tx.FromAddress = addr1
	bps := cosmos.NewUint(100_00)
	msg := NewMsgTCYUnstake(tx, bps, accAddr1)
	handler := NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)

	c.Assert(k.TCYStakerExists(ctx, addr1), Equals, false)
	c.Assert(k.TCYStakerExists(ctx, addr2), Equals, true)

	addr1Coin = k.GetBalanceOf(ctx, accAddr1, common.TCY)
	c.Assert(addr1Coin.Amount.Equal(math.NewInt(100)), Equals, true)
	addr2Coin = k.GetBalanceOf(ctx, accAddr2, common.TCY)
	c.Assert(addr2Coin.IsZero(), Equals, true)
	tcyStakeAmount = k.GetBalanceOfModule(ctx, TCYStakeName, common.TCY.Native())
	c.Assert(tcyStakeAmount.Equal(addr2Amount), Equals, true)

	// unstake 25% from addr2
	tx.FromAddress = addr2
	bps = cosmos.NewUint(25_00)
	msg = NewMsgTCYUnstake(tx, bps, accAddr2)
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)

	c.Assert(k.TCYStakerExists(ctx, addr1), Equals, false)
	c.Assert(k.TCYStakerExists(ctx, addr2), Equals, true)

	staker, err := k.GetTCYStaker(ctx, addr2)
	c.Assert(err, IsNil)
	c.Assert(staker.Address.Equals(addr2), Equals, true)
	c.Assert(staker.Amount.Equal(math.NewUint(150)), Equals, true)

	addr1Coin = k.GetBalanceOf(ctx, accAddr1, common.TCY)
	c.Assert(addr1Coin.Amount.Equal(math.NewInt(100)), Equals, true)
	addr2Coin = k.GetBalanceOf(ctx, accAddr2, common.TCY)
	c.Assert(addr2Coin.Amount.Equal(math.NewInt(50)), Equals, true)
	tcyStakeAmount = k.GetBalanceOfModule(ctx, TCYStakeName, common.TCY.Native())
	c.Assert(tcyStakeAmount.Equal(math.NewUint(150)), Equals, true)

	// unstake 100% from addr2
	tx.FromAddress = addr2
	bps = cosmos.NewUint(100_00)
	msg = NewMsgTCYUnstake(tx, bps, accAddr2)
	handler = NewTCYUnstakeHandler(NewDummyMgrWithKeeper(k))
	_, err = handler.handle(ctx, *msg)
	c.Assert(err, IsNil)

	c.Assert(k.TCYStakerExists(ctx, addr1), Equals, false)
	c.Assert(k.TCYStakerExists(ctx, addr2), Equals, false)

	addr1Coin = k.GetBalanceOf(ctx, accAddr1, common.TCY)
	c.Assert(addr1Coin.Amount.Equal(math.NewInt(100)), Equals, true)
	addr2Coin = k.GetBalanceOf(ctx, accAddr2, common.TCY)
	c.Assert(addr2Coin.Amount.Equal(math.NewInt(200)), Equals, true)
	tcyStakeAmount = k.GetBalanceOfModule(ctx, TCYStakeName, common.TCY.Native())
	c.Assert(tcyStakeAmount.IsZero(), Equals, true)
}
