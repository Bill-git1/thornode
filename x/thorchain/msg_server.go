package thorchain

import (
	"context"
	"fmt"
	"runtime"

	errorsmod "cosmossdk.io/errors"
	storetypes "cosmossdk.io/store/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type msgServer struct {
	mgr Manager
}

var _ types.MsgServer = msgServer{}

// NewMsgServerImpl returns an implementation of the module MsgServer interface.
func NewMsgServerImpl(mgr Manager) types.MsgServer {
	return &msgServer{mgr: mgr}
}

// managersFor returns the managers a message handler runs with.
func (ms msgServer) managersFor(goCtx context.Context) (Manager, error) {
	return managersForExecMode(goCtx, ms.mgr)
}

// managersForExecMode returns shared for block execution. A simulation executes
// the full handler on a state branch that is discarded, but the shared managers
// buffer per-block data in memory (observers, gas) that the next EndBlock writes
// to state. A simulation on one node must not reach that, or that node alone
// commits different state, so it gets its own throwaway set.
func managersForExecMode(goCtx context.Context, shared Manager) (Manager, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	if ctx.ExecMode() != sdk.ExecModeSimulate {
		return shared, nil
	}
	mgrs, ok := shared.(*Mgrs)
	if !ok {
		return shared, nil
	}
	mgr := NewManagers(mgrs.Keeper(), mgrs.cdc, mgrs.storeService, mgrs.coinKeeper,
		mgrs.accountKeeper, mgrs.upgradeKeeper, mgrs.wasmKeeper)
	// Loading reads node accounts; keep that out of the simulated tx's gas estimate.
	if err := mgr.LoadManagerIfNecessary(ctx.WithGasMeter(storetypes.NewInfiniteGasMeter())); err != nil {
		return nil, fmt.Errorf("fail to load managers for simulation: %w", err)
	}
	return mgr, nil
}

func (ms msgServer) Ban(goCtx context.Context, msg *types.MsgBan) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewBanHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) Deposit(goCtx context.Context, msg *types.MsgDeposit) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewDepositHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ErrataTx(goCtx context.Context, msg *types.MsgErrataTx) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewErrataTxHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ErrataTxQuorum(goCtx context.Context, msg *types.MsgErrataTxQuorum) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewErrataTxQuorumHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) Mimir(goCtx context.Context, msg *types.MsgMimir) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewMimirHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) NetworkFee(goCtx context.Context, msg *types.MsgNetworkFee) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewNetworkFeeHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) NetworkFeeQuorum(goCtx context.Context, msg *types.MsgNetworkFeeQuorum) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewNetworkFeeQuorumHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) NodePauseChain(goCtx context.Context, msg *types.MsgNodePauseChain) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewNodePauseChainHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ObservedTxIn(goCtx context.Context, msg *types.MsgObservedTxIn) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewObservedTxInHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ObservedTxOut(goCtx context.Context, msg *types.MsgObservedTxOut) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewObservedTxOutHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ObservedTxQuorum(goCtx context.Context, msg *types.MsgObservedTxQuorum) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewObservedTxQuorumHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) PriceFeedQuorumBatch(goCtx context.Context, msg *types.MsgPriceFeedQuorumBatch) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewPriceFeedQuorumBatchHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ThorSend(goCtx context.Context, msg *types.MsgSend) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewSendHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) SetIPAddress(goCtx context.Context, msg *types.MsgSetIPAddress) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewIPAddressHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) SetNodeKeys(goCtx context.Context, msg *types.MsgSetNodeKeys) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewSetNodeKeysHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) Solvency(goCtx context.Context, msg *types.MsgSolvency) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewSolvencyHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) SolvencyQuorum(goCtx context.Context, msg *types.MsgSolvencyQuorum) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewSolvencyQuorumHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) TssKeysignFail(goCtx context.Context, msg *types.MsgTssKeysignFail) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewTssKeysignHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) TssPool(goCtx context.Context, msg *types.MsgTssPool) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewTssHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) SetVersion(goCtx context.Context, msg *types.MsgSetVersion) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewVersionHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ProposeUpgrade(goCtx context.Context, msg *types.MsgProposeUpgrade) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewProposeUpgradeHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) ApproveUpgrade(goCtx context.Context, msg *types.MsgApproveUpgrade) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewApproveUpgradeHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func (ms msgServer) RejectUpgrade(goCtx context.Context, msg *types.MsgRejectUpgrade) (*types.MsgEmpty, error) {
	mgr, err := ms.managersFor(goCtx)
	if err != nil {
		return nil, err
	}
	handler := NewRejectUpgradeHandler(mgr)
	return externalHandler(goCtx, handler, msg)
}

func externalHandler(goCtx context.Context, handler MsgHandler, msg sdk.Msg) (_ *types.MsgEmpty, err error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	ctx = ctx.WithLogger(ctx.Logger().With("height", ctx.BlockHeight()))

	defer func() {
		if r := recover(); r != nil {
			// print stack
			stack := make([]byte, 1024)
			length := runtime.Stack(stack, true)
			ctx.Logger().Error("panic", "msg", msg)
			fmt.Println(string(stack[:length]))
			err = fmt.Errorf("panic: %v", r)
		}
	}()

	result, err := handler.Run(ctx, msg)

	if result != nil && result.Size() > 0 {
		return nil, fmt.Errorf("external handler, handler returned non-empty result, %s", msg)
	}
	if err != nil {
		if _, code, _ := errorsmod.ABCIInfo(err, false); code == 1 {
			// This would be redacted, so wrap it.
			err = errorsmod.Wrap(errInternal, err.Error())
		}
		return nil, err
	}

	return &types.MsgEmpty{}, err
}
