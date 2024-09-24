package thorchain

import (
	"fmt"
	"strconv"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	keeperv1 "gitlab.com/thorchain/thornode/x/thorchain/keeper/v1"

	"github.com/blang/semver"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
)

// ProposeUpgradeHandler is to handle the ProposeUpgrade message
type ProposeUpgradeHandler struct {
	mgr Manager
}

// NewProposeUpgradeHandler create new instance of ProposeUpgradeHandler
func NewProposeUpgradeHandler(mgr Manager) ProposeUpgradeHandler {
	return ProposeUpgradeHandler{
		mgr: mgr,
	}
}

// Run is the main entry point to execute upgrade proposal logic
func (h ProposeUpgradeHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgProposeUpgrade)
	if !ok {
		return nil, errInvalidMessage
	}

	u := msg.Upgrade

	ctx.Logger().Info(
		"Validator propose upgrade",
		"thor_address", msg.Signer.String(),
		"name", msg.Name,
		"height", u.Height,
		"info", u.Info,
	)

	if err := h.validate(ctx, msg); err != nil {
		ctx.Logger().Error("msg propose upgrade failed validation", "error", err)
		return nil, err
	}

	if err := h.handle(ctx, msg); err != nil {
		ctx.Logger().Error("failed to process msg propose upgrade", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}

func (h ProposeUpgradeHandler) validate(ctx cosmos.Context, msg *MsgProposeUpgrade) error {
	if h.mgr.GetVersion().GTE(semver.MustParse("2.136.0")) {
		return h.validateV136(ctx, msg)
	}
	return errBadVersion
}

func (h ProposeUpgradeHandler) validateV136(ctx cosmos.Context, msg *MsgProposeUpgrade) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if err := signedByActiveNodeAccount(ctx, h.mgr.Keeper(), msg.Signer); err != nil {
		return cosmos.ErrUnauthorized(err.Error())
	}

	if ctx.BlockHeight() >= msg.Upgrade.Height {
		return cosmos.ErrUnknownRequest(fmt.Sprintf("upgrade height %d must be in the future, current: %d", msg.Upgrade.Height, ctx.BlockHeight()))
	}

	k := h.mgr.Keeper()
	u, err := k.GetProposedUpgrade(ctx, msg.Name)
	if err != nil {
		return cosmos.ErrUnknownRequest(fmt.Sprintf("failed to get proposed upgrade: %s", msg.Name))
	}

	if u != nil {
		return cosmos.ErrUnknownRequest(fmt.Sprintf("upgrade proposal already exists: %s", msg.Name))
	}

	return nil
}

func (h ProposeUpgradeHandler) handle(ctx cosmos.Context, msg *MsgProposeUpgrade) error {
	u := msg.Upgrade
	name := msg.Name
	k := h.mgr.Keeper()

	if err := k.ProposeUpgrade(ctx, name, u); err != nil {
		return fmt.Errorf("failed to propose upgrade: %w", err)
	}

	ctx.EventManager().EmitEvent(
		cosmos.NewEvent("propose_upgrade",
			cosmos.NewAttribute("thor_address", msg.Signer.String()),
			cosmos.NewAttribute("name", name),
			cosmos.NewAttribute("height", strconv.FormatInt(u.Height, 10)),
			cosmos.NewAttribute("info", u.Info),
		),
	)

	k.ApproveUpgrade(ctx, msg.Signer, name)

	ctx.EventManager().EmitEvent(
		cosmos.NewEvent("approve_upgrade",
			cosmos.NewAttribute("thor_address", msg.Signer.String()),
			cosmos.NewAttribute("name", name),
		),
	)

	return scheduleUpgradeIfNecessary(ctx, k, name)
}

// ApproveUpgradeHandler is to handle the ApproveUpgrade message
type ApproveUpgradeHandler struct {
	mgr Manager
}

// NewApproveUpgradeHandler create new instance of ApproveUpgradeHandler
func NewApproveUpgradeHandler(mgr Manager) ApproveUpgradeHandler {
	return ApproveUpgradeHandler{
		mgr: mgr,
	}
}

// Run it the main entry point to execute Version logic
func (h ApproveUpgradeHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgApproveUpgrade)
	if !ok {
		return nil, errInvalidMessage
	}

	ctx.Logger().Info(
		"Validator approving upgrade",
		"thor_address", msg.Signer.String(),
		"name", msg.Name,
	)

	if err := h.validate(ctx, msg); err != nil {
		ctx.Logger().Error("msg approve upgrade failed validation", "error", err)
		return nil, err
	}

	if err := h.handle(ctx, msg); err != nil {
		ctx.Logger().Error("failed to process msg approve upgrade", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}

func (h ApproveUpgradeHandler) validate(ctx cosmos.Context, msg *MsgApproveUpgrade) error {
	if h.mgr.GetVersion().GTE(semver.MustParse("2.136.0")) {
		return h.validateV136(ctx, msg)
	}
	return errBadVersion
}

func (h ApproveUpgradeHandler) validateV136(ctx cosmos.Context, msg *MsgApproveUpgrade) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if err := signedByActiveNodeAccount(ctx, h.mgr.Keeper(), msg.Signer); err != nil {
		return cosmos.ErrUnauthorized(err.Error())
	}

	return nil
}

