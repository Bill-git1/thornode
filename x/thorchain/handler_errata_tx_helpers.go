package thorchain

import (
	"context"
	"fmt"

	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

func processErrataTxAttestation(
	ctx cosmos.Context,
	mgr Manager,
	voter *keeper.ErrataTxVoter,
	attester cosmos.AccAddress,
	active NodeAccounts,
	er *common.ErrataTx,
	shouldSlashForDuplicate bool,
) error {
	k := mgr.Keeper()
	eventMgr := mgr.EventMgr()

	observeSlashPoints := mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := k.GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{ // nolint
		telemetry.NewLabel("reason", "failed_observe_errata"),
		telemetry.NewLabel("chain", string(er.Chain)),
	}))

	slasher := mgr.Slasher()

	if !voter.Sign(attester) {
		// Slash for the network having to handle the extra message/s.
		if shouldSlashForDuplicate {
			slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
		}
		ctx.Logger().Info("signer already signed MsgErrataTx", "signer", attester.String(), "txid", er.Id)
		return nil
	}

	// doesn't have consensus yet
	if !voter.HasConsensus(active) {
		// Before consensus, slash until consensus.
		slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
		ctx.Logger().Info("not having consensus yet, return")
		return nil
	}

	if voter.BlockHeight > 0 {
		// After consensus, only decrement slash points if within the ObservationDelayFlexibility period.
		if (voter.BlockHeight + observeFlex) >= ctx.BlockHeight() {
			slasher.DecSlashPoints(slashCtx, lackOfObservationPenalty, attester)
		}
		// errata tx already processed
		return nil
	}

	voter.BlockHeight = ctx.BlockHeight()

	// This signer brings the voter to consensus; increment the signer's slash points like the before-consensus signers,
	// then decrement all the signers' slash points and increment the non-signers' slash points.
	slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
	signers := voter.GetSigners()
	nonSigners := getNonSigners(active, signers)
	slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
	slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)

	observedVoter, err := k.GetObservedTxInVoter(ctx, er.Id)
	if err != nil {
		return err
	}

	if len(observedVoter.Txs) == 0 {
		return processErrataOutboundTx(ctx, k, eventMgr, er)
	}
	// set the observed Tx to reverted
	observedVoter.SetReverted()
	k.SetObservedTxInVoter(ctx, observedVoter)
	if observedVoter.Tx.IsEmpty() {
		ctx.Logger().Info("tx has not reach consensus yet, so nothing need to be done", "tx_id", er.Id)
		return nil
	}

	tx := observedVoter.Tx.Tx
	if !tx.Chain.Equals(er.Chain) {
		// does not match chain
		return nil
	}
	if observedVoter.UpdatedVault {
		vaultPubKey := observedVoter.Tx.ObservedPubKey
		if !vaultPubKey.IsEmpty() {
			// try to deduct the asset from asgard
			// trunk-ignore(golangci-lint/govet): shadow
			vault, err := k.GetVault(ctx, vaultPubKey)
			if err != nil {
				return fmt.Errorf("fail to get active asgard vaults: %w", err)
			}
			vault.SubFunds(tx.Coins)
			// trunk-ignore(golangci-lint/govet): shadow
			if err := k.SetVault(ctx, vault); err != nil {
				return fmt.Errorf("fail to save vault, err: %w", err)
			}
		}
	}

	if !observedVoter.Tx.IsFinal() {
		ctx.Logger().Info("tx is not finalised, so nothing need to be done", "tx_id", er.Id)
		return nil
	}

	memo, _ := ParseMemoWithTHORNames(ctx, k, tx.Memo)
	// if the tx is a migration , from old valut to new vault , then the inbound tx must have a related outbound tx as well
	if memo.IsInternal() {
		return processErrataOutboundTx(ctx, k, eventMgr, er)
	}

	if !memo.IsType(TxSwap) && !memo.IsType(TxAdd) {
		// must be a swap or add transaction
		return nil
	}

	runeCoin := common.NoCoin
	assetCoin := common.NoCoin
	for _, coin := range tx.Coins {
		if coin.IsRune() {
			runeCoin = coin
		} else {
			assetCoin = coin
		}
	}

	// fetch pool from memo
	pool, err := k.GetPool(ctx, assetCoin.Asset)
	if err != nil {
		ctx.Logger().Error("fail to get pool for errata tx", "error", err)
		return err
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
		// trunk-ignore(golangci-lint/govet): shadow
		lp, err := k.GetLiquidityProvider(ctx, pool.Asset, tx.FromAddress)
		if err != nil {
			return fmt.Errorf("fail to get liquidity provider: %w", err)
		}

		// since this address is being malicious, zero their liquidity provider units
		pool.LPUnits = common.SafeSub(pool.LPUnits, lp.Units)
		lp.Units = cosmos.ZeroUint()
		lp.LastAddHeight = ctx.BlockHeight()

		k.SetLiquidityProvider(ctx, lp)
	}

	// trunk-ignore(golangci-lint/govet): shadow
	if err := k.SetPool(ctx, pool); err != nil {
		ctx.Logger().Error("fail to save pool", "error", err)
	}

	// send errata event
	mods := PoolMods{
		NewPoolMod(pool.Asset, runeCoin.Amount, false, assetCoin.Amount, false),
	}

	eventErrata := NewEventErrata(er.Id, mods)
	if err := mgr.EventMgr().EmitEvent(ctx, eventErrata); err != nil {
		return ErrInternal(err, "fail to emit errata event")
	}
	return nil
}

