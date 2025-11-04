package thorchain

import (
	"context"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/telemetry"
	se "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

// processTxInAttestation processes a single attestation for an observed tx.
// This is used by both MsgObservedTxIn (single attester) and MsgObservedTxInQuorum (multiple attesters).
func processTxInAttestation(
	ctx cosmos.Context,
	mgr Manager,
	voter ObservedTxVoter,
	nas NodeAccounts,
	tx ObservedTx,
	signer cosmos.AccAddress,
	shouldSlashForDuplicate bool,
) (ObservedTxVoter, bool) {
	k := mgr.Keeper()
	slasher := mgr.Slasher()

	observeSlashPoints := mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := k.GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_txin"),
		telemetry.NewLabel("chain", string(tx.Tx.Chain)),
	}))
	slashCtx = ctx.WithContext(context.WithValue(slashCtx.Context(), constants.CtxObservedTx, tx.Tx.ID.String()))

	ok := false
	if err := k.SetLastObserveHeight(ctx, tx.Tx.Chain, signer, tx.BlockHeight); err != nil {
		ctx.Logger().Error("fail to save last observe height", "error", err, "signer", signer, "chain", tx.Tx.Chain)
	}

	// As an observation requires processing by all nodes no matter what,
	// any observation should increment ObserveSlashPoints,
	// to be decremented only if contributing to or within ObservationDelayFlexibility of consensus.
	slasher.IncSlashPoints(slashCtx, observeSlashPoints, signer)

	if !voter.Add(tx, signer) {
		if !shouldSlashForDuplicate {
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signer)
		}
		// A duplicate message, so do nothing further.
		return voter, ok
	}
	if voter.HasFinalised(nas) {
		if voter.FinalisedHeight == 0 {
			ok = true
			voter.Height = ctx.BlockHeight() // Always record the consensus height of the finalised Tx
			if voter.UnfinalizedHeight == 0 {
				voter.UnfinalizedHeight = voter.Height // Preserve first consensus height
			}
			voter.FinalisedHeight = ctx.BlockHeight()
			voter.Tx = *voter.GetTx(nas)

			// This signer brings the voter to consensus;
			// decrement all the signers' slash points and increment the non-signers' slash points.
			signers := voter.GetConsensusSigners()
			nonSigners := getNonSigners(nas, signers)
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
			slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)
		} else if ctx.BlockHeight() <= (voter.FinalisedHeight+observeFlex) &&
			voter.Tx.IsFinal() == tx.IsFinal() &&
			voter.Tx.Tx.EqualsEx(tx.Tx) &&
			!voter.Tx.HasSigned(signer) {
			// Track already-decremented slash points with the consensus Tx's Signers list.
			voter.Tx.Signers = append(voter.Tx.Signers, signer.String())
			// event the tx had been processed , given the signer just a bit late , so still take away their slash points
			// but only when the tx signer are voting is the tx that already reached consensus
			slasher.DecSlashPoints(slashCtx, observeSlashPoints+lackOfObservationPenalty, signer)
		}
	}
	if !ok && voter.HasConsensus(nas) && !tx.IsFinal() && voter.FinalisedHeight == 0 {
		if voter.Height == 0 {
			ok = true
			voter.Height = ctx.BlockHeight()
			if voter.UnfinalizedHeight == 0 {
				voter.UnfinalizedHeight = voter.Height // Preserve first consensus height
			}
			// this is the tx that has consensus
			voter.Tx = *voter.GetTx(nas)

			// This signer brings the voter to consensus;
			// decrement all the signers' slash points and increment the non-signers' slash points.
			signers := voter.GetConsensusSigners()
			nonSigners := getNonSigners(nas, signers)
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
			slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)
		} else if ctx.BlockHeight() <= (voter.Height+observeFlex) &&
			voter.Tx.IsFinal() == tx.IsFinal() &&
			voter.Tx.Tx.EqualsEx(tx.Tx) &&
			!voter.Tx.HasSigned(signer) {
			// Track already-decremented slash points with the consensus Tx's Signers list.
			voter.Tx.Signers = append(voter.Tx.Signers, signer.String())
			// event the tx had been processed , given the signer just a bit late , so still take away their slash points
			// but only when the tx signer are voting is the tx that already reached consensus
			slasher.DecSlashPoints(slashCtx, observeSlashPoints+lackOfObservationPenalty, signer)
		}
	}

	k.SetObservedTxInVoter(ctx, voter)

	// Check to see if we have enough identical observations to process the transaction
	return voter, ok
}

