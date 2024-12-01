package thorchain

import (
	"fmt"

	"github.com/blang/semver"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// SecuredAssetDepositHandler is handler to process MsgSecuredAssetDeposit
type SecuredAssetDepositHandler struct {
	mgr Manager
}

// NewSecuredAssetDepositHandler create a new instance of SecuredAssetDepositHandler
func NewSecuredAssetDepositHandler(mgr Manager) SecuredAssetDepositHandler {
	return SecuredAssetDepositHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for SecuredAssetDepositHandler
func (h SecuredAssetDepositHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgSecuredAssetDeposit)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgSecuredAssetDeposit failed validation", "error", err)
		return nil, err
	}
	err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgSecuredAssetDeposit", "error", err)
		return nil, err
	}
	return &cosmos.Result{}, err
}

func (h SecuredAssetDepositHandler) validate(ctx cosmos.Context, msg MsgSecuredAssetDeposit) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h SecuredAssetDepositHandler) validateV3_0_0(ctx cosmos.Context, msg MsgSecuredAssetDeposit) error {
	if err := h.checkHalt(ctx, msg.Asset.Chain.String()); err != nil {
		return err
	}

	pool, err := h.mgr.Keeper().GetPool(ctx, msg.Asset)
	if err != nil {
		return fmt.Errorf("error loading %s pool", msg.Asset)
	}
	if !pool.IsAvailable() || pool.IsEmpty() {
		return fmt.Errorf("pool %s unavailable", msg.Asset)
	}

	if atTVLCap(ctx, common.NewCoins(
		common.NewCoin(msg.Asset, msg.Amount),
	), h.mgr) {
		return fmt.Errorf("%s secured deposits more than bond", msg.Asset)
	}

	return msg.ValidateBasic()
}

func (h SecuredAssetDepositHandler) handle(ctx cosmos.Context, msg MsgSecuredAssetDeposit) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

// handle process MsgSecuredAssetDeposit
func (h SecuredAssetDepositHandler) handleV3_0_0(ctx cosmos.Context, msg MsgSecuredAssetDeposit) error {
	_, err := h.mgr.SecuredAssetManager().Deposit(ctx, msg.Asset, msg.Amount, msg.Address, msg.Tx.FromAddress, msg.Tx.ID)
	if err != nil {
		ctx.Logger().Error("fail to handle Deposit", "error", err)
		return err
	}
	return nil
}

func (h SecuredAssetDepositHandler) checkHalt(ctx cosmos.Context, val string) error {
	m, err := h.mgr.Keeper().GetMimirWithRef(ctx, constants.MimirTemplateSecuredAssetHaltDeposit, val)
	if err != nil {
		return err
	}
	if m > 0 && m <= ctx.BlockHeight() {
		return fmt.Errorf("%s secured asset deposits are disabled", val)
	}
	return nil
}
