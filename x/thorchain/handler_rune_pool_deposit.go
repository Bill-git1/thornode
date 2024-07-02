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
	case version.GTE(semver.MustParse("1.134.0")):
		return h.validateV134(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolDepositHandler) validateV134(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
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
	case version.GTE(semver.MustParse("1.134.0")):
		return h.handleV134(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h RunePoolDepositHandler) handleV134(ctx cosmos.Context, msg MsgRunePoolDeposit) error {
	err := h.mgr.Keeper().SendFromModuleToModule(
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

	runeProvider.LastDepositHeight = ctx.BlockHeight()
	runeProvider.DepositAmount = runeProvider.DepositAmount.Add(msg.Tx.Coins[0].Amount)
	h.mgr.Keeper().SetRUNEProvider(ctx, runeProvider)

	depositEvent := NewEventRUNEPoolDeposit(
		runeProvider.RuneAddress,
		msg.Tx.Coins[0].Amount,
		cosmos.ZeroUint(), // replace with units withdrawn once added
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
