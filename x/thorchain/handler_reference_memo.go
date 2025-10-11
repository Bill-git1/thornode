package thorchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

// ReferenceMemoHandler a handler to process reference memo messages
type ReferenceMemoHandler struct {
	mgr Manager
}

// NewReferenceMemoHandler create a new instance of network fee handler
func NewReferenceMemoHandler(mgr Manager) ReferenceMemoHandler {
	return ReferenceMemoHandler{mgr: mgr}
}

// Run is the main entry point for network fee logic
func (h ReferenceMemoHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgReferenceMemo)
	if !ok {
		return nil, errInvalidMessage
	}
	ctx.Logger().Info("MsgReferenceMemo", "asset", msg.Asset, "memo", msg.Memo, "signer", msg.Signer)
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgReferenceMemo failed validation", "error", err)
		return nil, err
	}
	result, err := h.handle(ctx, *msg)
	if err != nil {
		ctx.Logger().Error("fail to process MsgReferenceMemo", "error", err)
	}
	return result, err
}

func (h ReferenceMemoHandler) validate(ctx cosmos.Context, msg MsgReferenceMemo) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	_, err := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), msg.Memo)
	if err != nil {
		return err
	}

	// Check if memoless transactions are halted
	haltMemoless, err := h.mgr.Keeper().GetMimir(ctx, constants.HaltMemoless.String())
	if err == nil && haltMemoless > 0 {
		return errors.New("memoless transactions are currently halted")
	}

	ttl := h.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnTTL)
	if ttl <= 0 {
		return errors.New("memoless transactions is currently disabled")
	}

	cost := h.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnCost)
	if cost > 0 {
		runeAmount := cosmos.NewInt(cost)
		cosmosRuneCoin := cosmos.NewCoin(common.RuneNative.Native(), runeAmount)
		if !h.mgr.Keeper().HasCoins(ctx, msg.Signer, cosmos.NewCoins(cosmosRuneCoin)) {
			return fmt.Errorf("insufficient RUNE balance to pay memoless transaction cost of %d", cost)
		}
	}

	return nil
}

// handle process MsgReferenceMemo
func (h ReferenceMemoHandler) handle(ctx cosmos.Context, msg MsgReferenceMemo) (*cosmos.Result, error) {
	ttl := h.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnTTL)
	cost := h.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnCost)

	// Find an available reference BEFORE charging cost to avoid charging users
	// when no references are available. References are allocated sequentially
	// starting from the last assigned number, preventing collisions even when
	// multiple users register concurrently.
	posStr := h.mgr.Keeper().GetLastReferenceNumber(ctx, msg.Asset)
	next := h.nextReference(ctx, msg.Asset, posStr, ttl)
	if next == "" {
		return nil, fmt.Errorf("unable to find an available reference")
	}

	// Only charge cost AFTER we've confirmed a reference is available.
	// This prevents users from losing RUNE when all references are exhausted.
	if cost > 0 {
		runeCoin := common.NewCoin(common.RuneNative, cosmos.NewUint(uint64(cost)))
		if err := h.mgr.Keeper().SendFromAccountToModule(ctx, msg.Signer, ReserveName, common.NewCoins(runeCoin)); err != nil {
			return nil, fmt.Errorf("fail to transfer memoless transaction cost to reserve: %w", err)
		}
	}

	hash := sha256.New()
	_, err := hash.Write(ctx.TxBytes())
	if err != nil {
		return nil, fmt.Errorf("fail to get txid: %w", err)
	}
	txid := hex.EncodeToString(hash.Sum(nil))
	txID, err := common.NewTxID(txid)
	if err != nil {
		return nil, fmt.Errorf("fail to get txid: %w", err)
	}

	refMemo := NewReferenceMemo(msg.Asset, msg.Memo, next, ctx.BlockHeight())
	refMemo.RegisteredBy = msg.Signer
	refMemo.RegistrationHash = txID

	h.mgr.Keeper().SetReferenceMemo(ctx, refMemo)
	h.mgr.Keeper().SetLastReferenceNumber(ctx, msg.Asset, next)
	ctx.Logger().Info("successfully registered memo", "asset", refMemo.Asset, "reference", refMemo.Reference, "memo", refMemo.Memo, "height", refMemo.Height, "registration_hash", refMemo.RegistrationHash)
	return &cosmos.Result{}, nil
}

// nextReference finds the next available reference for the asset
func (h ReferenceMemoHandler) nextReference(ctx cosmos.Context, asset common.Asset, posStr string, ttl int64) string {
	end := h.mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnRefCount)
	pos, _ := strconv.ParseInt(posStr, 10, 64)
	pos++ // next

	txnRefLength := len(fmt.Sprintf("%d", end))

	if pos > end {
		pos = 1
		posStr = leadingZeros(txnRefLength, fmt.Sprintf("%d", end))
	}

	// reachedEnd prevents infinite loops by tracking wraparounds.
	// We allow looping to the start once to search all references,
	// but return empty if we reach the end a second time (no available refs).
	reachedEnd := false
	for {
		if pos > end {
			pos = 1
			if reachedEnd {
				return ""
			}
			reachedEnd = true
		}
		ref := leadingZeros(txnRefLength, fmt.Sprintf("%d", pos))
		refMemo, err := h.mgr.Keeper().GetReferenceMemo(ctx, asset, ref)
		if err != nil {
			ctx.Logger().Error("fail to get ref memo", "error", err)
			pos++
			continue
		}

		// see if this memo is available, if its not being used, return it
		if refMemo.Height == 0 || refMemo.IsExpired(ctx.BlockHeight(), ttl) {
			return ref
		}
		// we've done a complete circle ending where we started. There are no
		// reference ids available right now
		if posStr == ref {
			return ""
		}
		pos++
	}
}
