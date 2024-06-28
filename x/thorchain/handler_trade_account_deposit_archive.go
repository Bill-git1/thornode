package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/mimir"
)

func (h TradeAccountDepositHandler) validateV1(ctx cosmos.Context, msg MsgTradeAccountDeposit) error {
	if mimir.NewTradeAccountsEnabled().IsOff(ctx, h.mgr.Keeper()) {
		return fmt.Errorf("trade account is disabled")
	}
	return msg.ValidateBasic()
}
