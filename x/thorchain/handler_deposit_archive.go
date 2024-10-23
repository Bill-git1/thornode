package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

// updateAffiliateCollector - accrue RUNE in the AffiliateCollector module and check if
// a PreferredAsset swap should be triggered
func (h DepositHandler) updateAffiliateCollector(ctx cosmos.Context, coin common.Coin, msg MsgSwap, thorname *THORName) {
	affcol, err := h.mgr.Keeper().GetAffiliateCollector(ctx, thorname.Owner)
	if err != nil {
		ctx.Logger().Error("failed to get affiliate collector", "msg", msg.AffiliateAddress, "error", err)
	} else {
		// trunk-ignore(golangci-lint/govet): shadow
		if err := h.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, AffiliateCollectorName, common.NewCoins(coin)); err != nil {
			ctx.Logger().Error("failed to send funds to affiliate collector", "error", err)
		} else {
			affcol.RuneAmount = affcol.RuneAmount.Add(coin.Amount)
			h.mgr.Keeper().SetAffiliateCollector(ctx, affcol)
		}
	}

	// Check if accrued RUNE is 100x current outbound fee of preferred asset chain, if so
	// trigger the preferred asset swap
	ofRune, err := h.mgr.GasMgr().GetAssetOutboundFee(ctx, thorname.PreferredAsset, true)
	if err != nil {
		ctx.Logger().Error("failed to get outbound fee for preferred asset, skipping preferred asset swap", "name", thorname.Name, "asset", thorname.PreferredAsset, "error", err)
		return
	}

	multiplier := h.mgr.Keeper().GetConfigInt64(ctx, constants.PreferredAssetOutboundFeeMultiplier)
	threshold := ofRune.Mul(cosmos.NewUint(uint64(multiplier)))
	if affcol.RuneAmount.GT(threshold) {
		if err = triggerPreferredAssetSwap(ctx, h.mgr, msg.AffiliateAddress, msg.Tx.ID, *thorname, affcol, 1); err != nil {
			ctx.Logger().Error("fail to swap to preferred asset", "thorname", thorname.Name, "err", err)
		}
	}
}

func (h DepositHandler) addSwapDirectV136(ctx cosmos.Context, msg MsgSwap) {
	if msg.Tx.Coins.IsEmpty() {
		return
	}
	amt := cosmos.ZeroUint()
	swapSourceAsset := msg.Tx.Coins[0].Asset

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
		toAddress, err := msg.AffiliateAddress.AccAddress()
		if err != nil {
			ctx.Logger().Error("fail to convert address into AccAddress", "msg", msg.AffiliateAddress, "error", err)
			return
		}

		memo, err := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), msg.Tx.Memo)
		if err != nil {
			ctx.Logger().Error("fail to parse swap memo", "memo", msg.Tx.Memo, "error", err)
			return
		}
		// since native transaction fee has been charged to inbound from address, thus for affiliated fee , the network doesn't need to charge it again
		coin := common.NewCoin(swapSourceAsset, amt)
		affThorname := memo.GetAffiliateTHORName()

		// PreferredAsset set, update the AffiliateCollector module
		if affThorname != nil && !affThorname.PreferredAsset.IsEmpty() && swapSourceAsset.IsRune() {
			h.updateAffiliateCollector(ctx, coin, msg, affThorname)
			return
		}

		// No PreferredAsset set, normal behavior
		sdkErr := h.mgr.Keeper().SendFromModuleToAccount(ctx, AsgardName, toAddress, common.NewCoins(coin))
		if sdkErr != nil {
			ctx.Logger().Error("fail to send native asset to affiliate", "msg", msg.AffiliateAddress, "error", err, "asset", swapSourceAsset)
		}
	}
}
