package thorchain

import (
	"fmt"

	"github.com/armon/go-metrics"
	"github.com/blang/semver"
	"github.com/cosmos/cosmos-sdk/telemetry"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

// RunePoolWithdrawHandler a handler to process withdrawals from RunePool
type RunePoolWithdrawHandler struct {
	mgr Manager
}

// NewRunePoolWithdrawHandler create new RunePoolWithdrawHandler
func NewRunePoolWithdrawHandler(mgr Manager) RunePoolWithdrawHandler {
	return RunePoolWithdrawHandler{
		mgr: mgr,
	}
}

// Run execute the handler
func (h RunePoolWithdrawHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgRunePoolWithdraw)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info("receive MsgRunePoolWithdraw",
		"signer", msg.Signer,
		"basis_points", msg.BasisPoints,
		"affiliate_address", msg.AffiliateAddress,
		"affiliate_basis_points", msg.AffiliateBasisPoints,
	)

	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("msg rune pool withdraw failed validation", "error", err)
		return nil, err
	}

	err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process msg rune pool withdraw", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}

func (h RunePoolWithdrawHandler) validate(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("1.134.0")):
		return h.validateV134(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolWithdrawHandler) validateV134(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	runePoolEnabled := h.mgr.Keeper().GetConfigInt64(ctx, constants.RUNEPoolEnabled)
	if runePoolEnabled <= 0 {
		return fmt.Errorf("RUNEPool disabled")
	}
	maxAffBasisPts := h.mgr.Keeper().GetConfigInt64(ctx, constants.MaxAffiliateFeeBasisPoints)
	if !msg.AffiliateBasisPoints.IsZero() && msg.AffiliateBasisPoints.GT(cosmos.NewUint(uint64(maxAffBasisPts))) {
		return fmt.Errorf("invalid affiliate basis points, max: %d, request: %d", maxAffBasisPts, msg.AffiliateBasisPoints.Uint64())
	}
	return nil
}

func (h RunePoolWithdrawHandler) handle(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("1.134.0")):
		return h.handleV134(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolWithdrawHandler) handleV134(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
	accAddr, err := cosmos.AccAddressFromBech32(msg.Signer.String())
	if err != nil {
		return fmt.Errorf("unable to AccAddressFromBech32: %s", err)
	}
	runeProvider, err := h.mgr.Keeper().GetRUNEProvider(ctx, accAddr)
	if err != nil {
		return fmt.Errorf("unable to GetRUNEProvider: %s", err)
	}

	runePoolCooldown := h.mgr.Keeper().GetConfigInt64(ctx, constants.RUNEPoolCooldown)
	currentBlockHeight := ctx.BlockHeight()

	blocksSinceLastWithdraw := currentBlockHeight - runeProvider.LastWithdrawHeight
	blocksSinceLastDeposit := currentBlockHeight - runeProvider.LastDepositHeight

	if blocksSinceLastWithdraw < runePoolCooldown {
		tryAgain := runePoolCooldown - blocksSinceLastWithdraw
		return fmt.Errorf(
			"last withdraw (%d blocks ago) sooner than RUNEPool cooldown (%d), please wait %d blocks and try again",
			blocksSinceLastWithdraw,
			runePoolCooldown,
			tryAgain,
		)
	}

	if blocksSinceLastDeposit < runePoolCooldown {
		tryAgain := runePoolCooldown - blocksSinceLastDeposit
		return fmt.Errorf(
			"last deposit (%d blocks ago) sooner than RUNEPool cooldown (%d), please wait %d blocks and try again",
			blocksSinceLastDeposit,
			runePoolCooldown,
			tryAgain,
		)
	}

	withdrawable := common.SafeSub(runeProvider.DepositAmount, runeProvider.WithdrawAmount)
	if withdrawable.IsZero() {
		return fmt.Errorf("nothing to withdraw")
	}

	var userRUNE cosmos.Uint
	userRUNE = common.GetSafeShare(msg.BasisPoints, cosmos.NewUint(constants.MaxBasisPts), withdrawable)
	if userRUNE.GT(withdrawable) {
		return fmt.Errorf("insufficient balance, withdrawable: %s", withdrawable.String())
	}

	affiliateRUNE := cosmos.ZeroUint()
	if !msg.AffiliateBasisPoints.IsZero() {
		affiliateRUNE = common.GetSafeShare(msg.AffiliateBasisPoints, cosmos.NewUint(constants.MaxBasisPts), userRUNE)
		userRUNE = common.SafeSub(userRUNE, affiliateRUNE)
		runeProvider.WithdrawAmount = runeProvider.WithdrawAmount.Add(affiliateRUNE)
	}
	runeProvider.LastWithdrawHeight = ctx.BlockHeight()
	runeProvider.WithdrawAmount = runeProvider.WithdrawAmount.Add(userRUNE)
	h.mgr.Keeper().SetRUNEProvider(ctx, runeProvider)

	err = h.mgr.Keeper().SendFromModuleToAccount(
		ctx,
		RUNEPoolName,
		runeProvider.RuneAddress,
		common.Coins{common.NewCoin(common.RuneNative, userRUNE)},
	)
	if err != nil {
		return fmt.Errorf("unable to SendFromModuleToAccount: %s", err)
	}

	if !affiliateRUNE.IsZero() {
		affAccAddr, err := msg.AffiliateAddress.AccAddress()
		if err != nil {
			return fmt.Errorf("unable to resolve affiliate AccAddress: %s", err)
		}
		err = h.mgr.Keeper().SendFromModuleToAccount(
			ctx,
			RUNEPoolName,
			affAccAddr,
			common.Coins{common.NewCoin(common.RuneNative, affiliateRUNE)},
		)
		if err != nil {
			return fmt.Errorf("unable to SendFromModuleToAccount (affiliate): %s", err)
		}
	}

	withdrawEvent := NewEventRUNEPoolWithdraw(
		runeProvider.RuneAddress,
		int64(msg.AffiliateBasisPoints.Uint64()),
		userRUNE,
		cosmos.ZeroUint(), // replace with units withdrawn once added
		msg.Tx.ID,
		msg.AffiliateAddress,
		int64(msg.AffiliateBasisPoints.Uint64()),
		affiliateRUNE,
	)
	if err := h.mgr.EventMgr().EmitEvent(ctx, withdrawEvent); err != nil {
		ctx.Logger().Error("fail to emit rune pool withdraw event", "error", err)
	}

	telemetry.IncrCounterWithLabels(
		[]string{"thornode", "rune_pool", "withdraw_count"},
		float32(1),
		[]metrics.Label{},
	)
	telemetry.IncrCounterWithLabels(
		[]string{"thornode", "rune_pool", "withdraw_amount"},
		telem(withdrawEvent.RuneAmount),
		[]metrics.Label{},
	)

	return nil
}
