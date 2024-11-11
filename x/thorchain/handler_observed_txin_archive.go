package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

// addSwapDirect adds the swap directly to the swap queue (no order book) - segmented
// out into its own function to allow easier maintenance of original behavior vs order
// book behavior.
func (h ObservedTxInHandler) addSwapDirectV136(ctx cosmos.Context, msg MsgSwap) {
	if msg.Tx.Coins.IsEmpty() {
		return
	}
	amt := cosmos.ZeroUint()

	// Check if affiliate fee should be paid out
	if !msg.AffiliateBasisPoints.IsZero() && msg.AffiliateAddress.IsChain(common.THORChain) {
		amt = common.GetSafeShare(
			msg.AffiliateBasisPoints,
			cosmos.NewUint(10000),
			msg.Tx.Coins[0].Amount,
		)
		msg.Tx.Coins[0].Amount = common.SafeSub(msg.Tx.Coins[0].Amount, amt)
	}

	// Queue the main swap
	if err := h.mgr.Keeper().SetSwapQueueItem(ctx, msg, 0); err != nil {
		ctx.Logger().Error("fail to add swap to queue", "error", err)
	}

	// Affiliate fee flow
	if !amt.IsZero() {
		affiliateSwap := NewMsgSwap(
			msg.Tx,
			common.RuneAsset(),
			msg.AffiliateAddress,
			cosmos.ZeroUint(),
			common.NoAddress,
			cosmos.ZeroUint(),
			"",
			"", nil,
			MarketOrder,
			0, 0,
			msg.Signer,
		)

		var affThorname *types.THORName
		memo, err := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), msg.Tx.Memo)
		if err != nil {
			ctx.Logger().Error("fail to parse swap memo", "memo", msg.Tx.Memo, "error", err)
		} else {
			affThorname = memo.GetAffiliateTHORName()
		}

		// PreferredAsset set, swap to the AffiliateCollector Module + check if the
		// preferred asset swap should be triggered
		if affThorname != nil && !affThorname.PreferredAsset.IsEmpty() {
			// trunk-ignore(golangci-lint/govet): shadow
			affcol, err := h.mgr.Keeper().GetAffiliateCollector(ctx, affThorname.Owner)
			if err != nil {
				ctx.Logger().Error("failed to get affiliate collector for thorname", "thorname", affThorname.Name, "error", err)
				return
			}

			affColAddress, err := h.mgr.Keeper().GetModuleAddress(AffiliateCollectorName)
			if err != nil {
				ctx.Logger().Error("failed to retrieve the affiliate collector module address", "error", err)
				return
			}

			// Set AffiliateCollector Module as destination and populate the AffiliateAddress
			// so that the swap handler can increment the emitted RUNE for the affiliate in
			// the AffiliateCollector KVStore.
			affiliateSwap.Destination = affColAddress
			affiliateSwap.AffiliateAddress = msg.AffiliateAddress

			// Check if accrued RUNE is 100x current outbound fee of preferred asset chain, if
			// so trigger the preferred asset swap
			ofRune, err := h.mgr.GasMgr().GetAssetOutboundFee(ctx, affThorname.PreferredAsset, true)
			if err != nil {
				ctx.Logger().Error("failed to get outbound fee for preferred asset, skipping preferred asset swap", "name", affThorname.Name, "asset", affThorname.PreferredAsset, "error", err)
			}
			multiplier := h.mgr.Keeper().GetConfigInt64(ctx, constants.PreferredAssetOutboundFeeMultiplier)
			threshold := ofRune.Mul(cosmos.NewUint(uint64(multiplier)))
			if err == nil && affcol.RuneAmount.GT(threshold) {
				if err = triggerPreferredAssetSwap(ctx, h.mgr, msg.AffiliateAddress, msg.Tx.ID, *affThorname, affcol, 2); err != nil {
					ctx.Logger().Error("fail to swap to preferred asset", "thorname", affThorname.Name, "err", err)
				}
			}
		}

		if affiliateSwap.Tx.Coins[0].Amount.GTE(amt) {
			affiliateSwap.Tx.Coins[0].Amount = amt
		}

		// trunk-ignore(golangci-lint/govet): shadow
		if err := h.mgr.Keeper().SetSwapQueueItem(ctx, *affiliateSwap, 1); err != nil {
			ctx.Logger().Error("fail to add swap to queue", "error", err)
		}
	}
}
