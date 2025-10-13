package keeper_test

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	"gitlab.com/thorchain/thornode/v3/x/scheduler/types"
)

func (s *KeeperTestSuite) TestStoreMsgs() {
	s.SetupTest()
	sdkCtx := sdk.UnwrapSDKContext(s.ctx)
	res, err := s.keeper.GetSchedule(sdkCtx, 1000)

	s.Require().NoError(err)
	s.Require().Equal(res.Height, uint64(0x3e8))
	s.Require().Empty(res.Msgs)

	err = s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  1000,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"do":"something"}`),
	})

	s.Require().NoError(err)

	res, err = s.keeper.GetSchedule(sdkCtx, 1001)

	s.Require().NoError(err)
	s.Require().Equal(res.Height, uint64(0x3e9))
	s.Require().Len(res.Msgs, 1)
	s.Require().Equal(accAddrs[0].String(), res.Msgs[0].Sender)
	s.Require().Equal([]byte(`{"do":"something"}`), res.Msgs[0].Msg)

	err = s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  1000,
		Sender: accAddrs[1].String(),
		Msg:    []byte(`{"do":"something else"}`),
	})
	s.Require().NoError(err)

	res, err = s.keeper.GetSchedule(sdkCtx, 1001)

	s.Require().NoError(err)
	s.Require().Equal(res.Height, uint64(0x3e9))
	s.Require().Len(res.Msgs, 2)
	s.Require().Equal(accAddrs[0].String(), res.Msgs[0].Sender)
	s.Require().Equal([]byte(`{"do":"something"}`), res.Msgs[0].Msg)
	s.Require().Equal(accAddrs[1].String(), res.Msgs[1].Sender)
	s.Require().Equal([]byte(`{"do":"something else"}`), res.Msgs[1].Msg)
}
