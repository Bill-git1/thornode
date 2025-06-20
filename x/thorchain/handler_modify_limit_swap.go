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
	hashes, err := h.mgr.Keeper().GetAdvSwapQueueIndex(ctx, MsgSwap{
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
	for _, hash := range hashes {
		msgSwap, err := h.mgr.Keeper().GetAdvSwapQueueItem(ctx, hash)
		if err != nil {
			ctx.Logger().Error("fail to get swap book item", "hash", hash)
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
		if err := h.mgr.Keeper().RemoveAdvSwapQueueIndex(ctx, msgSwap); err != nil {
			return err
		}
		if err := h.mgr.Keeper().RemoveAdvSwapQueueItem(ctx, msgSwap.Tx.ID); err != nil {
			return err
		}

		// Refund the original transaction
		voter, voterErr := h.mgr.Keeper().GetObservedTxInVoter(ctx, msgSwap.Tx.ID)
		var refundErr error
		if voterErr == nil && !voter.Tx.IsEmpty() {
			refundErr = refundTx(ctx, ObservedTx{Tx: msgSwap.Tx, ObservedPubKey: voter.Tx.ObservedPubKey}, h.mgr, CodeSwapFail, "limit swap cancelled", "")
		} else {
			ctx.Logger().Error("fail to get non-empty observed tx", "error", voterErr)
			refundErr = refundTx(ctx, ObservedTx{Tx: msgSwap.Tx}, h.mgr, CodeSwapFail, "limit swap cancelled", "")
		}

		if refundErr != nil {
			ctx.Logger().Error("fail to refund cancelled limit swap", "error", refundErr)
			return refundErr
		}
	} else {
		// remove current index
		if err := h.mgr.Keeper().RemoveAdvSwapQueueIndex(ctx, msgSwap); err != nil {
			return err
		}

		// update trade target
		msgSwap.TradeTarget = msg.ModifiedTargetAmount
		// save new index and swap limit item
		if err := h.mgr.AdvSwapQueueMgr().AddSwapQueueItem(ctx, msgSwap); err != nil {
			return err
		}
	}

	modEvent := NewEventModifyLimitSwap(msg.From, msg.Source, msg.Target, msg.ModifiedTargetAmount)
	if err := h.mgr.EventMgr().EmitEvent(ctx, modEvent); err != nil {
		ctx.Logger().Error("fail to emit modEvent event", "error", err)
	}

	return nil
}
