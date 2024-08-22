package thorchain

import (
	"context"
	"fmt"

	"github.com/armon/go-metrics"
	"github.com/blang/semver"
	"github.com/cosmos/cosmos-sdk/telemetry"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

// ErrataTxHandler is to handle ErrataTx message
type ErrataTxHandler struct {
	mgr Manager
}

// NewErrataTxHandler create new instance of ErrataTxHandler
func NewErrataTxHandler(mgr Manager) ErrataTxHandler {
	return ErrataTxHandler{
		mgr: mgr,
	}
}

// Run is the main entry point to execute ErrataTx logic
func (h ErrataTxHandler) Run(ctx cosmos.Context, m cosmos.Msg) (*cosmos.Result, error) {
	msg, ok := m.(*MsgErrataTx)
	if !ok {
		return nil, errInvalidMessage
	}
	if err := h.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("msg errata tx failed validation", "error", err)
		return nil, err
	}
	return h.handle(ctx, *msg)
}

func (h ErrataTxHandler) validate(ctx cosmos.Context, msg MsgErrataTx) error {
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("0.1.0")):
		return h.validateV1(ctx, msg)
	default:
		return errBadVersion
	}
}

func (h ErrataTxHandler) validateV1(ctx cosmos.Context, msg MsgErrataTx) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	if !isSignedByActiveNodeAccounts(ctx, h.mgr.Keeper(), msg.GetSigners()) {
		return cosmos.ErrUnauthorized(errNotAuthorized.Error())
	}

	return nil
}

func (h ErrataTxHandler) handle(ctx cosmos.Context, msg MsgErrataTx) (*cosmos.Result, error) {
	ctx.Logger().Info("handleMsgErrataTx request", "txid", msg.TxID.String())
	version := h.mgr.GetVersion()
	switch {
	case version.GTE(semver.MustParse("1.134.0")):
		return h.handleV134(ctx, msg)
	default:
		ctx.Logger().Error(errInvalidVersion.Error())
		return nil, errBadVersion
	}
}