// ensureVaultAndGetTxInVoter will make sure the vault exists, then get the ObservedTxInVoter from the store.
// if it doesn't exist, it will create a new one.
func ensureVaultAndGetTxInVoter(ctx cosmos.Context, vaultPubKey common.PubKey, txID common.TxID, k keeper.Keeper) (ObservedTxVoter, error) {
	// check we are sending to a valid vault
	if !k.VaultExists(ctx, vaultPubKey) {
		ctx.Logger().Info("Not valid Observed Pubkey", "observed pub key", vaultPubKey)
		return ObservedTxVoter{}, fmt.Errorf("vault not found for observed tx in pubkey: %s", vaultPubKey)
	}

	voter, err := k.GetObservedTxInVoter(ctx, txID)
	if err != nil {
		return ObservedTxVoter{}, fmt.Errorf("fail to get tx in voter: %w", err)
	}

	return voter, nil
}

// handleObservedTxInQuorum - will process the observed tx in quorum.
// used by both MsgObservedTxIn and MsgObservedTxInQuorum after processing
// attestation(s).
func handleObservedTxInQuorum(
	ctx cosmos.Context,
	mgr Manager,
	signer cosmos.AccAddress,
	activeNodeAccounts NodeAccounts,
	handler cosmos.Handler,
	tx common.ObservedTx,
	voter ObservedTxVoter,
	observers []cosmos.AccAddress,
	isQuorum bool,
) error {
	if !isQuorum {
		if voter.Height == ctx.BlockHeight() || voter.FinalisedHeight == ctx.BlockHeight() {
			// we've already process the transaction, but we should still
			// update the observing addresses
			mgr.ObMgr().AppendObserver(tx.Tx.Chain, observers)
		}
		return nil
	}

	// all logic after this is upon consensus

	if voter.Reverted {
		ctx.Logger().Info("tx had been reverted", "Tx", tx.String())
		return nil
	}

	k := mgr.Keeper()

	vault, err := k.GetVault(ctx, tx.ObservedPubKey)
	if err != nil {
		ctx.Logger().Error("fail to get vault", "error", err)
		return nil
	}

	voter.Tx.Tx.Memo = tx.Tx.Memo

	// add memo for memoless transactions (after consensus is reached)
	if len(tx.Tx.Coins) > 0 {
		// Use the asset from the first coin for reference memo lookup
		refAsset := tx.Tx.Coins[0].Asset

		// Generate reference memo for memoless transactions
		if strings.TrimSpace(voter.Tx.Tx.Memo) == "" {
			var referenceID string
			referenceID, err = generateReferenceMemoID(ctx, mgr, refAsset, tx)
			if err != nil {
				ctx.Logger().Error("failed to generate reference memo", "error", err, "txid", tx.Tx.ID.String())
				// Continue without reference memo - will likely be refunded later due to empty memo
			} else {
				// Use ReferenceReadMemo's CreateMemo function
				// Note: Reference memo expiry and duplicate usage validation is handled later in fetchMemoFromReference
				refMemo := NewReferenceReadMemo(referenceID)
				voter.Tx.Tx.Memo = refMemo.CreateMemo()
			}
		}

	}

	// memo errors are ignored here and will be caught later in processing,
	// after vault update, voter setup, etc and the coin will be refunded
	memo, _ := ParseMemoWithTHORNames(ctx, k, tx.Tx.Memo)

	hasFinalised := voter.HasFinalised(activeNodeAccounts)

	// Update vault balances from inbounds with Migrate memos immediately,
	// to minimise any gap between outbound and inbound observations.
	// TODO: In future somehow update both balances in a single action,
	// so the ActiveVault balance increase is guaranteed to never be early nor late?
	if hasFinalised || memo.IsType(TxMigrate) {
		if vault.IsAsgard() && !voter.UpdatedVault {
			if !tx.Tx.FromAddress.Equals(tx.Tx.ToAddress) {
				// Don't add to or subtract from vault balances when the sender and recipient are the same
				// (particularly avoid Consolidate SafeSub zeroing of vault balances).
				vault.AddFunds(tx.Tx.Coins)
				vault.InboundTxCount++
			}
			voter.UpdatedVault = true
		}
	}
	if err = k.SetLastChainHeight(ctx, tx.Tx.Chain, tx.BlockHeight); err != nil {
		ctx.Logger().Error("fail to set last chain height", "error", err)
	}

	// save the changes in Tx Voter to key value store
	k.SetObservedTxInVoter(ctx, voter)
	if err = k.SetVault(ctx, vault); err != nil {
		ctx.Logger().Error("fail to set vault", "error", err)
		return nil
	}

	if !vault.IsAsgard() {
		ctx.Logger().Info("Vault is not an Asgard vault, transaction ignored.")
		return nil
	}

	if memo.IsOutbound() || memo.IsInternal() {
		// do not process outbound handlers here, or internal handlers
		return nil
	}

	// add addresses to observing addresses. This is used to detect
	// active/inactive observing node accounts

	mgr.ObMgr().AppendObserver(tx.Tx.Chain, voter.Tx.GetSigners())

	if !hasFinalised {
		ctx.Logger().Info("transaction pending confirmation counting", "hash", voter.TxID)
		return nil
	}

	ctx.Logger().Debug("tx in finalized and has consensus",
		"id", tx.Tx.ID.String(),
		"chain", tx.Tx.Chain.String(),
		"height", tx.BlockHeight,
		"from", tx.Tx.FromAddress.String(),
		"to", tx.Tx.ToAddress.String(),
		"memo", tx.Tx.Memo,
		"coins", tx.Tx.Coins.String(),
		"gas", common.Coins(tx.Tx.Gas).String(),
		"observed_vault_pubkey", tx.ObservedPubKey.String(),
	)

	if vault.Status == InactiveVault {
		ctx.Logger().Error("observed tx on inactive vault", "tx", tx.String())
		if newErr := refundTx(ctx, tx, mgr, CodeInvalidVault, "observed inbound tx to an inactive vault", ""); newErr != nil {
			ctx.Logger().Error("fail to refund", "error", newErr)
		}
		return nil
	}

	// Resolve reference memo if needed before processing
	if len(tx.Tx.Coins) > 0 {
		asset := tx.Tx.Coins[0].Asset
		resolvedMemo := fetchMemoFromReference(ctx, mgr, asset, voter.Tx.Tx, voter.UnfinalizedHeight)
		preMemo := voter.Tx.Tx.Memo
		voter.Tx.Tx.Memo = resolvedMemo
		ctx.Logger().Info("reference memo conversion", "pre", preMemo, "post", voter.Tx.Tx.Memo, "asset", asset)
		k.SetObservedTxInVoter(ctx, voter)
	}

	// construct msg from memo
	m, txErr := processOneTxIn(ctx, k, voter.Tx, signer)
	if txErr != nil {
		ctx.Logger().Error("fail to process inbound tx", "error", txErr.Error(), "tx hash", tx.Tx.ID.String())
		if newErr := refundTx(ctx, tx, mgr, CodeInvalidMemo, txErr.Error(), ""); nil != newErr {
			ctx.Logger().Error("fail to refund", "error", err)
		}
		return nil
	}

	// check if we've halted trading
	swapMsg, isSwap := m.(*MsgSwap)
	_, isAddLiquidity := m.(*MsgAddLiquidity)

	if isSwap || isAddLiquidity {
		if k.IsTradingHalt(ctx, m) || k.RagnarokInProgress(ctx) {
			if newErr := refundTx(ctx, tx, mgr, se.ErrUnauthorized.ABCICode(), "trading halted", ""); nil != newErr {
				ctx.Logger().Error("fail to refund for halted trading", "error", err)
			}
			return nil
		}
	}

	// if its a swap, send it to our queue for processing later
	if isSwap {
		if err = addSwap(ctx, mgr, *swapMsg); err != nil {
			if refundErr := refundTx(ctx, tx, mgr, CodeSwapFail, err.Error(), ""); refundErr != nil {
				ctx.Logger().Error("fail to refund swap", "error", refundErr)
				// swallow the error here
			}
		}
		return nil
	}

	// if it is a loan, inject the observed TxID and ToAddress into the context
	_, isLoanOpen := m.(*MsgLoanOpen)
	_, isLoanRepayment := m.(*MsgLoanRepayment)
	mCtx := ctx
	if isLoanOpen || isLoanRepayment {
		mCtx = ctx.WithValue(constants.CtxLoanTxID, tx.Tx.ID)
		mCtx = mCtx.WithValue(constants.CtxLoanToAddress, tx.Tx.ToAddress)
	}

	// Check and block switch assets
	// Check is independent of the mimir to enable the handler in order to support
	// bifrost & switch whitelisting prior to switching commencing
	_, isSwitch := m.(*MsgSwitch)
	if !isSwitch && mgr.SwitchManager().IsSwitch(ctx, tx.Tx.Coins[0].Asset) {
		if err = refundTx(ctx, tx, mgr, CodeTxFail, "asset is a switch asset", ""); err != nil {
			ctx.Logger().Error("fail to refund", "error", err)
		}

		return nil
	}

	_, err = handler(mCtx, m)
	if err != nil {
		if err = refundTx(ctx, tx, mgr, CodeTxFail, err.Error(), ""); err != nil {
			return fmt.Errorf("fail to refund: %w", err)
		}
		return nil
	}

	// if an outbound is not expected, mark the voter as done
	if !memo.GetType().HasOutbound() {
		// retrieve the voter from store in case the handler caused a change
		voter, err = k.GetObservedTxInVoter(ctx, tx.Tx.ID)
		if err != nil {
			return fmt.Errorf("fail to get voter")
		}
		voter.SetDone()
		k.SetObservedTxInVoter(ctx, voter)
	}

	ctx.Logger().Info("tx in processed", "chain", tx.Tx.Chain, "id", tx.Tx.ID, "finalized", tx.IsFinal())

	return nil
}

