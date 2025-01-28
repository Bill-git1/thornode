package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

func (h UnBondHandler) validateV3_0_0(ctx cosmos.Context, msg MsgUnBond) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	na, err := h.mgr.Keeper().GetNodeAccount(ctx, msg.NodeAddress)
	if err != nil {
		return ErrInternal(err, fmt.Sprintf("fail to get node account(%s)", msg.NodeAddress))
	}

	if na.Status == NodeActive || na.Status == NodeReady {
		return cosmos.ErrUnknownRequest("cannot unbond while node is in active or ready status")
	}

	if h.mgr.Keeper().GetConfigInt64(ctx, constants.PauseUnbond) > 0 {
		return ErrInternal(err, "unbonding has been paused")
	}

	jail, err := h.mgr.Keeper().GetNodeAccountJail(ctx, msg.NodeAddress)
	if err != nil {
		// ignore this error and carry on. Don't want a jail bug causing node
		// accounts to not be able to get their funds out
		ctx.Logger().Error("fail to get node account jail", "error", err)
	}
	if jail.IsJailed(ctx) {
		return fmt.Errorf("failed to unbond due to jail status: (release height %d) %s", jail.ReleaseHeight, jail.Reason)
	}

	bp, err := h.mgr.Keeper().GetBondProviders(ctx, msg.NodeAddress)
	if err != nil {
		return ErrInternal(err, fmt.Sprintf("fail to get bond providers(%s)", msg.NodeAddress))
	}
	from, err := msg.BondAddress.AccAddress()
	if err != nil {
		return ErrInternal(err, fmt.Sprintf("fail to parse bond address(%s)", msg.BondAddress))
	}
	if !bp.Has(from) && !na.BondAddress.Equals(msg.BondAddress) {
		return cosmos.ErrUnauthorized(fmt.Sprintf("%s are not authorized to manage %s", msg.BondAddress, msg.NodeAddress))
	}

	return nil
}
