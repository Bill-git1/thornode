package thorchain

import (
	"fmt"

	math "cosmossdk.io/math"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// WasmExecHandler handles Exec memo calls from L1 integrations
type WasmExecHandler struct {
	mgr Manager
}

// NewWasmExecHandler create a new instance of WasmExecHandler
func NewWasmExecHandler(mgr Manager) WasmExecHandler {
	return WasmExecHandler{mgr: mgr}
}

// Run is the main entry of WasmExecHandler
func (h WasmExecHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgWasmExec)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgWasmExec failed validation", "error", err)
		return nil, err
	}
	result, err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgWasmExec", "error", err)
		return nil, err
	}
	return result, nil
}

func (h WasmExecHandler) validate(ctx cosmos.Context, msg MsgWasmExec) error {
	return msg.ValidateBasic()
}

func (h WasmExecHandler) handle(ctx cosmos.Context, msg MsgWasmExec) (*cosmos.Result, error) {
	ctx.Logger().Info("receive MsgWasmExec", "from", msg.Signer)
	if h.mgr.Keeper().IsChainHalted(ctx, common.THORChain) {
		return nil, fmt.Errorf("unable to use MsgWasmExec while THORChain is halted")
	}

	var (
		execAmt cosmos.Coin
		err     error
	)

	if msg.Asset.IsSecuredAsset() {
		execAmt = cosmos.NewCoin(msg.Asset.Native(), math.Int(msg.Amount))
	} else {
		execAmt, err = h.mgr.SecuredAssetManager().Deposit(ctx, msg.Asset, msg.Amount, msg.Sender, msg.Tx.FromAddress, msg.Tx.ID)
		if err != nil {
			return nil, err
		}
	}

	_, err = h.mgr.WasmManager().ExecuteContract(ctx, msg.Contract, msg.Sender, msg.Msg, cosmos.NewCoins(execAmt))
	if err != nil {
		return nil, err
	}

	return &cosmos.Result{}, nil
}