// processTxOutAttestation processes a single attestation for an observed tx.
// This is used by both MsgObservedTxOut (single attester) and MsgObservedTxOutQuorum (multiple attesters).
func processTxOutAttestation(
	ctx cosmos.Context,
	mgr Manager,
	voter ObservedTxVoter,
	nas NodeAccounts,
	tx ObservedTx,
	signer cosmos.AccAddress,
	shouldSlashForDuplicate bool,
) (ObservedTxVoter, bool) {
	k := mgr.Keeper()
	slasher := mgr.Slasher()

	observeSlashPoints := mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := k.GetConfigInt64(ctx, constants.ObservationDelayFlexibility)
	ok := false

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_txout"),
		telemetry.NewLabel("chain", string(tx.Tx.Chain)),
	}))
	slashCtx = ctx.WithContext(context.WithValue(slashCtx.Context(), constants.CtxObservedTx, tx.Tx.ID.String()))

	if err := k.SetLastObserveHeight(ctx, tx.Tx.Chain, signer, tx.BlockHeight); err != nil {
		ctx.Logger().Error("fail to save last observe height", "error", err, "signer", signer, "chain", tx.Tx.Chain)
	}

	// As an observation requires processing by all nodes no matter what,
	// any observation should increment ObserveSlashPoints,
	// to be decremented only if contributing to or within ObservationDelayFlexibility of consensus.
	slasher.IncSlashPoints(slashCtx, observeSlashPoints, signer)

	if !voter.Add(tx, signer) {
		if !shouldSlashForDuplicate {
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signer)
		}
		// A duplicate message, so do nothing further.
		return voter, ok
	}

	// Outbound memos can have | data passthrough,
	// so linked TxID extracted with memo parsing and GetTxID
	// rather than strings.Split .
	if memo, err := ParseMemoWithTHORNames(ctx, k, tx.Tx.Memo); err != nil {
		ctx.Logger().Error("fail to parse outbound memo", "error", err, "memo", tx.Tx.Memo)
	} else if inhash := memo.GetTxID(); !inhash.IsEmpty() {
		k.SetObservedLink(ctx, inhash, tx.Tx.ID)
	}

	if voter.HasFinalised(nas) {
		if voter.FinalisedHeight == 0 {
			if voter.Height == 0 {
				ok = true
				// Record the consensus height at which outbound consensus actions are taken.
				voter.Height = ctx.BlockHeight()
				if voter.UnfinalizedHeight == 0 {
					voter.UnfinalizedHeight = voter.Height // Preserve first consensus height
				}
			}
			voter.FinalisedHeight = ctx.BlockHeight()
			voter.Tx = *voter.GetTx(nas)

			ctx.Logger().Debug("tx out finalized and has consensus",
				"id", tx.Tx.ID.String(),
				"chain", tx.Tx.Chain.String(),
				"height", tx.BlockHeight,
				"from", tx.Tx.FromAddress.String(),
				"to", tx.Tx.ToAddress.String(),
				"memo", tx.Tx.Memo,
				"coins", tx.Tx.Coins.String(),
				"gas", common.Coins(tx.Tx.Gas).String(),
				"observed_vault_pubkey", tx.ObservedPubKey.String(),
			)

			// This signer brings the voter to consensus;
			// decrement all the signers' slash points and increment the non-signers' slash points.
			signers := voter.GetConsensusSigners()
			nonSigners := getNonSigners(nas, signers)
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
			slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)
		} else if ctx.BlockHeight() <= (voter.FinalisedHeight+observeFlex) &&
			voter.Tx.IsFinal() == tx.IsFinal() &&
			voter.Tx.Tx.EqualsEx(tx.Tx) &&
			!voter.Tx.HasSigned(signer) {
			// Track already-decremented slash points with the consensus Tx's Signers list.
			voter.Tx.Signers = append(voter.Tx.Signers, signer.String())
			// event the tx had been processed , given the signer just a bit late , so we still take away their slash points
			slasher.DecSlashPoints(slashCtx, observeSlashPoints+lackOfObservationPenalty, signer)
		}
	}
	if !ok && voter.HasConsensus(nas) && !tx.IsFinal() && voter.FinalisedHeight == 0 {
		if voter.Height == 0 {
			ok = true
			// Record the consensus height at which outbound consensus actions are taken,
			// even if not yet Finalised.
			voter.Height = ctx.BlockHeight()
			if voter.UnfinalizedHeight == 0 {
				voter.UnfinalizedHeight = voter.Height // Preserve first consensus height
			}
			// this is the tx that has consensus
			voter.Tx = *voter.GetTx(nas)

			// This signer brings the voter to consensus;
			// decrement all the signers' slash points and increment the non-signers' slash points.
			signers := voter.GetConsensusSigners()
			nonSigners := getNonSigners(nas, signers)
			slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
			slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)
		} else if ctx.BlockHeight() <= (voter.Height+observeFlex) &&
			voter.Tx.IsFinal() == tx.IsFinal() &&
			voter.Tx.Tx.EqualsEx(tx.Tx) &&
			!voter.Tx.HasSigned(signer) {
			// Track already-decremented slash points with the consensus Tx's Signers list.
			voter.Tx.Signers = append(voter.Tx.Signers, signer.String())
			// event the tx had been processed , given the signer just a bit late , so still take away their slash points
			// but only when the tx signer are voting is the tx that already reached consensus
			slasher.DecSlashPoints(slashCtx, observeSlashPoints+lackOfObservationPenalty, signer)
		}
	}

	k.SetObservedTxOutVoter(ctx, voter)

	// Check to see if we have enough identical observations to process the transaction
	return voter, ok
}

