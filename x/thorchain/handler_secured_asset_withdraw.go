package thorchain

import (
	"fmt"

	"github.com/blang/semver"
	"github.com/hashicorp/go-multierror"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// SecuredAssetWithdrawHandler is handler to process MsgSecuredAssetWithdraw
type SecuredAssetWithdrawHandler struct {
	mgr Manager
}

// NewSecuredAssetWithdrawHandler create a new instance of SecuredAssetWithdrawHandler
func NewSecuredAssetWithdrawHandler(mgr Manager) SecuredAssetWithdrawHandler {
	return SecuredAssetWithdrawHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for SecuredAssetWithdrawHandler
func (h SecuredAssetWithdrawHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgSecuredAssetWithdraw)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgSecuredAssetWithdraw failed validation", "error", err)
		return nil, err
	}
	err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgSecuredAssetWithdraw", "error", err)
	}
	return &cosmos.Result{}, err
}

func (h SecuredAssetWithdrawHandler) validate(ctx cosmos.Context, msg MsgSecuredAssetWithdraw) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h SecuredAssetWithdrawHandler) validateV3_0_0(ctx cosmos.Context, msg MsgSecuredAssetWithdraw) error {
	m, err := h.mgr.Keeper().GetMimirWithRef(ctx, constants.MimirTemplateSecuredAssetHaltWithdraw, msg.Asset.Chain.String())
	if err != nil {
		return err
	}
	if m > 0 && m <= ctx.BlockHeight() {
		return fmt.Errorf("%s secured asset withdrawals are disabled", msg.Asset.Chain)
	}

	return msg.ValidateBasic()
}

func (h SecuredAssetWithdrawHandler) handle(ctx cosmos.Context, msg MsgSecuredAssetWithdraw) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

// handle process MsgSecuredAssetWithdraw
func (h SecuredAssetWithdrawHandler) handleV3_0_0(ctx cosmos.Context, msg MsgSecuredAssetWithdraw) error {
	withdraw, _, err := h.mgr.SecuredAssetManager().Withdraw(ctx, msg.Asset, msg.Amount, msg.Signer, msg.AssetAddress, msg.Tx.ID)
	if err != nil {
		return err
	}

	layer1Asset := msg.Asset.GetLayer1Asset()

	toi := TxOutItem{
		Chain:     layer1Asset.GetChain(),
		InHash:    msg.Tx.ID,
		ToAddress: msg.AssetAddress,
		Coin:      common.NewCoin(layer1Asset, withdraw),
	}

	ok, err := h.mgr.TxOutStore().TryAddTxOutItem(ctx, h.mgr, toi, cosmos.ZeroUint())
	if err != nil {
		return multierror.Append(errFailAddOutboundTx, err)
	}
	if !ok {
		return errFailAddOutboundTx
	}

	return nil
}
