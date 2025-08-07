package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// ModifyLimitSwapHandler is the handler to process MsgModifyLimitSwap.
type ModifyLimitSwapHandler struct {
	mgr Manager
}

// NewModifyLimitSwapHandler creates a new instance of ModifyLimitSwapHandler.
func NewModifyLimitSwapHandler(mgr Manager) ModifyLimitSwapHandler {
	return ModifyLimitSwapHandler{
		mgr: mgr,
	}
}

// Run is the main entry point for ModifyLimitSwapHandler.
func (h ModifyLimitSwapHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgModifyLimitSwap)
	if !ok {
		return nil, errInvalidMessage
	}

	err := h.validate(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("MsgModifyLimitSwap failed validation", "error", err)
		return nil, err
	}

	err = h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgModifyLimitSwap", "error", err)
		return nil, err
	}

	return &cosmos.Result{}, err
}

func (h ModifyLimitSwapHandler) validate(ctx cosmos.Context, msg MsgModifyLimitSwap) error {
	return msg.ValidateBasic()
}

func (h ModifyLimitSwapHandler) handle(ctx cosmos.Context, msg MsgModifyLimitSwap) error {
	// Design Decision: Swaps are identified by source/target assets rather than tx_id
	// This allows users to modify their swaps without tracking the original transaction ID.
	// Security is maintained by verifying the FromAddress matches the original swap creator.
	// If multiple swaps exist with the same source/target for a user, only the first is modified.

	// get the txn hashes that match this fake swap msg
	items, err := h.mgr.Keeper().GetAdvSwapQueueIndex(ctx, MsgSwap{
		Tx: common.Tx{
			Coins: common.NewCoins(msg.Source),
		},
		TargetAsset: msg.Target.Asset,
		TradeTarget: msg.Target.Amount,
		SwapType:    LimitSwap,
	})
	if err != nil {
		return err
	}

	// convert the list of txn hashes to real msg swaps
	msgSwaps := make([]MsgSwap, 0)
	for _, item := range items {
		msgSwap, err := h.mgr.Keeper().GetAdvSwapQueueItem(ctx, item.TxID, item.Index)
		if err != nil {
			ctx.Logger().Error("fail to get swap book item", "hash", item.TxID, "index", item.Index)
			continue
		}

		// ensure addresses match so people can't change other people's limit swaps
		if !msgSwap.Tx.FromAddress.Equals(msg.From) {
			continue
		}

		msgSwaps = append(msgSwaps, msgSwap)
	}

	if len(msgSwaps) == 0 {
		return fmt.Errorf("could not find matching limit swap")
	}

	// Only modify the first matching swap, not all of them
	msgSwap := msgSwaps[0]
	if msg.ModifiedTargetAmount.IsZero() {
		// the target is being modified to zero, which is interpreted as a cancel
		if err := h.cancelLimitSwap(ctx, msgSwap); err != nil {
			return err
		}
	} else {
		// modify the limit swap
		if err := h.modifyLimitSwap(ctx, msgSwap, msg.ModifiedTargetAmount); err != nil {
			return err
		}
	}

	// Donate any incoming funds from the modification transaction to the pool
	if !msg.DepositAmount.IsZero() && !msg.DepositAsset.IsEmpty() {
		if err := h.donateToPool(ctx, msg.DepositAsset, msg.DepositAmount, msg.From); err != nil {
			ctx.Logger().Error("fail to donate modification tx funds to pool", "error", err, "asset", msg.DepositAsset, "amount", msg.DepositAmount)
			// Don't fail the modification if donation fails
		}
	}

	modEvent := NewEventModifyLimitSwap(msg.From, msg.Source, msg.Target, msg.ModifiedTargetAmount)
	if err := h.mgr.EventMgr().EmitEvent(ctx, modEvent); err != nil {
		ctx.Logger().Error("fail to emit modEvent event", "error", err)
	}

	return nil
}

// cancelLimitSwap handles the cancellation of a limit swap
func (h ModifyLimitSwapHandler) cancelLimitSwap(ctx cosmos.Context, msgSwap MsgSwap) error {
	// Use settleSwap to handle the cancellation
	// This will handle any partial swaps and refund the remainder
	return settleSwap(ctx, h.mgr, msgSwap, "limit swap cancelled")
}

// modifyLimitSwap handles the modification of a limit swap's target amount
func (h ModifyLimitSwapHandler) modifyLimitSwap(ctx cosmos.Context, msgSwap MsgSwap, newTargetAmount cosmos.Uint) error {
	// remove current index
	if err := h.mgr.Keeper().RemoveAdvSwapQueueIndex(ctx, msgSwap); err != nil {
		return err
	}

	// update trade target
	msgSwap.TradeTarget = newTargetAmount

	// save the modified swap back to the queue (SetAdvSwapQueueItem also updates the index)
	if err := h.mgr.Keeper().SetAdvSwapQueueItem(ctx, msgSwap); err != nil {
		return err
	}

	return nil
}

// donateToPool adds the given amount to the specified pool's balance
func (h ModifyLimitSwapHandler) donateToPool(ctx cosmos.Context, asset common.Asset, amount cosmos.Uint, from common.Address) error {
	// Get the pool for the asset
	pool, err := h.mgr.Keeper().GetPool(ctx, asset.GetLayer1Asset())
	if err != nil {
		return fmt.Errorf("fail to get pool: %w", err)
	}
	if pool.IsEmpty() {
		return fmt.Errorf("pool does not exist for asset %s", asset)
	}

	// Add the amount to the appropriate balance
	if asset.IsRune() {
		pool.BalanceRune = pool.BalanceRune.Add(amount)
	} else {
		pool.BalanceAsset = pool.BalanceAsset.Add(amount)
	}

	// Save the updated pool
	if err := h.mgr.Keeper().SetPool(ctx, pool); err != nil {
		return fmt.Errorf("fail to save pool: %w", err)
	}

	// Create a minimal transaction for the donation event
	tx := common.Tx{
		ID:          common.TxID(""),
		Chain:       asset.GetChain(),
		FromAddress: from,
		ToAddress:   common.NoAddress,
		Coins:       common.NewCoins(common.NewCoin(asset, amount)),
		Gas:         nil,
		Memo:        "THOR-MODIFY-LIMIT",
	}

	// Emit a donation event
	donateEvt := NewEventDonate(pool.Asset, tx)
	if err := h.mgr.EventMgr().EmitEvent(ctx, donateEvt); err != nil {
		ctx.Logger().Error("fail to emit donate event", "error", err)
	}

	return nil
}
