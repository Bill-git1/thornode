package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// RotateHandler is the handler to process MsgRotate.
type RotateHandler struct {
	mgr Manager
}

// NewRotateHandler creates a new instance of RotateHandler.
func NewRotateHandler(mgr Manager) RotateHandler {
	return RotateHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for RotateHandler.
func (h RotateHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgRotate)
	if !ok {
		return nil, errInvalidMessage
	}

	err := h.validate(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("MsgRotate failed validation", "error", err)
		return nil, err
	}

	err = h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgRotate", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, err
}

func (h RotateHandler) validate(ctx cosmos.Context, msg MsgRotate) error {
	return msg.ValidateBasic()
}

func (h RotateHandler) handle(ctx cosmos.Context, msg MsgRotate) error {
	// find nodes operated by the signer
	iter := h.mgr.Keeper().GetNodeAccountIterator(ctx)
	defer iter.Close()
	rotateNodes := NodeAccounts{}
	for ; iter.Valid(); iter.Next() {
		var na NodeAccount
		if err := h.mgr.Keeper().Cdc().Unmarshal(iter.Value(), &na); err != nil {
			return fmt.Errorf("fail to unmarshal node account, %w", err)
		}
		if !na.IsEmpty() && na.BondAddress.Equals(common.Address(msg.Signer.String())) {
			rotateNodes = append(rotateNodes, na)
		}
	}

	// rotate each node
	for _, node := range rotateNodes {
		if err := h.rotate(ctx, msg.OperatorAddress, node); err != nil {
			return err
		}
	}

	return nil
}

func (h RotateHandler) rotate(ctx cosmos.Context, operator cosmos.AccAddress, nodeAcc NodeAccount) error {
	currentOperator, err := nodeAcc.BondAddress.AccAddress()
	if err != nil {
		return ErrInternal(err, "fail to get bond address")
	}

	// rotate the operator address
	nodeAcc.BondAddress = common.Address(operator.String())

	// get current bond provider records
	bp, err := h.mgr.Keeper().GetBondProviders(ctx, nodeAcc.NodeAddress)
	if err != nil {
		return ErrInternal(err, fmt.Sprintf("fail to get bond providers(%s)", nodeAcc.NodeAddress))
	}
	err = passiveBackfill(ctx, h.mgr, nodeAcc, &bp)
	if err != nil {
		return err
	}

	// update the corresponding bond provider record
	for i, provider := range bp.Providers {
		if provider.BondAddress.Equals(currentOperator) {
			bp.Providers[i].BondAddress = operator
		}
	}

	// store updated bond provider records
	err = h.mgr.Keeper().SetBondProviders(ctx, bp)
	if err != nil {
		return ErrInternal(err, "fail to save bond providers")
	}

	// store updated node account
	err = h.mgr.Keeper().SetNodeAccount(ctx, nodeAcc)
	if err != nil {
		return ErrInternal(err, "fail to save node account")
	}

	rotateEvent := NewEventRotate(currentOperator, nodeAcc.NodeAddress, operator)
	if err := h.mgr.EventMgr().EmitEvent(ctx, rotateEvent); err != nil {
		ctx.Logger().Error("fail to emit rotate event", "error", err)
	}

	return nil
}
