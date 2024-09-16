package thorchain

import (
	"errors"
	"fmt"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
)

func (h SwapHandler) validateV129(ctx cosmos.Context, msg MsgSwap) error {
	if err := msg.ValidateBasic(); err != nil {
		return err
	}

	// For external-origin (here valid) memos, do not allow a network module as the final destination.
	// If unable to parse the memo, here assume it to be internal.
	memo, _ := ParseMemoWithTHORNames(ctx, h.mgr.Keeper(), msg.Tx.Memo)
	mem, isSwapMemo := memo.(SwapMemo)
	if isSwapMemo {
		destAccAddr, err := mem.Destination.AccAddress()
		// A network module address would be resolvable,
		// so if not resolvable it should not be a network module address.
		if err == nil && IsModuleAccAddress(h.mgr.Keeper(), destAccAddr) {
			return fmt.Errorf("a network module cannot be the final destination of a swap memo")
		}
	}

	target := msg.TargetAsset
	if h.mgr.Keeper().IsTradingHalt(ctx, &msg) {
		return errors.New("trading is halted, can't process swap")
	}

	if target.IsDerivedAsset() || msg.Tx.Coins[0].Asset.IsDerivedAsset() {
		if h.mgr.Keeper().GetConfigInt64(ctx, constants.EnableDerivedAssets) == 0 {
			// since derived assets are disabled, only the protocol can use
			// them (specifically lending)
			if !msg.Tx.FromAddress.Equals(common.NoopAddress) && !msg.Tx.ToAddress.Equals(common.NoopAddress) && !msg.Destination.Equals(common.NoopAddress) {
				return fmt.Errorf("swapping to/from a derived asset is not allowed, except for lending (%s or %s)", msg.Tx.FromAddress, msg.Destination)
			}
		}
	}

	if len(msg.Aggregator) > 0 {
		swapOutDisabled := h.mgr.Keeper().GetConfigInt64(ctx, constants.SwapOutDexAggregationDisabled)
		if swapOutDisabled > 0 {
			return errors.New("swap out dex integration disabled")
		}
		if !msg.TargetAsset.Equals(msg.TargetAsset.Chain.GetGasAsset()) {
			return fmt.Errorf("target asset (%s) is not gas asset , can't use dex feature", msg.TargetAsset)
		}
		// validate that a referenced dex aggregator is legit
		addr, err := FetchDexAggregator(h.mgr.GetVersion(), target.Chain, msg.Aggregator)
		if err != nil {
			return err
		}
		if addr == "" {
			return fmt.Errorf("aggregator address is empty")
		}
		if len(msg.AggregatorTargetAddress) == 0 {
			return fmt.Errorf("aggregator target address is empty")
		}
	}

	if target.IsSyntheticAsset() && target.GetLayer1Asset().IsNative() {
		return errors.New("minting a synthetic of a native coin is not allowed")
	}

	if target.IsTradeAsset() && target.GetLayer1Asset().IsNative() {
		return errors.New("swapping to a trade asset of a native coin is not allowed")
	}

	var sourceCoin common.Coin
	if len(msg.Tx.Coins) > 0 {
		sourceCoin = msg.Tx.Coins[0]
	}

	if msg.IsStreaming() {
		pausedStreaming := h.mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapPause)
		if pausedStreaming > 0 {
			return fmt.Errorf("streaming swaps are paused")
		}

		// if either source or target in ragnarok, streaming is not allowed
		for _, asset := range []common.Asset{sourceCoin.Asset, target} {
			key := "RAGNAROK-" + asset.MimirString()
			ragnarok, err := h.mgr.Keeper().GetMimir(ctx, key)
			if err == nil && ragnarok > 0 {
				return fmt.Errorf("streaming swaps disabled on ragnarok asset %s", asset)
			}
		}

		swp := msg.GetStreamingSwap()
		if h.mgr.Keeper().StreamingSwapExists(ctx, msg.Tx.ID) {
			var err error
			swp, err = h.mgr.Keeper().GetStreamingSwap(ctx, msg.Tx.ID)
			if err != nil {
				ctx.Logger().Error("fail to fetch streaming swap", "error", err)
				return err
			}
		}

		if (swp.Quantity > 0 && swp.IsDone()) || swp.In.GTE(swp.Deposit) {
			// check both swap count and swap in vs deposit to cover all basis
			return fmt.Errorf("streaming swap is completed, cannot continue to swap again")
		}

		if swp.Count > 0 {
			// end validation early, as synth TVL caps are not applied to streaming
			// swaps. This is to ensure that streaming swaps don't get interrupted
			// and cause a partial fulfillment, which would cause issues for
			// internal streaming swaps for savers and loans.
			return nil
		} else {
			// first swap we check the entire swap amount (not just the
			// sub-swap amount) to ensure the value of the entire has TVL/synth
			// room
			sourceCoin.Amount = swp.Deposit
		}
	}

	if target.IsSyntheticAsset() {
		// the following is only applicable for mainnet
		totalLiquidityRUNE, err := h.getTotalLiquidityRUNE(ctx)
		if err != nil {
			return ErrInternal(err, "fail to get total liquidity RUNE")
		}

		// total liquidity RUNE after current add liquidity
		if len(msg.Tx.Coins) > 0 {
			// calculate rune value on incoming swap, and add to total liquidity.
			runeVal := sourceCoin.Amount
			if !sourceCoin.IsRune() {
				pool, err := h.mgr.Keeper().GetPool(ctx, sourceCoin.Asset.GetLayer1Asset())
				if err != nil {
					return ErrInternal(err, "fail to get pool")
				}
				runeVal = pool.AssetValueInRune(sourceCoin.Amount)
			}
			totalLiquidityRUNE = totalLiquidityRUNE.Add(runeVal)
		}
		maximumLiquidityRune, err := h.mgr.Keeper().GetMimir(ctx, constants.MaximumLiquidityRune.String())
		if maximumLiquidityRune < 0 || err != nil {
			maximumLiquidityRune = h.mgr.GetConstants().GetInt64Value(constants.MaximumLiquidityRune)
		}
		if maximumLiquidityRune > 0 {
			if totalLiquidityRUNE.GT(cosmos.NewUint(uint64(maximumLiquidityRune))) {
				return errAddLiquidityRUNEOverLimit
			}
		}

		// fail validation if synth supply is already too high, relative to pool depth
		// do a simulated swap to see how much of the target synth the network
		// will need to mint and check if that amount exceeds limits
		targetAmount, runeAmount := cosmos.ZeroUint(), cosmos.ZeroUint()
		swapper, err := GetSwapper(h.mgr.GetVersion())
		if err == nil {
			if sourceCoin.IsRune() {
				runeAmount = sourceCoin.Amount
			} else {
				// asset --> rune swap
				sourceAssetPool := sourceCoin.Asset
				if sourceAssetPool.IsSyntheticAsset() {
					sourceAssetPool = sourceAssetPool.GetLayer1Asset()
				}
				sourcePool, err := h.mgr.Keeper().GetPool(ctx, sourceAssetPool)
				if err != nil {
					ctx.Logger().Error("fail to fetch pool for swap simulation", "error", err)
				} else {
					runeAmount = swapper.CalcAssetEmission(sourcePool.BalanceAsset, sourceCoin.Amount, sourcePool.BalanceRune)
				}
			}
			// rune --> synth swap
			targetPool, err := h.mgr.Keeper().GetPool(ctx, target.GetLayer1Asset())
			if err != nil {
				ctx.Logger().Error("fail to fetch pool for swap simulation", "error", err)
			} else {
				targetAmount = swapper.CalcAssetEmission(targetPool.BalanceRune, runeAmount, targetPool.BalanceAsset)
			}
		}
		err = isSynthMintPaused(ctx, h.mgr, target, targetAmount)
		if err != nil {
			return err
		}

		ensureLiquidityNoLargerThanBond := h.mgr.GetConstants().GetBoolValue(constants.StrictBondLiquidityRatio)
		if ensureLiquidityNoLargerThanBond {
			// If source and target are synthetic assets there is no net
			// liquidity gain (RUNE is just moved from pool A to pool B), so
			// skip this check
			if !sourceCoin.Asset.IsSyntheticAsset() && atTVLCap(ctx, common.NewCoins(sourceCoin), h.mgr) {
				return errAddLiquidityRUNEMoreThanBond
			}
		}
	}

	return nil
}
