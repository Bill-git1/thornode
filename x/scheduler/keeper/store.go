package keeper

import (
	"fmt"

	"cosmossdk.io/collections"

	"cosmossdk.io/collections/codec"

	sdkcodec "github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	query "github.com/cosmos/cosmos-sdk/types/query"
	"gitlab.com/thorchain/thornode/v3/x/scheduler/types"
)

func (k Keeper) Store() collections.Map[uint64, types.Schedule] {
	return collections.NewMap(
		collections.NewSchemaBuilder(k.storeService),
		types.SchedulePrefix, types.ScheduleKey,
		codec.NewUint64Key[uint64](),
		sdkcodec.CollValue[types.Schedule](k.cdc),
	)
}

func (k Keeper) AddMsg(ctx sdk.Context, msg types.MsgScheduleExecuteContract) error {
	height := uint64(ctx.BlockHeight()) + msg.After + 1

	existing, err := k.GetSchedule(ctx, height)
	if err != nil {
		return err
	}
	existing.Msgs = append(existing.Msgs, msg)

	ctx.EventManager().EmitEvents(sdk.Events{
		sdk.NewEvent(
			types.EventScheduleMsg,
			sdk.NewAttribute(types.AttributeSender, msg.Sender),
			sdk.NewAttribute(types.AttributeAfter, fmt.Sprint(msg.After)),
			sdk.NewAttribute(types.AttributeHeight, fmt.Sprint(height)),
			sdk.NewAttribute(types.AttributeMsg, string(msg.Msg)),
		),
	})

	return k.SetSchedule(ctx, *existing)
}

func (k Keeper) GetSchedule(ctx sdk.Context, height uint64) (*types.Schedule, error) {
	has, err := k.Store().Has(ctx, height)
	if err != nil {
		return nil, err
	}
	if !has {
		return &types.Schedule{
			Height: height,
		}, nil
	}

	existing, err := k.Store().Get(ctx, height)
	return &existing, err
}

func (k Keeper) SetSchedule(ctx sdk.Context, schedule types.Schedule) error {
	return k.Store().Set(ctx, schedule.Height, schedule)
}

func (k Keeper) RemoveSchedule(ctx sdk.Context, height uint64) error {
	return k.Store().Remove(ctx, height)
}

func (k Keeper) ListSchedules(ctx sdk.Context, pagerequest *query.PageRequest) ([]types.Schedule, *query.PageResponse, error) {
	return query.CollectionPaginate(ctx, k.Store(), pagerequest, func(key uint64, value types.Schedule) (types.Schedule, error) {
		return value, nil
	})
}
