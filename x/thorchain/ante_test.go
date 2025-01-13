package thorchain

import (
	"github.com/stretchr/testify/suite"

	. "gopkg.in/check.v1"

	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type AnteTestSuite struct {
	suite.Suite
}

var _ = Suite(&AnteTestSuite{})

func (s *AnteTestSuite) TestRejectMutlipleDepositMsgs(c *C) {
	_, k := setupKeeperForTest(c)

	ad := AnteDecorator{
		keeper: k,
	}

	// no deposit or send msgs is ok
	err := ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}})
	c.Assert(err, IsNil)

	// one deposit msg is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}, &types.MsgDeposit{}})
	c.Assert(err, IsNil)

	// one send msg is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}, &types.MsgSend{}})
	c.Assert(err, IsNil)

	// two deposit msgs is not ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}, &types.MsgDeposit{}, &types.MsgDeposit{}})
	c.Assert(err, NotNil)

	// one deposit and one send is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}, &types.MsgDeposit{}, &types.MsgSend{}})
	c.Assert(err, IsNil)

	// two bank sends is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&banktypes.MsgSend{}, &banktypes.MsgSend{}})
	c.Assert(err, IsNil)

	bankSendDeposit := banktypes.MsgSend{
		ToAddress: k.GetModuleAccAddress(ModuleName).String(),
	}
	// one bank send to module account is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&bankSendDeposit})
	c.Assert(err, IsNil)

	// two bank sends to module account is not ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&bankSendDeposit, &bankSendDeposit})
	c.Assert(err, NotNil)

	// one deposit and one send is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&types.MsgBan{}, &types.MsgBond{}, &types.MsgDeposit{}, &types.MsgSend{}})
	c.Assert(err, IsNil)

	// two bank sends is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&banktypes.MsgSend{}, &banktypes.MsgSend{}})
	c.Assert(err, IsNil)

	// one bank send to module account is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&bankSendDeposit})
	c.Assert(err, IsNil)

	// two bank sends to module account is not ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&bankSendDeposit, &bankSendDeposit})
	c.Assert(err, NotNil)

	// one normal bank send and one bank send to module account is ok
	err = ad.rejectMultipleDepositMsgs([]cosmos.Msg{&banktypes.MsgSend{}, &bankSendDeposit})
	c.Assert(err, IsNil)
}

func (s *AnteTestSuite) TestAnteHandleMessage(c *C) {
	ctx, k := setupKeeperForTest(c)
	version := GetCurrentVersion()

	ad := AnteDecorator{
		keeper: k,
	}

	fromAddr := GetRandomBech32Addr()
	toAddr := GetRandomBech32Addr()

	// fund an addr so it can pass the fee deduction ante
	funds, err := common.NewCoin(common.RuneNative, cosmos.NewUint(200*common.One)).Native()
	c.Assert(err, IsNil)
	err = k.AddCoins(ctx, fromAddr, cosmos.NewCoins(funds))
	c.Assert(err, IsNil)
	coin, err := common.NewCoin(common.RuneNative, cosmos.NewUint(1*common.One)).Native()
	c.Assert(err, IsNil)

	goodMsg := types.MsgSend{
		FromAddress: fromAddr,
		ToAddress:   toAddr,
		Amount:      cosmos.NewCoins(coin),
	}
	err = ad.anteHandleMessage(ctx, version, &goodMsg)
	c.Assert(err, IsNil)

	// bank sends are allowed
	bankSendMsg := banktypes.MsgSend{
		FromAddress: fromAddr.String(),
		ToAddress:   toAddr.String(),
	}
	err = ad.anteHandleMessage(ctx, version, &bankSendMsg)
	c.Assert(err, IsNil)

	// other non-thorchain msgs should be rejected
	badMsg := banktypes.MsgMultiSend{}
	err = ad.anteHandleMessage(ctx, version, &badMsg)
	c.Assert(err, NotNil)
}
