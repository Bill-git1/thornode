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

	// Note: No contract whitelist is applied here. The execute interface is unrestricted
	// for any contract, similar to MsgExecuteContract. Restrictions only apply to
	// privileged operations like migrate, sudo, and instantiate.

	var (
		execAmt cosmos.Coin
		err     error
	)

	// The secured-asset deposit (which mints secured shares and increases pool
	// depth) and the contract execution must be atomic. Minting shares before the
	// contract call succeeds would allow a failing/reverting contract to leave the
	// minted shares in place while the original L1 inbound is refunded elsewhere,
	// double-spending vault liquidity. Run both inside a cache context and only
	// commit once execution succeeds; any earlier return discards the deposit along
	// with every other cached side effect.
	cacheCtx, commit := ctx.CacheContext()

	if msg.Asset.IsSecuredAsset() {
		execAmt = cosmos.NewCoin(msg.Asset.Native(), math.Int(msg.Amount))
	} else {
		execAmt, err = h.mgr.SecuredAssetManager().Deposit(cacheCtx, msg.Asset, msg.Amount, msg.Sender, msg.Tx.FromAddress, msg.Tx.ID)
		if err != nil {
			return nil, err
		}
	}

	_, err = h.mgr.WasmManager().ExecuteContract(cacheCtx, msg.Contract, msg.Sender, msg.Msg, cosmos.NewCoins(execAmt))
	if err != nil {
		return nil, err
	}

	// Both the deposit and the contract execution succeeded; commit the cached
	// state and events to the parent context.
	commit()

	return &cosmos.Result{}, nil
}
