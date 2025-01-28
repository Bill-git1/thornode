package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

func (h LeaveHandler) validateV3_0_0(ctx cosmos.Context, msg MsgLeave) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	jail, err := h.mgr.Keeper().GetNodeAccountJail(ctx, msg.NodeAddress)
	if err != nil {
		// ignore this error and carry on. Don't want a jail bug causing node
		// accounts to not be able to get their funds out
		ctx.Logger().Error("fail to get node account jail", "error", err)
	}
	if jail.IsJailed(ctx) {
		return fmt.Errorf("failed to leave due to jail status: (release height %d) %s", jail.ReleaseHeight, jail.Reason)
	}

	return nil
}
