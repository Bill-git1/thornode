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

// RunePoolDepositHandler a handler to process deposits to RunePool
type RunePoolDepositHandler struct {
	mgr Manager
}

// NewRunePoolDepositHandler create new RunePoolDepositHandler
func NewRunePoolDepositHandler(mgr Manager) RunePoolDepositHandler {
	return RunePoolDepositHandler{
		mgr: mgr,
	}
}

// Run execute the handler
func (h RunePoolDepositHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgRunePoolDeposit)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info("receive MsgRunePoolDeposit",
		"tx_id", msg.Tx.ID,
		"rune_address", msg.Signer,
		"deposit_asset", msg.Tx.Coins[0].Asset,
		"deposit_amount", msg.Tx.Coins[0].Amount,
	)

	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("msg rune pool deposit failed validation", "error", err)
		return nil, err
	}

	err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process msg rune pool deposit", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, nil
}

func (h RunePoolDepositHandler) validate(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.validateV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolDepositHandler) validateV3_0_0(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}
	runePoolEnabled := h.mgr.Keeper().GetConfigInt64(ctx, constants.RUNEPoolEnabled)
	if runePoolEnabled <= 0 {
		return fmt.Errorf("RUNEPool disabled")
	}
	return nil
}

func (h RunePoolDepositHandler) handle(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return h.handleV3_0_0(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolDepositHandler) handleV3_0_0(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
	// get rune pool value before deposit
	runePoolValue, err := runePoolValue(ctx, h.mgr)
	if err != nil {
		return fmt.Errorf("fail to get rune pool value: %s", err)
	}

	// send deposit to runepool module
	err = h.mgr.Keeper().SendFromModuleToModule(
		ctx,
		AsgardName,
		RUNEPoolName,
		common.Coins{msg.Tx.Coins[0]},
	)
	if err != nil {
		return fmt.Errorf("unable to SendFromModuleToModule: %s", err)
	}

	accAddr, err := cosmos.AccAddressFromBech32(msg.Signer.String())
	if err != nil {
		return fmt.Errorf("unable to decode AccAddressFromBech32: %s", err)
	}
	runeProvider, err := h.mgr.Keeper().GetRUNEProvider(ctx, accAddr)
	if err != nil {
		return fmt.Errorf("unable to GetRUNEProvider: %s", err)
	}

	runeProvider.LastDepositHeight = ctx.BlockHeight()
	runeProvider.DepositAmount = runeProvider.DepositAmount.Add(msg.Tx.Coins[0].Amount)

	// rune pool tracks the reserve and pooler unit shares of pol
	runePool, err := h.mgr.Keeper().GetRUNEPool(ctx)
	if err != nil {
		return fmt.Errorf("fail to get rune pool: %s", err)
	}

	// if there are no units, this is the initial deposit
	depositUnits := msg.Tx.Coins[0].Amount

	// compute deposit units
	if !runePool.TotalUnits().IsZero() {
		depositRune := msg.Tx.Coins[0].Amount
		depositUnits = common.GetSafeShare(depositRune, runePoolValue, runePool.TotalUnits())
	}

	// update the provider and rune pool records
	runeProvider.Units = runeProvider.Units.Add(depositUnits)
	h.mgr.Keeper().SetRUNEProvider(ctx, runeProvider)
	runePool.PoolUnits = runePool.PoolUnits.Add(depositUnits)
	runePool.RuneDeposited = runePool.RuneDeposited.Add(msg.Tx.Coins[0].Amount)
	h.mgr.Keeper().SetRUNEPool(ctx, runePool)

	// rebalance ownership from reserve to poolers if able
	err = reserveExitRUNEPool(ctx, h.mgr)
	if err != nil {
		return fmt.Errorf("fail to exit reserve rune pool: %w", err)
	}

	ctx.Logger().Info(
		"runepool deposit",
		"address", msg.Signer,
		"units", depositUnits,
		"amount", msg.Tx.Coins[0].Amount,
	)

	depositEvent := NewEventRUNEPoolDeposit(
		runeProvider.RuneAddress,
		msg.Tx.Coins[0].Amount,
		depositUnits,
		msg.Tx.ID,
	)
	if err := h.mgr.EventMgr().EmitEvent(ctx, depositEvent); err != nil {
		ctx.Logger().Error("fail to emit rune pool deposit event", "error", err)
	}

	telemetry.IncrCounterWithLabels(
		[]string{"thornode", "rune_pool", "deposit_count"},
		float32(1),
		[]metrics.Label{},
	)
	telemetry.IncrCounterWithLabels(
		[]string{"thornode", "rune_pool", "deposit_amount"},
		telem(depositEvent.RuneAmount),
		[]metrics.Label{},
	)

	return nil
}