// ensureVaultAndGetTxOutVoter will make sure the vault exists, then get the ObservedTxOutVoter from the store.
// if it doesn't exist, it will create a new one.
func ensureVaultAndGetTxOutVoter(ctx cosmos.Context, k keeper.Keeper, vaultPubKey common.PubKey, txID common.TxID, observers []cosmos.AccAddress, keysignMs int64) (ObservedTxVoter, error) {
	// check we are sending from a valid vault
	if !k.VaultExists(ctx, vaultPubKey) {
		ctx.Logger().Info("Not valid Observed Pubkey", "observed pub key", vaultPubKey)
		return ObservedTxVoter{}, fmt.Errorf("vault not found for observed tx out pubkey: %s", vaultPubKey)
	}

	if keysignMs > 0 {
		keysignMetric, err := k.GetTssKeysignMetric(ctx, txID)
		if err != nil {
			ctx.Logger().Error("fail to get keysign metric", "error", err)
		} else {
			for _, o := range observers {
				keysignMetric.AddNodeTssTime(o, keysignMs)
			}
			k.SetTssKeysignMetric(ctx, keysignMetric)
		}
	}

	voter, err := k.GetObservedTxOutVoter(ctx, txID)
	if err != nil {
		return ObservedTxVoter{}, fmt.Errorf("fail to get tx out voter: %w", err)
	}

	return voter, nil
}

