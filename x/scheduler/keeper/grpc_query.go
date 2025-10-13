package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"gitlab.com/thorchain/thornode/v3/x/scheduler/types"
)

var _ types.QueryServer = Keeper{}

// Schedule implements types.QueryServer.
func (k Keeper) Schedule(ctx context.Context, req *types.QueryScheduleRequest) (*types.QueryScheduleResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	schedule, err := k.GetSchedule(sdkCtx, req.Height)
	if err != nil {
		return nil, err
	}
	return &types.QueryScheduleResponse{
		Schedule: schedule,
	}, nil
}

// Schedules implements types.QueryServer.
func (k Keeper) Schedules(ctx context.Context, req *types.QuerySchedulesRequest) (*types.QuerySchedulesResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	schedules, pageRes, err := k.ListSchedules(sdkCtx, &req.Pagination)
	if err != nil {
		return nil, err
	}
	// Convert []types.Schedule to []*types.Schedule
	schedulePtrs := make([]*types.Schedule, len(schedules))
	for i := range schedules {
		schedulePtrs[i] = &schedules[i]
	}
	return &types.QuerySchedulesResponse{
		Schedules:  schedulePtrs,
		Pagination: *pageRes,
	}, nil
}
