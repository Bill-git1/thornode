package thorchain

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

func processSolvencyAttestation(
	ctx cosmos.Context,
	mgr Manager,
	voter *keeper.SolvencyVoter,
	attester cosmos.AccAddress,
	active NodeAccounts,
	s *common.Solvency,
	shouldSlashForDuplicate bool,
) error {
	k := mgr.Keeper()

	observeSlashPoints := mgr.GetConstants().GetInt64Value(constants.ObserveSlashPoints)
	lackOfObservationPenalty := mgr.GetConstants().GetInt64Value(constants.LackOfObservationPenalty)
	observeFlex := k.GetConfigInt64(ctx, constants.ObservationDelayFlexibility)

	slashCtx := ctx.WithContext(context.WithValue(ctx.Context(), constants.CtxMetricLabels, []metrics.Label{
		telemetry.NewLabel("reason", "failed_observe_solvency"),
		telemetry.NewLabel("chain", string(s.Chain)),
	}))

	slasher := mgr.Slasher()

	if voter.Empty() {
		*voter = NewSolvencyVoter(s.Id, s.Chain, s.PubKey, s.Coins, s.Height, attester)
	} else if !voter.Sign(attester) {
		// Slash for the network having to handle the extra message/s.
		if shouldSlashForDuplicate {
			slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
		}
		ctx.Logger().Info("signer already signed MsgSolvency", "signer", attester.String(), "id", s.Id)
		return nil
	}
	k.SetSolvencyVoter(ctx, *voter)

	if !voter.HasConsensus(active) {
		// Before consensus, slash until consensus.
		slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
		return nil
	}

	// from this point , solvency reach consensus
	if voter.ConsensusBlockHeight > 0 {
		// After consensus, only decrement slash points if within the ObservationDelayFlexibility period.
		if (voter.ConsensusBlockHeight + observeFlex) >= ctx.BlockHeight() {
			slasher.DecSlashPoints(slashCtx, lackOfObservationPenalty, attester)
		}
		// solvency tx already processed
		return nil
	}
	voter.ConsensusBlockHeight = ctx.BlockHeight()

	// This signer brings the voter to consensus; increment the signer's slash points like the before-consensus signers,
	// then decrement all the signers' slash points and increment the non-signers' slash points.
	slasher.IncSlashPoints(slashCtx, observeSlashPoints, attester)
	signers := voter.GetSigners()
	nonSigners := getNonSigners(active, signers)
	slasher.DecSlashPoints(slashCtx, observeSlashPoints, signers...)
	slasher.IncSlashPoints(slashCtx, lackOfObservationPenalty, nonSigners...)

	vault, err := k.GetVault(ctx, voter.PubKey)
	if err != nil {
		ctx.Logger().Error("fail to get vault", "error", err)
		return fmt.Errorf("fail to get vault: %w", err)
	}

	// Do checks for whether to act on this consensus or not.
	const StopSolvencyCheckKey = `StopSolvencyCheck`
	stopSolvencyCheck, err := k.GetMimir(ctx, StopSolvencyCheckKey)
	if err != nil {
		ctx.Logger().Error("fail to get mimir", "key", StopSolvencyCheckKey, "error", err)
	}
	if stopSolvencyCheck > 0 && stopSolvencyCheck < ctx.BlockHeight() {
		return nil
	}
	// stop solvency checker can be chain specific
	stopSolvencyCheckChain, err := k.GetMimir(ctx, fmt.Sprintf("%s%s", StopSolvencyCheckKey, voter.Chain.String()))
	if err != nil {
		ctx.Logger().Error("fail to get mimir", "key", StopSolvencyCheckKey+voter.Chain.String(), "error", err)
	}
	if stopSolvencyCheckChain > 0 && stopSolvencyCheckChain < ctx.BlockHeight() {
		return nil
	}
	haltChainKey := fmt.Sprintf(`SolvencyHalt%sChain`, voter.Chain)
	haltChain, err := k.GetMimir(ctx, haltChainKey)
	if err != nil {
		ctx.Logger().Error("fail to get mimir", "error", err)
	}
	// If the chain was halted this block, leave it halted without overriding.
	// (For instance if halted because of a different vault which is insolvent.)
	// Also don't unhalt if the chain was manually halted for a future height
	// or indefinitely ('1').
	if haltChain >= ctx.BlockHeight() || haltChain == 1 {
		return nil
	}
	// If the solvency message is from a height which does not reflect inbounds
	// reflected in the supermajority-observation vault balances,
	// do not act on it.
	lastChainHeight, err := k.GetLastChainHeight(ctx, voter.Chain)
	if err != nil {
		ctx.Logger().Error("fail to get last chain height", "chain", voter.Chain, "error", err)
	}
	// According to the validate msg.Id check, the Height is consistent for all the voter's messages.
	if s.Height < lastChainHeight {
		ctx.Logger().Info("solvency message consensus for height before last chain height inbound supermajority observation", "chain", voter.Chain, "vault pubkey", voter.PubKey, "last chain height", lastChainHeight, "solvency message height", s.Height)
		return nil
	}

	isInsolvent := insolvencyCheck(ctx, mgr, vault, voter.Coins, voter.Chain)

	// If insolvent and already halted, leave the Mimir key unchanged as a record of since when it's been insolvent.
	// If insolvent and unhalted, halt the chain.
	if isInsolvent && haltChain <= 0 {
		k.SetMimir(ctx, haltChainKey, ctx.BlockHeight())
		mimirEvent := NewEventSetMimir(strings.ToUpper(haltChainKey), strconv.FormatInt(ctx.BlockHeight(), 10))
		if err := mgr.EventMgr().EmitEvent(ctx, mimirEvent); err != nil {
			ctx.Logger().Error("fail to emit set_mimir event", "error", err)
		}
		ctx.Logger().Info("chain is insolvent, halt until it is resolved", "chain", voter.Chain)
	}

	// If not insolvent and the chain is halted from an earlier block height, unhalt the chain.
	// Even if a different vault is still insolvent, it can re-halt the chain in this or a later block.
	// (An alternative approach would be for if an insolvent vault always updated a lower-height Mimir key to the current height.)
	if !isInsolvent && haltChain > 1 {
		// if the chain was halted by previous solvency checker, auto unhalt it
		ctx.Logger().Info("auto un-halt", "chain", voter.Chain, "previous halt height", haltChain, "current block height", ctx.BlockHeight())
		k.SetMimir(ctx, haltChainKey, 0)
		mimirEvent := NewEventSetMimir(strings.ToUpper(haltChainKey), "0")
		if err := mgr.EventMgr().EmitEvent(ctx, mimirEvent); err != nil {
			ctx.Logger().Error("fail to emit set_mimir event", "error", err)
		}
	}

	return nil
}