// processErrataOutboundTx when the network detect an outbound tx which previously had been sent out to customer , however it get re-org , and it doesn't
// exist on the external chain anymore , then it will need to reschedule the tx
func processErrataOutboundTx(ctx cosmos.Context, k keeper.Keeper, eventMgr EventManager, er *common.ErrataTx) error {
	txOutVoter, err := k.GetObservedTxOutVoter(ctx, er.Id)
	if err != nil {
		return fmt.Errorf("fail to get observed tx out voter for tx (%s) : %w", er.Id, err)
	}
	if len(txOutVoter.Txs) == 0 {
		return fmt.Errorf("cannot find tx: %s", er.Id)
	}
	if txOutVoter.Tx.IsEmpty() {
		return fmt.Errorf("tx out voter is not finalised")
	}
	tx := txOutVoter.Tx.Tx
	if !tx.Chain.Equals(er.Chain) || tx.Coins.IsEmpty() {
		return nil
	}
	// parse the outbound tx memo, so we can figure out which inbound tx triggered the outbound
	m, err := ParseMemoWithTHORNames(ctx, k, tx.Memo)
	if err != nil {
		return fmt.Errorf("fail to parse memo(%s): %w", tx.Memo, err)
	}
	if !m.IsOutbound() && !m.IsInternal() {
		return fmt.Errorf("%s is not outbound or internal tx", m)
	}
	vaultPubKey := txOutVoter.Tx.ObservedPubKey
	if !vaultPubKey.IsEmpty() {
		// trunk-ignore(golangci-lint/govet): shadow
		v, err := k.GetVault(ctx, vaultPubKey)
		if err != nil {
			return fmt.Errorf("fail to get vault with pubkey %s: %w", vaultPubKey, err)
		}
		compensate := true
		if v.IsAsgard() {
			// if the fund is sending out from asgard , then it need to be credit back to asgard
			// if the asgard has been retired (inactive), need to set it to Retiring again , so the fund can be migrated
			v.AddFunds(tx.Coins)
			compensate = false
			if v.Status == InactiveVault {
				ctx.Logger().Info("Errata cause retired vault to be resurrect", "vault pub key", v.PubKey)
				v.UpdateStatus(RetiringVault, ctx.BlockHeight())
			}
		}

		if !v.IsEmpty() {
			// trunk-ignore(golangci-lint/govet): shadow
			if err := k.SetVault(ctx, v); err != nil {
				return fmt.Errorf("fail to save vault: %w", err)
			}
		}
		if compensate {
			for _, coin := range tx.Coins {
				if coin.IsRune() {
					// it is using native rune, so outbound can't be RUNE
					continue
				}
				// trunk-ignore(golangci-lint/govet): shadow
				p, err := k.GetPool(ctx, coin.Asset)
				if err != nil {
					return fmt.Errorf("fail to get pool(%s): %w", coin.Asset, err)
				}
				runeValue := p.AssetValueInRune(coin.Amount)
				p.BalanceRune = p.BalanceRune.Add(runeValue)
				p.BalanceAsset = common.SafeSub(p.BalanceAsset, coin.Amount)
				// trunk-ignore(golangci-lint/govet): shadow
				if err := k.SendFromModuleToModule(ctx, ReserveName, AsgardName, common.Coins{
					common.NewCoin(common.RuneAsset(), runeValue),
				}); err != nil {
					return fmt.Errorf("fail to send fund from reserve to asgard: %w", err)
				}
				// trunk-ignore(golangci-lint/govet): shadow
				if err := k.SetPool(ctx, p); err != nil {
					return fmt.Errorf("fail to save pool (%s) : %w", p.Asset, err)
				}
				// send errata event
				mods := PoolMods{
					NewPoolMod(p.Asset, runeValue, true, coin.Amount, false),
				}

				eventErrata := NewEventErrata(er.Id, mods)
				// trunk-ignore(golangci-lint/govet): shadow
				if err := eventMgr.EmitEvent(ctx, eventErrata); err != nil {
					return ErrInternal(err, "fail to emit errata event")
				}
			}
		}
	}

	// emit security event
	event := NewEventSecurity(tx, "outbound errata")
	// trunk-ignore(golangci-lint/govet): shadow
	if err := eventMgr.EmitEvent(ctx, event); err != nil {
		return ErrInternal(err, "fail to emit security event")
	}

	txOutVoter.SetReverted()
	k.SetObservedTxOutVoter(ctx, txOutVoter)
	return nil
}