// handleObservedTxOutQuorum - will process the observed tx out quorum.
// used by both MsgObservedTxOut and MsgObservedTxOutQuorum after processing
// attestation(s).
func handleObservedTxOutQuorum(
	ctx cosmos.Context,
	mgr Manager,
	signer cosmos.AccAddress,
	activeNodeAccounts NodeAccounts,
	handler cosmos.Handler,
	tx common.ObservedTx,
	voter ObservedTxVoter,
	observers []cosmos.AccAddress,
	isQuorum bool,
) error {
	// check whether the tx has consensus
	if !isQuorum {
		if voter.Height == ctx.BlockHeight() {
			// we've already process the transaction, but we should still
			// update the observing addresses
			mgr.ObMgr().AppendObserver(tx.Tx.Chain, observers)
		}
		return nil
	}

	k := mgr.Keeper()

	if isCancelTx(tx) {
		ctx.Logger().Info("skipping slash for cancel tx with empty memo", "txid", tx.Tx.ID)
		// Credit gas to gas manager and deduct from vault (legit operational spend, no penalty)
		// This also adds to fee_spent_rune for dynamic outbound fee calculation
		if err := addGasFees(ctx, mgr, tx); err != nil {
			ctx.Logger().Error("fail to add gas fees for cancel tx", "error", err)
		}
		return nil
	}

	// if memo isn't valid or its an inbound memo, slash the vault
	memo, _ := ParseMemoWithTHORNames(ctx, k, tx.Tx.Memo)
	if memo.IsEmpty() || memo.IsInbound() {
		vault, err := k.GetVault(ctx, tx.ObservedPubKey)
		if err != nil {
			ctx.Logger().Error("fail to get vault", "error", err)
			return nil
		}
		toSlash := make(common.Coins, len(tx.Tx.Coins))
		copy(toSlash, tx.Tx.Coins)
		toSlash = toSlash.Add(tx.Tx.Gas.ToCoins()...)

		slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
			telemetry.NewLabel("reason", "sent_extra_funds"),
			telemetry.NewLabel("chain", string(tx.Tx.Chain)),
		}))

		if err := mgr.Slasher().SlashVault(slashCtx, tx.ObservedPubKey, toSlash, mgr); err != nil {
			ctx.Logger().Error("fail to slash account for sending extra fund", "error", err)
		}
		vault.SubFunds(toSlash)
		if err := k.SetVault(ctx, vault); err != nil {
			ctx.Logger().Error("fail to save vault", "error", err)
		}

		return nil
	}

	txOut := voter.GetTx(activeNodeAccounts) // get consensus tx, in case our for loop is incorrect
	txOut.Tx.Memo = tx.Tx.Memo
	m, err := processOneTxIn(ctx, k, *txOut, signer)
	if err != nil || tx.Tx.Chain.IsEmpty() {
		ctx.Logger().Error("fail to process txOut",
			"error", err,
			"tx", tx.Tx.String())
		return nil
	}

	// add addresses to observing addresses. This is used to detect
	// active/inactive observing node accounts
	mgr.ObMgr().AppendObserver(tx.Tx.Chain, txOut.GetSigners())

	// emit tss keysign metrics
	if tx.KeysignMs > 0 {
		var keysignMetric *keeper.TssKeysignMetric
		keysignMetric, err = k.GetTssKeysignMetric(ctx, tx.Tx.ID)
		if err != nil {
			ctx.Logger().Error("fail to get tss keysign metric", "error", err, "hash", tx.Tx.ID)
		} else {
			evt := NewEventTssKeysignMetric(keysignMetric.TxID, keysignMetric.GetMedianTime())
			if err = mgr.EventMgr().EmitEvent(ctx, evt); err != nil {
				ctx.Logger().Error("fail to emit tss metric event", "error", err)
			}
		}
	}
	var res *cosmos.Result
	res, err = handler(ctx, m)
	if err != nil {
		ctx.Logger().Error("handler failed:", "error", err)
		return nil
	}

	vaultSlash := false
	if res != nil && res.Events != nil {
		for _, ev := range res.Events {
			if ev.Type == "vault-slash" {
				vaultSlash = true
				break
			}
		}
	}

	voter.SetDone()
	k.SetObservedTxOutVoter(ctx, voter)

	// only deduct gas fee via the manager if there was not a vault slash that covered it
	if !vaultSlash {
		if err = addGasFees(ctx, mgr, tx); err != nil {
			ctx.Logger().Error("fail to add gas fee", "error", err)
			return nil
		}
	}

	// process the msg first , and then deduct the fund from vault last
	// If sending from one of our vaults, decrement coins
	vault, err := k.GetVault(ctx, tx.ObservedPubKey)
	if err != nil {
		ctx.Logger().Error("fail to get vault", "error", err)
		return nil
	}

	// if the vault was slashed we skipped the gas manager above and deduct gas directly
	if vaultSlash {
		vault.SubFunds(tx.Tx.Gas.ToCoins())
	}

	// Don't add to or subtract from vault balances when the sender and recipient are the same
	// (particularly avoid Consolidate SafeSub zeroing of vault balances).
	if !tx.Tx.FromAddress.Equals(tx.Tx.ToAddress) {
		// skip deducting funds for outbound fake gas
		if !isOutboundFakeGasTx(tx) {
			vault.SubFunds(tx.Tx.Coins)
		}
		vault.OutboundTxCount++
	}

	if vault.IsAsgard() && memo.IsType(TxMigrate) {
		// only remove the block height that had been specified in the memo
		vault.RemovePendingTxBlockHeights(memo.GetBlockHeight())
	}

	if !vault.HasFunds() && vault.Status == RetiringVault {
		// we have successfully removed all funds from a retiring vault,
		// mark it as inactive
		vault.UpdateStatus(InactiveVault, ctx.BlockHeight())
	}
	// if the vault is frozen, then unfreeze it. Since we saw that a
	// transaction was signed
	for _, coin := range tx.Tx.Coins {
		for i := range vault.Frozen {
			if strings.EqualFold(coin.Asset.GetChain().String(), vault.Frozen[i]) {
				vault.Frozen = append(vault.Frozen[:i], vault.Frozen[i+1:]...)
				break
			}
		}
	}
	if err := k.SetVault(ctx, vault); err != nil {
		ctx.Logger().Error("fail to save vault", "error", err)
		return nil
	}

	ctx.Logger().Info("tx out processed", "chain", tx.Tx.Chain, "id", tx.Tx.ID, "finalized", tx.IsFinal())

	return nil
}