func (h ErrataTxHandler) handleV134(ctx cosmos.Context, msg MsgErrataTx) (*cosmos.Result, error) {
	active, err := h.mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		return nil, wrapError(ctx, err, "fail to get list of active node accounts")
	}

	voter, err := h.mgr.Keeper().GetErrataTxVoter(ctx, msg.TxID, msg.Chain)
	if err != nil {
		return nil, err
	}
	observeSlashPoints := h.mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := h.mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := h.mgr.Keeper().GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{ // nolint
		telemetry.NewLabel("reason", "failed_observe_errata"),
		telemetry.NewLabel("chain", string(msg.Chain)),
	}))

	if !voter.Sign(msg.Signer) {
		// Slash for the network having to handle the extra message/s.
		h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		ctx.Logger().Info("signer already signed MsgErrataTx", "signer", msg.Signer.String(), "txid", msg.TxID)
		return &cosmos.Result{}, nil
	}
	h.mgr.Keeper().SetErrataTxVoter(ctx, voter)
	// doesn't have consensus yet
	if !voter.HasConsensus(active) {
		// Before consensus, slash until consensus.
		h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
		ctx.Logger().Info("not having consensus yet, return")
		return &cosmos.Result{}, nil
	}

	if voter.BlockHeight > 0 {
		// After consensus, only decrement slash points if within the ObservationDelayFlexibility period.
		if (voter.BlockHeight + observeFlex) >= ctx.BlockHeight() {
			h.mgr.Slasher().DecSlashPoints(slashCtx, lackOfObservationPenalty, msg.Signer)
		}
		// errata tx already processed
		return &cosmos.Result{}, nil
	}

	voter.BlockHeight = ctx.BlockHeight()
	h.mgr.Keeper().SetErrataTxVoter(ctx, voter)

	// This signer brings the voter to consensus; increment the signer's slash points like the before-consensus signers,
	// then decrement all the signers' slash points and increment the non-signers' slash points.
	h.mgr.Slasher().IncSlashPoints(slashCtx, observeSlashPoints, msg.Signer)
	signers := voter.GetSigners()
	nonSigners := getNonSigners(active, signers)
	h.mgr.Slasher().DecSlashPoints(slashCtx, observeSlashPoints, signers...)
	h.mgr.Slasher().IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)

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
			// trunk-ignore(golangci-lint/govet): shadow
			vault, err := h.mgr.Keeper().GetVault(ctx, vaultPubKey)
			if err != nil {
				return nil, fmt.Errorf("fail to get active asgard vaults: %w", err)
			}
			vault.SubFunds(tx.Coins)
			// trunk-ignore(golangci-lint/govet): shadow
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
		if coin.IsRune() {
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
		// trunk-ignore(golangci-lint/govet): shadow
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

	// trunk-ignore(golangci-lint/govet): shadow
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

// processErrataOutboundTx when the network detect an outbound tx which previously had been sent out to customer , however it get re-org , and it doesn't
// exist on the external chain anymore , then it will need to reschedule the tx
func (h ErrataTxHandler) processErrataOutboundTx(ctx cosmos.Context, msg MsgErrataTx) (*cosmos.Result, error) {
	txOutVoter, err := h.mgr.Keeper().GetObservedTxOutVoter(ctx, msg.GetTxID())
	if err != nil {
		return nil, fmt.Errorf("fail to get observed tx out voter for tx (%s) : %w", msg.GetTxID(), err)
	}
	if len(txOutVoter.Txs) == 0 {
		return nil, fmt.Errorf("cannot find tx: %s", msg.TxID)
	}
	if txOutVoter.Tx.IsEmpty() {
		return nil, fmt.Errorf("tx out voter is not finalised")
	}
	tx := txOutVoter.Tx.Tx
	if !tx.Chain.Equals(msg.Chain) || tx.Coins.IsEmpty() {
		return &cosmos.Result{}, nil
	}
	// parse the outbound tx memo, so we can figure out which inbound tx triggered the outbound
	m, err := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), tx.Memo)
	if err != nil {
		return nil, fmt.Errorf("fail to parse memo(%s): %w", tx.Memo, err)
	}
	if !m.IsOutbound() && !m.IsInternal() {
		return nil, fmt.Errorf("%s is not outbound or internal tx", m)
	}
	vaultPubKey := txOutVoter.Tx.ObservedPubKey
	if !vaultPubKey.IsEmpty() {
		// trunk-ignore(golangci-lint/govet): shadow
		v, err := h.mgr.Keeper().GetVault(ctx, vaultPubKey)
		if err != nil {
			return nil, fmt.Errorf("fail to get vault with pubkey %s: %w", vaultPubKey, err)
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
			if err := h.mgr.Keeper().SetVault(ctx, v); err != nil {
				return nil, fmt.Errorf("fail to save vault: %w", err)
			}
		}
		if compensate {
			for _, coin := range tx.Coins {
				if coin.IsRune() {
					// it is using native rune, so outbound can't be RUNE
					continue
				}
				// trunk-ignore(golangci-lint/govet): shadow
				p, err := h.mgr.Keeper().GetPool(ctx, coin.Asset)
				if err != nil {
					return nil, fmt.Errorf("fail to get pool(%s): %w", coin.Asset, err)
				}
				runeValue := p.AssetValueInRune(coin.Amount)
				p.BalanceRune = p.BalanceRune.Add(runeValue)
				p.BalanceAsset = common.SafeSub(p.BalanceAsset, coin.Amount)
				// trunk-ignore(golangci-lint/govet): shadow
				if err := h.mgr.Keeper().SendFromModuleToModule(ctx, ReserveName, AsgardName, common.Coins{
					common.NewCoin(common.RuneAsset(), runeValue),
				}); err != nil {
					return nil, fmt.Errorf("fail to send fund from reserve to asgard: %w", err)
				}
				// trunk-ignore(golangci-lint/govet): shadow
				if err := h.mgr.Keeper().SetPool(ctx, p); err != nil {
					return nil, fmt.Errorf("fail to save pool (%s) : %w", p.Asset, err)
				}
				// send errata event
				mods := PoolMods{
					NewPoolMod(p.Asset, runeValue, true, coin.Amount, false),
				}

				eventErrata := NewEventErrata(msg.TxID, mods)
				// trunk-ignore(golangci-lint/govet): shadow
				if err := h.mgr.EventMgr().EmitEvent(ctx, eventErrata); err != nil {
					return nil, ErrInternal(err, "fail to emit errata event")
				}
			}
		}
	}

	// emit security event
	event := NewEventSecurity(tx, "outbound errata")
	// trunk-ignore(golangci-lint/govet): shadow
	if err := h.mgr.EventMgr().EmitEvent(ctx, event); err != nil {
		return nil, ErrInternal(err, "fail to emit security event")
	}

	txOutVoter.SetReverted()
	h.mgr.Keeper().SetObservedTxOutVoter(ctx, txOutVoter)
	return &cosmos.Result{}, nil
}

// ErrataTxAnteHandler called by the ante handler to gate mempool entry
// and also during deliver. Store changes will persist if this function
// succeeds, regardless of the success of the transaction.
func ErrataTxAnteHandler(ctx cosmos.Context, v semver.Version, k keeper.Keeper, msg MsgErrataTx) error {
	if !isSignedByActiveNodeAccounts(ctx, k, msg.GetSigners()) {
		return cosmos.ErrUnauthorized(errNotAuthorized.Error())
	}
	return nil
}
