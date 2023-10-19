package thorchain

import (
	"context"
	"fmt"

	"github.com/armon/go-metrics"
	"github.com/cosmos/cosmos-sdk/telemetry"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

func (h ErrataTxHandler) handleV58(ctx cosmos.Context, msg MsgErrataTx) (*cosmos.Result, error) {
	active, err := h.mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		return nil, wrapError(ctx, err, "fail to get list of active node accounts")
	}

	voter, err := h.mgr.Keeper().GetErrataTxVoter(ctx, msg.TxID, msg.Chain)
	if err != nil {
		return nil, err
	}
	observeSlashPoints := h.mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	observeFlex := h.mgr.GetConstants().GetInt64Value(constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{ // nolint
		telemetry.NewLabel("reason", "failed_observe_errata"),
		telemetry.NewLabel("chain", string(msg.Chain)),
	}))
	h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)

	if !voter.Sign(msg.Signer) {
		ctx.Logger().Info("signer already signed MsgErrataTx", "signer", msg.Signer.String(), "txid", msg.TxID)
		return &cosmos.Result{}, nil
	}
	h.mgr.Keeper().SetErrataTxVoter(ctx, voter)
	// doesn't have consensus yet
	if !voter.HasConsensus(active) {
		ctx.Logger().Info("not having consensus yet, return")
		return &cosmos.Result{}, nil
	}

	if voter.BlockHeight > 0 {
		if (voter.BlockHeight + observeFlex) >= ctx.BlockHeight() {
			h.mgr.Slasher().DecSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		}
		// errata tx already processed
		return &cosmos.Result{}, nil
	}

	voter.BlockHeight = ctx.BlockHeight()
	h.mgr.Keeper().SetErrataTxVoter(ctx, voter)
	// decrease the slash points
	h.mgr.Slasher().DecSlashPoints(slashCtx, observeSlashPoints, voter.GetSigners()...)
	observedVoter, err := h.mgr.Keeper().GetObservedTxInVoter(ctx, msg.TxID)
	if err != nil {
		return nil, err
	}

	if len(observedVoter.Txs) == 0 {
		return h.processErrataOutboundTx(ctx, msg)
	}
	// set the observed Tx to reverted
	observedVoter.SetReverted()
	h.mgr.Keeper().SetObservedTxInVoter(ctx, observedVoter)
	if observedVoter.Tx.IsEmpty() {
		ctx.Logger().Info("tx has not reach consensus yet, so nothing need to be done", "tx_id", msg.TxID)
		return &cosmos.Result{}, nil
	}

	tx := observedVoter.Tx.Tx
	if !tx.Chain.Equals(msg.Chain) {
		// does not match chain
		return &cosmos.Result{}, nil
	}
	if observedVoter.UpdatedVault {
		vaultPubKey := observedVoter.Tx.ObservedPubKey
		if !vaultPubKey.IsEmpty() {
			// try to deduct the asset from asgard
			vault, err := h.mgr.Keeper().GetVault(ctx, vaultPubKey)
			if err != nil {
				return nil, fmt.Errorf("fail to get active asgard vaults: %w", err)
			}
			vault.SubFunds(tx.Coins)
			if err := h.mgr.Keeper().SetVault(ctx, vault); err != nil {
				return nil, fmt.Errorf("fail to save vault, err: %w", err)
			}
		}
	}

	if !observedVoter.Tx.IsFinal() {
		ctx.Logger().Info("tx is not finalised, so nothing need to be done", "tx_id", msg.TxID)
		return &cosmos.Result{}, nil
	}

	memo, _ := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), tx.Memo)
	// if the tx is a migration , from old valut to new vault , then the inbound tx must have a related outbound tx as well
	if memo.IsInternal() {
		return h.processErrataOutboundTx(ctx, msg)
	}

	if !memo.IsType(TxSwap) && !memo.IsType(TxAdd) {
		// must be a swap or add transaction
		return &cosmos.Result{}, nil
	}

	runeCoin := common.NoCoin
	assetCoin := common.NoCoin
	for _, coin := range tx.Coins {
		if coin.Asset.IsRune() {
			runeCoin = coin
		} else {
			assetCoin = coin
		}
	}

	// fetch pool from memo
	pool, err := h.mgr.Keeper().GetPool(ctx, assetCoin.Asset)
	if err != nil {
		ctx.Logger().Error("fail to get pool for errata tx", "error", err)
		return nil, err
	}

	// subtract amounts from pool balances
	if runeCoin.Amount.GT(pool.BalanceRune) {
		runeCoin.Amount = pool.BalanceRune
	}
	if assetCoin.Amount.GT(pool.BalanceAsset) {
		assetCoin.Amount = pool.BalanceAsset
	}
	pool.BalanceRune = common.SafeSub(pool.BalanceRune, runeCoin.Amount)
	pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, assetCoin.Amount)
	if memo.IsType(TxAdd) {
		lp, err := h.mgr.Keeper().GetLiquidityProvider(ctx, pool.Asset, tx.FromAddress)
		if err != nil {
			return nil, fmt.Errorf("fail to get liquidity provider: %w", err)
		}

		// since this address is being malicious, zero their liquidity provider units
		pool.LPUnits = common.SafeSub(pool.LPUnits, lp.Units)
		lp.Units = cosmos.ZeroUint()
		lp.LastAddHeight = ctx.BlockHeight()

		h.mgr.Keeper().SetLiquidityProvider(ctx, lp)
	}

	if err := h.mgr.Keeper().SetPool(ctx, pool); err != nil {
		ctx.Logger().Error("fail to save pool", "error", err)
	}

	// send errata event
	mods := PoolMods{
		NewPoolMod(pool.Asset, runeCoin.Amount, false, assetCoin.Amount, false),
	}

	eventErrata := NewEventErrata(msg.TxID, mods)
	if err := h.mgr.EventMgr().EmitEvent(ctx, eventErrata); err != nil {
		return nil, ErrInternal(err, "fail to emit errata event")
	}
	return &cosmos.Result{}, nil
}