// insolvencyCheck compare the coins in vault against the coins report by solvency message
// insolvent usually means vault has more coins than wallet
// return true means the vault is insolvent , the network should halt , otherwise false
func insolvencyCheck(ctx cosmos.Context, mgr Manager, vault Vault, coins common.Coins, chain common.Chain) bool {
	adjustVault, err := excludePendingOutboundFromVault(ctx, mgr, vault)
	if err != nil {
		return false
	}
	permittedSolvencyGap, err := mgr.Keeper().GetMimir(ctx, constants.PermittedSolvencyGap.String())
	if err != nil || permittedSolvencyGap <= 0 {
		permittedSolvencyGap = mgr.GetConstants().GetInt64Value(constants.PermittedSolvencyGap)
	}
	// Use the coin in vault as baseline , wallet can have more coins than vault
	for _, c := range adjustVault.Coins {
		if !c.Asset.Chain.Equals(chain) {
			continue
		}
		if c.IsEmpty() {
			continue
		}
		walletCoin := coins.GetCoin(c.Asset)
		if walletCoin.IsEmpty() {
			ctx.Logger().Info("asset exist in vault , but not in wallet, insolvent", "asset", c.Asset.String(), "amount", c.Amount.String())
			return true
		}
		if c.Asset.IsGasAsset() {
			gas, err := mgr.GasMgr().GetMaxGas(ctx, c.Asset.GetChain())
			if err != nil {
				ctx.Logger().Error("fail to get max gas", "error", err)
			} else if c.Amount.LTE(gas.Amount.MulUint64(10)) {
				// if the amount left in asgard vault is not enough for 10 * max gas, then skip it from solvency check
				continue
			}
		}

		if c.Amount.GT(walletCoin.Amount) {
			gap := c.Amount.Sub(walletCoin.Amount)
			permittedGap := walletCoin.Amount.MulUint64(uint64(permittedSolvencyGap)).QuoUint64(10000)
			if gap.GT(permittedGap) {
				ctx.Logger().Info("vault has more asset than wallet, insolvent", "asset", c.Asset.String(), "vault amount", c.Amount.String(), "wallet amount", walletCoin.Amount.String(), "gap", gap.String())
				return true
			}
		}
	}
	return false
}

func excludePendingOutboundFromVault(ctx cosmos.Context, mgr Manager, vault Vault) (Vault, error) {
	// go back SigningTransactionPeriod blocks to see whether there are outstanding tx, the vault need to send out
	// if there is , deduct it from their balance
	signingPeriod := mgr.GetConstants().GetInt64Value(constants.SigningTransactionPeriod)
	startHeight := ctx.BlockHeight() - signingPeriod
	if startHeight < 1 {
		startHeight = 1
	}
	for i := startHeight; i < ctx.BlockHeight(); i++ {
		blockOut, err := mgr.Keeper().GetTxOut(ctx, i)
		if err != nil {
			ctx.Logger().Error("fail to get block tx out", "error", err)
			return vault, fmt.Errorf("fail to get block tx out, err: %w", err)
		}
		vault = deductVaultBlockPendingOutbound(vault, blockOut)
	}
	return vault, nil
}

func deductVaultBlockPendingOutbound(vault Vault, block *TxOut) Vault {
	for _, txOutItem := range block.TxArray {
		if !txOutItem.VaultPubKey.Equals(vault.PubKey) {
			continue
		}
		// only still outstanding txout will be considered
		if !txOutItem.OutHash.IsEmpty() {
			continue
		}
		// deduct the gas asset from the vault as well
		var gasCoin common.Coin
		if !txOutItem.MaxGas.IsEmpty() {
			gasCoin = txOutItem.MaxGas.ToCoins().GetCoin(txOutItem.Chain.GetGasAsset())
		}
		for i, vaultCoin := range vault.Coins {
			if vaultCoin.Asset.Equals(txOutItem.Coin.Asset) {
				vault.Coins[i].Amount = common.SafeSub(vault.Coins[i].Amount, txOutItem.Coin.Amount)
			}
			if vaultCoin.Asset.Equals(gasCoin.Asset) {
				vault.Coins[i].Amount = common.SafeSub(vault.Coins[i].Amount, gasCoin.Amount)
			}
		}
	}
	return vault
}
