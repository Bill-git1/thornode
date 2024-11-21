package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common"
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"

	. "gopkg.in/check.v1"
)

type HandlerSendSuiteV87 struct{}

var _ = Suite(&HandlerSendSuiteV87{})

func (s *HandlerSendSuiteV87) TestValidate(c *C) {
	ctx, k := setupKeeperForTest(c)

	addr1 := GetRandomBech32Addr()
	addr2 := GetRandomBech32Addr()

	msg := &MsgSend{
		FromAddress: addr1,
		ToAddress:   addr2,
		Amount:      cosmos.NewCoins(cosmos.NewCoin("dummy", cosmos.NewInt(12))),
	}
	handler := NewSendHandler(NewDummyMgrWithKeeper(k))
	err := handler.validate(ctx, msg)
	c.Assert(err, IsNil)

	msg.ToAddress = k.GetModuleAccAddress(ModuleName)
	err = handler.validate(ctx, msg)
	c.Assert(err, IsNil, Commentf("sending to module address: %s indicates a memo will be attached for MsgDeposit", ModuleName))

	for _, moduleName := range []string{AsgardName, BondName, ReserveName} {
		msg.ToAddress = k.GetModuleAccAddress(moduleName)
		err := handler.validate(ctx, msg)
		c.Assert(err, NotNil, Commentf("cannot send to module: %s", moduleName))
	}

	// invalid msg
	msg = &MsgSend{}
	err = handler.validate(ctx, msg)
	c.Assert(err, NotNil)
}

func (s *HandlerSendSuiteV87) TestHandle(c *C) {
	ctx, k := setupKeeperForTest(c)

	addr1 := GetRandomBech32Addr()
	addr2 := GetRandomBech32Addr()

	funds, err := common.NewCoin(common.RuneNative, cosmos.NewUint(200*common.One)).Native()
	c.Assert(err, IsNil)
	err = k.AddCoins(ctx, addr1, cosmos.NewCoins(funds))
	c.Assert(err, IsNil)

	coin, err := common.NewCoin(common.RuneNative, cosmos.NewUint(12*common.One)).Native()
	c.Assert(err, IsNil)
	msg := &MsgSend{
		FromAddress: addr1,
		ToAddress:   addr2,
		Amount:      cosmos.NewCoins(coin),
	}

	handler := NewSendHandler(NewDummyMgrWithKeeper(k))
	_, err = handler.handle(ctx, msg)
	c.Assert(err, IsNil)

	// invalid msg should result in a error
	result, err := handler.Run(ctx, NewMsgNetworkFee(ctx.BlockHeight(), common.ETHChain, 1, 10000, GetRandomBech32Addr()))
	c.Assert(err, NotNil)
	c.Assert(result, IsNil)
	// insufficient funds
	coin, err = common.NewCoin(common.RuneNative, cosmos.NewUint(3000*common.One)).Native()
	c.Assert(err, IsNil)
	msg = &MsgSend{
		FromAddress: addr1,
		ToAddress:   addr2,
		Amount:      cosmos.NewCoins(coin),
	}
	_, err = handler.handle(ctx, msg)
	c.Assert(err, NotNil)
}