// fetchMemoFromReference fetches memo from reference if it's a memoless transaction
func fetchMemoFromReference(ctx cosmos.Context, mgr Manager, asset common.Asset, tx common.Tx, txObservationHeight int64) string {
	mem, err := ParseMemoWithTHORNames(ctx, mgr.Keeper(), tx.Memo)
	if err != nil {
		ctx.Logger().Debug("failed to parse memo in fetchMemoFromReference", "error", err, "memo", tx.Memo, "txid", tx.ID)
	}
	if mem.GetType() == TxReferenceReadMemo {
		m, ok := mem.(ReferenceReadMemo)
		if !ok {
			return tx.Memo
		}
		// Check if memoless transactions are halted
		haltMemoless, err := mgr.Keeper().GetMimir(ctx, constants.HaltMemoless.String())
		if err == nil && haltMemoless > 0 {
			return ""
		}

		refMemo, err := mgr.Keeper().GetReferenceMemo(ctx, asset, m.Reference)
		if err != nil {
			ctx.Logger().Error("unable to fetch pool for ref memo lookup", "error", err, "asset", asset)
			return ""
		}

		// Ensure transaction was observed after the reference memo was created
		if txObservationHeight <= refMemo.Height {
			ctx.Logger().Info("transaction observed before reference memo creation", "tx_height", txObservationHeight, "memo_height", refMemo.Height, "reference", m.Reference)
			// Track failure usage
			trackReferenceMemoUsage(ctx, mgr, asset, tx.Memo, tx.ID)
			return ""
		}

		ttl := mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnTTL)
		if !refMemo.IsExpired(ctx.BlockHeight(), ttl) {
			// Check usage limit
			maxUse := mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnMaxUse)
			if maxUse > 0 && refMemo.GetUsageCount() >= maxUse {
				ctx.Logger().Info("reference memo usage limit exceeded in fetchMemoFromReference", "reference", m.Reference, "usage_count", refMemo.GetUsageCount(), "max_use", maxUse)

				// Track failure usage
				trackReferenceMemoUsage(ctx, mgr, asset, tx.Memo, tx.ID)
				return ""
			}

			// Track successful usage
			trackReferenceMemoUsage(ctx, mgr, asset, tx.Memo, tx.ID)
			return refMemo.Memo
		}

		// Track failure usage
		trackReferenceMemoUsage(ctx, mgr, asset, tx.Memo, tx.ID)
		return ""
	}
	return tx.Memo
}

