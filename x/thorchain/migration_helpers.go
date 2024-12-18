package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// When an ObservedTxInVoter has dangling Actions items swallowed by the vaults, requeue
// them. This can happen when a TX has multiple outbounds scheduled and one of them is
// erroneously scheduled for the past.
func requeueDanglingActions(ctx cosmos.Context, mgr *Mgrs, txIDs []common.TxID) {
	// Select the least secure ActiveVault Asgard for all outbounds.
	// Even if it fails (as in if the version changed upon the keygens-complete block of a churn),
	// updating the voter's FinalisedHeight allows another MaxOutboundAttempts for LackSigning vault selection.
	activeAsgards, err := mgr.Keeper().GetAsgardVaultsByStatus(ctx, types.VaultStatus_ActiveVault)
	if err != nil || len(activeAsgards) == 0 {
		ctx.Logger().Error("fail to get active asgard vaults", "error", err)
		return
	}
	if len(activeAsgards) > 1 {
		signingTransactionPeriod := mgr.GetConstants().GetInt64Value(constants.SigningTransactionPeriod)
		activeAsgards = mgr.Keeper().SortBySecurity(ctx, activeAsgards, signingTransactionPeriod)
	}
	vaultPubKey := activeAsgards[0].PubKey

	for _, txID := range txIDs {
		voter, err := mgr.Keeper().GetObservedTxInVoter(ctx, txID)
		if err != nil {
			ctx.Logger().Error("fail to get observed tx voter", "error", err)
			continue
		}

		if len(voter.OutTxs) >= len(voter.Actions) {
			log := fmt.Sprintf("(%d) OutTxs present for (%s), despite expecting fewer than the (%d) Actions.", len(voter.OutTxs), txID.String(), len(voter.Actions))
			ctx.Logger().Debug(log)
			continue
		}

		var indices []int
		for i := range voter.Actions {
			if isActionsItemDangling(voter, i) {
				indices = append(indices, i)
			}
		}
		if len(indices) == 0 {
			log := fmt.Sprintf("No dangling Actions item found for (%s).", txID.String())
			ctx.Logger().Debug(log)
			continue
		}

		if len(voter.Actions)-len(voter.OutTxs) != len(indices) {
			log := fmt.Sprintf("(%d) Actions and (%d) OutTxs present for (%s), yet there appeared to be (%d) dangling Actions.", len(voter.Actions), len(voter.OutTxs), txID.String(), len(indices))
			ctx.Logger().Debug(log)
			continue
		}

		height := ctx.BlockHeight()

		// Update the voter's FinalisedHeight to give another MaxOutboundAttempts.
		voter.FinalisedHeight = height

		for _, index := range indices {
			// Use a pointer to update the voter as well.
			actionItem := &voter.Actions[index]

			// Update the vault pubkey.
			actionItem.VaultPubKey = vaultPubKey

			// Update the Actions item's MaxGas and GasRate.
			// Note that nothing in this function should require a GasManager BeginBlock.
			gasCoin, err := mgr.GasMgr().GetMaxGas(ctx, actionItem.Chain)
			if err != nil {
				ctx.Logger().Error("fail to get max gas", "chain", actionItem.Chain, "error", err)
				continue
			}
			actionItem.MaxGas = common.Gas{gasCoin}
			actionItem.GasRate = int64(mgr.GasMgr().GetGasRate(ctx, actionItem.Chain).Uint64())

			// UnSafeAddTxOutItem is used to queue the txout item directly, without for instance deducting another fee.
			err = mgr.TxOutStore().UnSafeAddTxOutItem(ctx, mgr, *actionItem, height)
			if err != nil {
				ctx.Logger().Error("fail to add outbound tx", "error", err)
				continue
			}
		}

		// Having requeued all dangling Actions items, set the updated voter.
		mgr.Keeper().SetObservedTxInVoter(ctx, voter)
	}
}
