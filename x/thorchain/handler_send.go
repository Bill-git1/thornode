package thorchain

import (
	"fmt"

	"github.com/blang/semver"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

// NewSendHandler create a new instance of SendHandler
func NewSendHandler(mgr Manager) BaseHandler[*MsgSend] {
	return BaseHandler[*MsgSend]{
		mgr:    mgr,
		logger: MsgSendLogger,
		validators: NewValidators[*MsgSend]().
			Register("1.130.0", MsgSendValidateV130),
		handlers: NewHandlers[*MsgSend]().
			Register("1.116.0", MsgSendHandleV116),
	}
}

func MsgSendValidateV130(ctx cosmos.Context, mgr Manager, msg *MsgSend) error {
	// ValidateBasic is also executed in message service router's handler and isn't versioned there
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	// disallow sends to modules, they should only be interacted with via deposit messages
	if IsModuleAccAddress(mgr.Keeper(), msg.ToAddress) {
		return fmt.Errorf("cannot use MsgSend for Module transactions, use MsgDeposit instead")
	}

	return nil
}

func MsgSendLogger(ctx cosmos.Context, msg *MsgSend) {
	ctx.Logger().Info("receive MsgSend", "from", msg.FromAddress, "to", msg.ToAddress, "coins", msg.Amount)
}

func MsgSendHandleV116(ctx cosmos.Context, mgr Manager, msg *MsgSend) (*cosmos.Result, error) {
	if mgr.Keeper().IsChainHalted(ctx, common.THORChain) {
		return nil, fmt.Errorf("unable to use MsgSend while THORChain is halted")
	}

	err := mgr.Keeper().SendCoins(ctx, msg.FromAddress, msg.ToAddress, msg.Amount)
	if err != nil {
		return nil, err
	}

	return &cosmos.Result{}, nil
}

// SendAnteHandler called by the ante handler to gate mempool entry
// and also during deliver. Store changes will persist if this function
// succeeds, regardless of the success of the transaction.
func SendAnteHandler(ctx cosmos.Context, v semver.Version, k keeper.Keeper, msg MsgSend) error {
	return k.DeductNativeTxFeeFromAccount(ctx, msg.GetSigners()[0])
}