// trackReferenceMemoUsage tracks that a transaction has used a reference memo
func trackReferenceMemoUsage(ctx cosmos.Context, mgr Manager, asset common.Asset, memo string, txID common.TxID) {
	mem, _ := ParseMemo(mgr.GetVersion(), memo) // ignore err
	if mem.GetType() == TxReferenceReadMemo {
		m, ok := mem.(ReferenceReadMemo)
		if !ok {
			return
		}

		refMemo, err := mgr.Keeper().GetReferenceMemo(ctx, asset, m.Reference)
		if err != nil {
			ctx.Logger().Error("fail to get reference memo for usage tracking", "error", err)
			return
		}

		// Check if already used by this transaction
		if refMemo.HasBeenUsedBy(txID) {
			// Already tracked, nothing to do
			return
		}

		// Add this transaction to the usage list (with duplicate protection)
		if refMemo.AddUsage(txID) {
			mgr.Keeper().SetReferenceMemo(ctx, refMemo)
			maxUse := mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnMaxUse)
			ctx.Logger().Info("reference memo usage tracked", "reference", m.Reference, "usage_count", refMemo.GetUsageCount(), "max_use", maxUse, "txid", txID, "asset", asset, "memo", refMemo.Memo)
		}
	}
}

func generateReferenceMemoID(ctx cosmos.Context, mgr Manager, asset common.Asset, tx common.ObservedTx) (string, error) {
	if len(tx.Tx.Coins) == 0 {
		return "", fmt.Errorf("no coins in transaction for reference generation")
	}

	amount := tx.Tx.Coins[0].Amount.Uint64()
	return ExtractReferenceFromAmount(ctx, mgr, asset, amount)
}

