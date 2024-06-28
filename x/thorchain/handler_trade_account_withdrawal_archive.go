package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/mimir"
)

func (h TradeAccountWithdrawalHandler) validateV1(ctx cosmos.Context, msg MsgTradeAccountWithdrawal) error {
	if mimir.NewTradeAccountsEnabled().IsOff(ctx, h.mgr.Keeper()) {
		return fmt.Errorf("trade account is disabled")
	}
	return msg.ValidateBasic()
}
