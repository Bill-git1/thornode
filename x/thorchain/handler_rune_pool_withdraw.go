package thorchain

import (
	"fmt"

	"github.com/blang/semver"
	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
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
		"tx_id", msg.Tx.ID,
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
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolWithdrawHandler) validateV3_0_0(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
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
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolWithdrawHandler) handleV3_0_0(ctx cosmos.Context, msg MsgRunePoolWithdraw) error {
	accAddr, err := cosmos.AccAddressFromBech32(msg.Signer.String())
	if err != nil {
		return fmt.Errorf("unable to AccAddressFromBech32: %s", err)
	}
	runeProvider, err := h.mgr.Keeper().GetRUNEProvider(ctx, accAddr)
	if err != nil {
		return fmt.Errorf("unable to GetRUNEProvider: %s", err)
	}

	// ensure the deposit has reached maturity
	depositMaturity := h.mgr.Keeper().GetConfigInt64(ctx, constants.RUNEPoolDepositMaturityBlocks)
	currentBlockHeight := ctx.BlockHeight()
	blocksSinceLastDeposit := currentBlockHeight - runeProvider.LastDepositHeight
	if blocksSinceLastDeposit < depositMaturity {
		return fmt.Errorf("deposit reaches maturity in %d blocks", depositMaturity-blocksSinceLastDeposit)
	}

	// rune pool tracks the reserve and pooler unit shares of pol
	runePool, err := h.mgr.Keeper().GetRUNEPool(ctx)
	if err != nil {
		return fmt.Errorf("fail to get rune pool: %s", err)
	}

	// compute withdraw units
	maxBps := cosmos.NewUint(constants.MaxBasisPts)
	withdrawUnits := common.GetSafeShare(msg.BasisPoints, maxBps, runeProvider.Units)

	totalRunePoolValue, err := runePoolValue(ctx, h.mgr)
	if err != nil {
		return fmt.Errorf("fail to get rune pool value: %w", err)
	}

	// determine the profit of the withdraw amount to share with affiliate
	affiliateAmount := cosmos.ZeroUint()
	if !msg.AffiliateBasisPoints.IsZero() {
		totalUnits := runePool.TotalUnits()
		currentValue := common.GetSafeShare(runeProvider.Units, totalUnits, totalRunePoolValue)
		depositRemaining := common.SafeSub(runeProvider.DepositAmount, runeProvider.WithdrawAmount)
		currentYield := common.SafeSub(currentValue, depositRemaining)
		withdrawYield := common.GetSafeShare(msg.BasisPoints, maxBps, currentYield)
		affiliateAmount = common.GetSafeShare(msg.AffiliateBasisPoints, maxBps, withdrawYield)
	}

	// compute withdraw amount
	withdrawAmount := common.GetSafeShare(withdrawUnits, runePool.TotalUnits(), totalRunePoolValue)

	// if insufficient pending units, reserve should enter to create space for withdraw
	pendingRune := h.mgr.Keeper().GetRuneBalanceOfModule(ctx, RUNEPoolName)
	if withdrawAmount.GT(pendingRune) {
		reserveEnterRune := common.SafeSub(withdrawAmount, pendingRune)

		// There may be cases where providers are in a state of profit, and for the reserve
		// to buy their share of POL it must exceed POLMaxNetworkDeposit to cover the profit
		// of the provider. We allow exceeding this limit up to RUNEPoolMaxReserveBackstop
		// beyond the POLMaxNetworkDeposit as a circuit breaker. If the circuit breaker is
		// reached, withdraws will fail pending governance to increase the limit or extend
		// logic to trigger POL withdraw and sacrifice pool depth to satisfy withdrawals.
		maxReserveBackstop := h.mgr.Keeper().GetConfigInt64(ctx, constants.RUNEPoolMaxReserveBackstop)
		polMaxNetworkDeposit := h.mgr.Keeper().GetConfigInt64(ctx, constants.POLMaxNetworkDeposit)
		maxReserveUsage := cosmos.NewInt(maxReserveBackstop + polMaxNetworkDeposit)
		pol, err := h.mgr.Keeper().GetPOL(ctx)
		if err != nil {
			return fmt.Errorf("fail to get POL: %w", err)
		}
		currentReserveDeposit := pol.CurrentDeposit().
			Sub(runePool.CurrentDeposit()).
			Add(cosmos.NewIntFromBigInt(pendingRune.BigInt()))
		newReserveDeposit := currentReserveDeposit.Add(cosmos.NewIntFromBigInt(reserveEnterRune.BigInt()))
		if newReserveDeposit.GT(maxReserveUsage) {
			return fmt.Errorf("reserve enter %d rune exceeds backstop", reserveEnterRune.Uint64())
		}

		err = reserveEnterRUNEPool(ctx, h.mgr, reserveEnterRune)
		if err != nil {
			return fmt.Errorf("fail to reserve enter rune pool: %w", err)
		}

		// fetch rune pool after reserve enter for updated units
		runePool, err = h.mgr.Keeper().GetRUNEPool(ctx)
		if err != nil {
			return fmt.Errorf("fail to get rune pool: %w", err)
		}
	}

	// update provider and rune pool records
	runeProvider.Units = common.SafeSub(runeProvider.Units, withdrawUnits)
	runeProvider.WithdrawAmount = runeProvider.WithdrawAmount.Add(withdrawAmount)
	runeProvider.LastWithdrawHeight = ctx.BlockHeight()
	h.mgr.Keeper().SetRUNEProvider(ctx, runeProvider)
	runePool.PoolUnits = common.SafeSub(runePool.PoolUnits, withdrawUnits)
	runePool.RuneWithdrawn = runePool.RuneWithdrawn.Add(withdrawAmount)
	h.mgr.Keeper().SetRUNEPool(ctx, runePool)

	// send the affiliate fee
	userAmount := common.SafeSub(withdrawAmount, affiliateAmount)
	if !affiliateAmount.IsZero() {
		affiliateCoins := common.NewCoins(common.NewCoin(common.RuneNative, affiliateAmount))
		affiliateAddress, err := msg.AffiliateAddress.AccAddress()
		if err != nil {
			return fmt.Errorf("fail to get affiliate address: %w", err)
		}
		err = h.mgr.Keeper().SendFromModuleToAccount(ctx, RUNEPoolName, affiliateAddress, affiliateCoins)
		if err != nil {
			return fmt.Errorf("fail to send affiliate fee: %w", err)
		}
	}

	// send the user the withdraw
	userCoins := common.NewCoins(common.NewCoin(common.RuneNative, userAmount))
	err = h.mgr.Keeper().SendFromModuleToAccount(ctx, RUNEPoolName, msg.Signer, userCoins)
	if err != nil {
		return fmt.Errorf("fail to send user withdraw: %w", err)
	}

	ctx.Logger().Info(
		"runepool withdraw",
		"address", msg.Signer,
		"units", withdrawUnits,
		"amount", userAmount,
		"affiliate_amount", affiliateAmount,
	)

	withdrawEvent := NewEventRUNEPoolWithdraw(
		runeProvider.RuneAddress,
		int64(msg.BasisPoints.Uint64()),
		withdrawAmount,
		withdrawUnits,
		msg.Tx.ID,
		msg.AffiliateAddress,
		int64(msg.AffiliateBasisPoints.Uint64()),
		affiliateAmount,
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