// ExtractReferenceFromAmount extracts the reference ID from a transaction amount.
// This is the core logic shared by both the handler (generateReferenceMemoID)
// and the querier (queryReferenceMemoPreflight).
func ExtractReferenceFromAmount(ctx cosmos.Context, mgr Manager, asset common.Asset, amount uint64) (string, error) {
	if asset.IsEmpty() {
		return "", fmt.Errorf("asset is empty for reference generation")
	}

	if amount == 0 {
		return "", fmt.Errorf("zero amount in transaction for reference generation")
	}

	var decimals int64
	if asset.IsGasAsset() {
		decimals = asset.Chain.GetGasAssetDecimal()
	} else {
		pool, err := mgr.Keeper().GetPool(ctx, asset)
		if err != nil {
			ctx.Logger().Error("unable to fetch pool for ref memo", "error", err)
			return "", err
		}
		decimals = pool.Decimals
	}

	baseEnd := mgr.Keeper().GetConfigInt64(ctx, constants.MemolessTxnRefCount)
	txnRefLength := len(fmt.Sprintf("%d", baseEnd))

	// Prevent overflow: uint64 max is ~1.8e19, so max safe length is 19 digits
	const maxRefLength = 19
	if txnRefLength > maxRefLength {
		return "", fmt.Errorf("reference length %d exceeds maximum %d to prevent overflow", txnRefLength, maxRefLength)
	}

	// Calculate modulus based on reference length (10^txnRefLength)
	var modulus uint64 = 1
	if baseEnd > 0 {
		for i := 0; i < txnRefLength; i++ {
			modulus *= 10
		}
	}

	// Normalize amount to THORChain decimals.
	if decimals < int64(common.THORChainDecimals) {
		divisor := int64(1)
		for i := decimals; i < int64(common.THORChainDecimals); i++ {
			divisor *= 10
		}
		amount /= uint64(divisor)
	}

	// Extract reference from the amount.
	refNum := amount % modulus

	// Zero references are not allowed as they would create ambiguous reference IDs.
	// This occurs when the amount (after decimal normalization) is exactly divisible
	// by the modulus (e.g., 50000000 sat with modulus 100 → ref 0).
	// Users should adjust their amount slightly to avoid this edge case.
	if refNum == 0 {
		return "", fmt.Errorf("zero reference from amount is invalid: amount %d is divisible by modulus %d", amount, modulus)
	}

	return leadingZeros(txnRefLength, fmt.Sprintf("%d", refNum)), nil
}
