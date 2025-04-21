package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// TCYStakeHandler to process withdraw requests
type TCYStakeHandler struct {
	mgr Manager
}

// NewTCYStakeHandler create a new instance of TCYStakeHandler to process withdraw request
func NewTCYStakeHandler(mgr Manager) TCYStakeHandler {
	return TCYStakeHandler{
		mgr: mgr,
	}
}

// Run is the main entry point of withdraw
func (h TCYStakeHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgTCYStake)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info("receive MsgTCYStake", "address", msg.Tx.FromAddress, "amount", msg.Tx.Coins[0].Amount)
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgTCYStake failed validation", "error", err)
		return nil, err
	}

	result, err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process msg tcy stake", "error", err)
		return nil, err
	}
	return result, err
}

func (h TCYStakeHandler) validate(ctx cosmos.Context, msg MsgTCYStake) error {
	if err := msg.ValidateBasic(); err != nil {
		return errTCYStakeFailValidation
	}
	stakingHalt := h.mgr.Keeper().GetConfigInt64(ctx, constants.TCYStakingHalt)
	if stakingHalt > 0 {
		return fmt.Errorf("tcy staking is halted")
	}
	return nil
}

func (h TCYStakeHandler) handle(ctx cosmos.Context, msg MsgTCYStake) (*cosmos.Result, error) {
	ctx.Logger().Info("staking tcy claim", "address", msg.Tx.FromAddress.String(), "amount", msg.Tx.Coins[0].Amount.String())

	err := h.mgr.Keeper().UpdateTCYStaker(ctx, msg.Tx.FromAddress, msg.Tx.Coins[0].Amount)
	if err != nil {
		ctx.Logger().Error("failed to update tcy staker", "err", err)
	}

	evt := types.NewEventTCYStake(msg.Tx.FromAddress, msg.Tx.Coins[0].Amount)
	return &cosmos.Result{}, h.mgr.EventMgr().EmitEvent(ctx, evt)
}
