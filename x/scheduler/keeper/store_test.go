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

func (s *KeeperTestSuite) TestGetSchedulesBySender() {
	s.SetupTest()
	sdkCtx := sdk.UnwrapSDKContext(s.ctx)

	// Add msgs from two different senders at different heights
	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  100,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"a":"1"}`),
	}))
	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  200,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"a":"2"}`),
	}))
	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  100,
		Sender: accAddrs[1].String(),
		Msg:    []byte(`{"b":"1"}`),
	}))

	// Query sender 0 - should get 2 schedules with correct msgs
	res, err := s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[0].String()})
	s.Require().NoError(err)
	s.Require().NotNil(res.Pagination)
	s.Require().Len(res.Schedules, 2)
	s.Require().Equal(uint64(101), res.Schedules[0].Height)
	s.Require().Equal([]byte(`{"a":"1"}`), res.Schedules[0].Msgs[0].Msg)
	s.Require().Equal(accAddrs[0].String(), res.Schedules[0].Msgs[0].Sender)
	s.Require().Equal(uint64(201), res.Schedules[1].Height)
	s.Require().Equal([]byte(`{"a":"2"}`), res.Schedules[1].Msgs[0].Msg)
	s.Require().Equal(accAddrs[0].String(), res.Schedules[1].Msgs[0].Sender)

	// Query sender 1 - should get 1 schedule with correct msg
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[1].String()})
	s.Require().NoError(err)
	s.Require().NotNil(res.Pagination)
	s.Require().Len(res.Schedules, 1)
	s.Require().Equal(uint64(101), res.Schedules[0].Height)
	// msg 0 is from sender 0 because it is added first
	s.Require().Equal([]byte(`{"a":"1"}`), res.Schedules[0].Msgs[0].Msg)
	s.Require().Equal(accAddrs[0].String(), res.Schedules[0].Msgs[0].Sender)
	s.Require().Equal([]byte(`{"b":"1"}`), res.Schedules[0].Msgs[1].Msg)
	s.Require().Equal(accAddrs[1].String(), res.Schedules[0].Msgs[1].Sender)

	// Query unknown sender - should get 0
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: sdk.AccAddress("addr?_______________").String()})
	s.Require().NoError(err)
	s.Require().Empty(res.Schedules)

	// Query invalid sender - should error
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: "abc123"})
	s.Require().Error(err)
	s.Require().Nil(res)
}

func (s *KeeperTestSuite) TestRemoveScheduleCleansIndex() {
	s.SetupTest()
	sdkCtx := sdk.UnwrapSDKContext(s.ctx)

	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  100,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"a":"1"}`),
	}))
	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  200,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"a":"2"}`),
	}))

	// Verify both are indexed
	res, err := s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[0].String()})
	s.Require().NoError(err)
	s.Require().Len(res.Schedules, 2)

	// Remove only the first schedule
	s.Require().NoError(s.keeper.RemoveSchedule(sdkCtx, 101))

	// Only the second schedule should remain
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[0].String()})
	s.Require().NoError(err)
	s.Require().Len(res.Schedules, 1)
	s.Require().Equal(uint64(201), res.Schedules[0].Height)
	s.Require().Equal([]byte(`{"a":"2"}`), res.Schedules[0].Msgs[0].Msg)
}

func (s *KeeperTestSuite) TestSetScheduleUpdatesIndex() {
	s.SetupTest()
	sdkCtx := sdk.UnwrapSDKContext(s.ctx)

	// Add a msg via AddMsg (which maintains the index)
	s.Require().NoError(s.keeper.AddMsg(sdkCtx, types.MsgScheduleExecuteContract{
		After:  100,
		Sender: accAddrs[0].String(),
		Msg:    []byte(`{"a":"1"}`),
	}))

	// Verify sender 0 is indexed
	res, err := s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[0].String()})
	s.Require().NoError(err)
	s.Require().Len(res.Schedules, 1)

	// Overwrite the schedule via SetSchedule, replacing sender 0 with sender 1
	s.Require().NoError(s.keeper.SetSchedule(sdkCtx, types.Schedule{
		Height: 101,
		Msgs: []types.MsgScheduleExecuteContract{
			{Sender: accAddrs[1].String(), Msg: []byte(`{"b":"1"}`)},
		},
	}))

	// Sender 0 should no longer be indexed
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[0].String()})
	s.Require().NoError(err)
	s.Require().Empty(res.Schedules)

	// Sender 1 should now be indexed
	res, err = s.keeper.Schedules(s.ctx, &types.QuerySchedulesRequest{Sender: accAddrs[1].String()})
	s.Require().NoError(err)
	s.Require().Len(res.Schedules, 1)
	s.Require().Equal(uint64(101), res.Schedules[0].Height)
	s.Require().Equal([]byte(`{"b":"1"}`), res.Schedules[0].Msgs[0].Msg)
	s.Require().Equal(accAddrs[1].String(), res.Schedules[0].Msgs[0].Sender)
}