func (h ApproveUpgradeHandler) handle(ctx cosmos.Context, msg *MsgApproveUpgrade) error {
	k := h.mgr.Keeper()
	name := msg.Name

	k.ApproveUpgrade(ctx, msg.Signer, name)

	ctx.EventManager().EmitEvent(
		cosmos.NewEvent("approve_upgrade",
			cosmos.NewAttribute("thor_address", msg.Signer.String()),
			cosmos.NewAttribute("name", name),
		),
	)

	return scheduleUpgradeIfNecessary(ctx, k, name)
}

// RejectUpgradeHandler is to handle the RejectUpgrade message
type RejectUpgradeHandler struct {
	mgr Manager
}

// NewRejectUpgradeHandler create new instance of RejectUpgradeHandler
func NewRejectUpgradeHandler(mgr Manager) RejectUpgradeHandler {
	return RejectUpgradeHandler{
		mgr: mgr,
	}
}

// Run it the main entry point to execute Version logic
func (h RejectUpgradeHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgRejectUpgrade)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info(
		"Validator rejecting upgrade",
		"thor_address", msg.Signer.String(),
		"name", msg.Name,
	)
	if err := h.validate(ctx, msg); err != nil {
		ctx.Logger().Error("msg reject upgrade failed validation", "error", err)
		return nil, err
	}
	if err := h.handle(ctx, msg); err != nil {
		ctx.Logger().Error("failed to process msg reject upgrade", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}

func (h RejectUpgradeHandler) validate(ctx cosmos.Context, msg *MsgRejectUpgrade) error {
	if h.mgr.GetVersion().GTE(semver.MustParse("2.136.0")) {
		return h.validateV136(ctx, msg)
	}
	return errBadVersion
}

func (h RejectUpgradeHandler) validateV136(ctx cosmos.Context, msg *MsgRejectUpgrade) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if err := signedByActiveNodeAccount(ctx, h.mgr.Keeper(), msg.Signer); err != nil {
		return cosmos.ErrUnauthorized(err.Error())
	}

	return nil
}

func (h RejectUpgradeHandler) handle(ctx cosmos.Context, msg *MsgRejectUpgrade) error {
	k := h.mgr.Keeper()
	name := msg.Name

	k.RejectUpgrade(ctx, msg.Signer, name)

	ctx.EventManager().EmitEvent(
		cosmos.NewEvent("reject_upgrade",
			cosmos.NewAttribute("thor_address", msg.Signer.String()),
			cosmos.NewAttribute("name", name),
		),
	)

	return clearUpgradeIfNecessary(ctx, k, name)
}

func scheduleUpgradeIfNecessary(ctx cosmos.Context, k keeper.Keeper, name string) error {
	upgradePlan, upgradeScheduled := k.GetUpgradePlan(ctx)
	if upgradeScheduled && upgradePlan.Name == name {
		// already scheduled
		return nil
	}

	u, err := k.GetProposedUpgrade(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to get proposed upgrade: %w", err)
	}

	if u == nil {
		return fmt.Errorf("upgrade proposal not found: %s", name)
	}

	uq, err := keeperv1.UpgradeApprovedByMajority(ctx, k, name)
	if err != nil {
		return fmt.Errorf("failed to determine if upgrade is approved by majority threshold of validators: %w", err)
	}

	if uq.Approved {
		if upgradeScheduled {
			return fmt.Errorf("a different upgrade is already scheduled: %s", upgradePlan.Name)
		}

		// upgrade approval is now over the majority threshold
		return k.ScheduleUpgrade(ctx, upgradetypes.Plan{
			Name:   name,
			Height: u.Height,
			Info:   u.Info,
		})
	}

	return nil
}

func clearUpgradeIfNecessary(ctx cosmos.Context, k keeper.Keeper, name string) error {
	upgradePlan, upgradeScheduled := k.GetUpgradePlan(ctx)
	if !upgradeScheduled || (upgradeScheduled && upgradePlan.Name != name) {
		// upgrade by this name not scheduled
		return nil
	}

	uq, err := keeperv1.UpgradeApprovedByMajority(ctx, k, name)
	if err != nil {
		return fmt.Errorf("failed to determine if upgrade is approved by majority threshold of validators: %w", err)
	}

	if !uq.Approved {
		// upgrade approval dropped below the majority threshold. upgrade plan was on chain, so cancel it.
		k.ClearUpgradePlan(ctx)
	}

	return nil
}

// ActiveValidatorAnteHandler called by the ante handler to gate mempool entry and
// also during deliver to only active validator nodes. Store changes will persist
// if this function succeeds, regardless of the success of the transaction.
func ActiveValidatorAnteHandler(ctx cosmos.Context, v semver.Version, k keeper.Keeper, signer cosmos.AccAddress) error {
	if err := signedByActiveNodeAccount(ctx, k, signer); err != nil {
		return err
	}

	return k.DeductNativeTxFeeFromBond(ctx, signer)
}
