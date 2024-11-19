package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
)

func getMaxSwapQuantityV136(ctx cosmos.Context, mgr Manager, sourceAsset, targetAsset common.Asset, swp StreamingSwap) (uint64, error) {
	if swp.Interval == 0 {
		return 0, nil
	}

	// collect pools involved in this swap
	var pools Pools
	totalRuneDepth := cosmos.ZeroUint()
	for _, asset := range []common.Asset{sourceAsset, targetAsset} {
		if asset.IsRune() {
			continue
		}

		pool, err := mgr.Keeper().GetPool(ctx, asset.GetLayer1Asset())
		if err != nil {
			ctx.Logger().Error("fail to fetch pool", "error", err)
			return 0, err
		}
		pools = append(pools, pool)
		totalRuneDepth = totalRuneDepth.Add(pool.BalanceRune)
	}
	if len(pools) == 0 {
		return 0, fmt.Errorf("dev error: no pools selected during a streaming swap")
	}
	var virtualDepth cosmos.Uint
	switch len(pools) {
	case 1:
		// single swap, virtual depth is the same size as the single pool
		virtualDepth = totalRuneDepth
	case 2:
		// double swap, dynamically calculate a virtual pool that is between the
		// depth of pool1 and pool2. This calculation should result in a
		// consistent swap fee (in bps) no matter the depth of the pools. The
		// larger the difference between the pools, the more the virtual pool
		// skews towards the smaller pool. This results in less rewards given
		// to the larger pool, and more rewards given to the smaller pool.

		// (2*r1*r2) / (r1+r2)
		r1 := pools[0].BalanceRune
		r2 := pools[1].BalanceRune
		num := r1.Mul(r2).MulUint64(2)
		denom := r1.Add(r2)
		if denom.IsZero() {
			return 0, fmt.Errorf("dev error: both pools have no rune balance")
		}
		virtualDepth = num.Quo(denom)
	default:
		return 0, fmt.Errorf("dev error: unsupported number of pools in a streaming swap: %d", len(pools))
	}
	if !sourceAsset.IsRune() {
		// since the inbound asset is not rune, the virtual depth needs to be
		// recalculated to be the asset side
		virtualDepth = common.GetUncappedShare(virtualDepth, pools[0].BalanceRune, pools[0].BalanceAsset)
	}

	// determine lowest min slip for the swap assets, which results in highest quantity
	minMinSlip := cosmos.ZeroUint()
	for _, asset := range []common.Asset{sourceAsset, targetAsset} {
		if asset.IsRune() {
			continue
		}
		minSlip := getMinSlipBps(ctx, mgr.Keeper(), asset)
		if minSlip.IsZero() {
			continue
		}
		if minMinSlip.IsZero() || minSlip.LT(minMinSlip) {
			minMinSlip = minSlip
		}
	}
	if minMinSlip.IsZero() {
		return 0, fmt.Errorf("streaming swaps are not allowed with a min slip of zero")
	}
	minSize := common.GetSafeShare(minMinSlip, cosmos.NewUint(constants.MaxBasisPts), virtualDepth)
	if minSize.IsZero() {
		return 1, nil
	}
	maxSwapQuantity := swp.Deposit.Quo(minSize)

	// make sure maxSwapQuantity doesn't infringe on max length that a
	// streaming swap can exist
	var maxLength int64
	if sourceAsset.IsNative() && targetAsset.IsNative() {
		maxLength = mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapMaxLengthNative)
	} else {
		maxLength = mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapMaxLength)
	}
	if swp.Interval == 0 {
		return 1, nil
	}
	maxSwapInMaxLength := uint64(maxLength) / swp.Interval
	if maxSwapQuantity.GT(cosmos.NewUint(maxSwapInMaxLength)) {
		return maxSwapInMaxLength, nil
	}

	// sanity check that max swap quantity is not zero
	if maxSwapQuantity.IsZero() {
		return 1, nil
	}

	// if swapping with a derived asset, reduce quantity relative to derived
	// virtual pool depth. The equation for this as follows
	dbps := cosmos.ZeroUint()
	for _, asset := range []common.Asset{sourceAsset, targetAsset} {
		if !asset.IsDerivedAsset() {
			continue
		}

		// get the rune depth of the anchor pool(s)
		runeDepth, _, _ := mgr.NetworkMgr().CalcAnchor(ctx, mgr, asset)
		dpool, _ := mgr.Keeper().GetPool(ctx, asset) // get the derived asset pool
		newDbps := common.GetUncappedShare(dpool.BalanceRune, runeDepth, cosmos.NewUint(constants.MaxBasisPts))
		if dbps.IsZero() || newDbps.LT(dbps) {
			dbps = newDbps
		}
	}
	if !dbps.IsZero() {
		// quantity = 1 / (1-dbps)
		// But since we're dealing in basis points (to avoid float math)
		// quantity = 10,000 / (10,000 - dbps)
		maxBasisPoints := cosmos.NewUint(constants.MaxBasisPts)
		diff := common.SafeSub(maxBasisPoints, dbps)
		if !diff.IsZero() {
			newQuantity := maxBasisPoints.Quo(diff)
			if maxSwapQuantity.GT(newQuantity) {
				return newQuantity.Uint64(), nil
			}
		}
	}

	return maxSwapQuantity.Uint64(), nil
}

func getMaxSwapQuantityV1(ctx cosmos.Context, mgr Manager, sourceAsset, targetAsset common.Asset, swp StreamingSwap) (uint64, error) {
	if swp.Interval == 0 {
		return 0, nil
	}
	// collect pools involved in this swap
	var pools Pools
	totalRuneDepth := cosmos.ZeroUint()
	for _, asset := range []common.Asset{sourceAsset, targetAsset} {
		if asset.IsRune() {
			continue
		}

		pool, err := mgr.Keeper().GetPool(ctx, asset.GetLayer1Asset())
		if err != nil {
			ctx.Logger().Error("fail to fetch pool", "error", err)
			return 0, err
		}
		pools = append(pools, pool)
		totalRuneDepth = totalRuneDepth.Add(pool.BalanceRune)
	}
	if len(pools) == 0 {
		return 0, fmt.Errorf("dev error: no pools selected during a streaming swap")
	}
	var virtualDepth cosmos.Uint
	switch len(pools) {
	case 1:
		// single swap, virtual depth is the same size as the single pool
		virtualDepth = totalRuneDepth
	case 2:
		// double swap, dynamically calculate a virtual pool that is between the
		// depth of pool1 and pool2. This calculation should result in a
		// consistent swap fee (in bps) no matter the depth of the pools. The
		// larger the difference between the pools, the more the virtual pool
		// skews towards the smaller pool. This results in less rewards given
		// to the larger pool, and more rewards given to the smaller pool.

		// (2*r1*r2) / (r1+r2)
		r1 := pools[0].BalanceRune
		r2 := pools[1].BalanceRune
		num := r1.Mul(r2).MulUint64(2)
		denom := r1.Add(r2)
		if denom.IsZero() {
			return 0, fmt.Errorf("dev error: both pools have no rune balance")
		}
		virtualDepth = num.Quo(denom)
	default:
		return 0, fmt.Errorf("dev error: unsupported number of pools in a streaming swap: %d", len(pools))
	}
	if !sourceAsset.IsRune() {
		// since the inbound asset is not rune, the virtual depth needs to be
		// recalculated to be the asset side
		virtualDepth = common.GetUncappedShare(virtualDepth, pools[0].BalanceRune, pools[0].BalanceAsset)
	}
	// we multiply by 100 to ensure we can support decimal points (ie 2.5bps / 2 == 1.25)
	minBP := mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapMinBPFee) * constants.StreamingSwapMinBPFeeMulti
	minBP /= int64(len(pools)) // since multiple swaps are executed, then minBP should be adjusted
	if minBP == 0 {
		return 0, fmt.Errorf("streaming swaps are not allows with a min BP of zero")
	}
	// constants.StreamingSwapMinBPFee is in 10k basis point x 10, so we add an
	// addition zero here (_0)
	minSize := common.GetSafeShare(cosmos.SafeUintFromInt64(minBP), cosmos.SafeUintFromInt64(10_000*constants.StreamingSwapMinBPFeeMulti), virtualDepth)
	if minSize.IsZero() {
		return 1, nil
	}
	maxSwapQuantity := swp.Deposit.Quo(minSize)

	// make sure maxSwapQuantity doesn't infringe on max length that a
	// streaming swap can exist
	var maxLength int64
	if sourceAsset.IsNative() && targetAsset.IsNative() {
		maxLength = mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapMaxLengthNative)
	} else {
		maxLength = mgr.Keeper().GetConfigInt64(ctx, constants.StreamingSwapMaxLength)
	}
	if swp.Interval == 0 {
		return 1, nil
	}
	maxSwapInMaxLength := uint64(maxLength) / swp.Interval
	if maxSwapQuantity.GT(cosmos.NewUint(maxSwapInMaxLength)) {
		return maxSwapInMaxLength, nil
	}

	// sanity check that max swap quantity is not zero
	if maxSwapQuantity.IsZero() {
		return 1, nil
	}

	// if swapping with a derived asset, reduce quantity relative to derived
	// virtual pool depth. The equation for this as follows
	dbps := cosmos.ZeroUint()
	for _, asset := range []common.Asset{sourceAsset, targetAsset} {
		if !asset.IsDerivedAsset() {
			continue
		}

		// get the rune depth of the anchor pool(s)
		runeDepth, _, _ := mgr.NetworkMgr().CalcAnchor(ctx, mgr, asset)
		dpool, _ := mgr.Keeper().GetPool(ctx, asset) // get the derived asset pool
		newDbps := common.GetUncappedShare(dpool.BalanceRune, runeDepth, cosmos.NewUint(constants.MaxBasisPts))
		if dbps.IsZero() || newDbps.LT(dbps) {
			dbps = newDbps
		}
	}
	if !dbps.IsZero() {
		// quantity = 1 / (1-dbps)
		// But since we're dealing in basis points (to avoid float math)
		// quantity = 10,000 / (10,000 - dbps)
		maxBasisPoints := cosmos.NewUint(constants.MaxBasisPts)
		diff := common.SafeSub(maxBasisPoints, dbps)
		if !diff.IsZero() {
			newQuantity := maxBasisPoints.Quo(diff)
			if maxSwapQuantity.GT(newQuantity) {
				return newQuantity.Uint64(), nil
			}
		}
	}

	return maxSwapQuantity.Uint64(), nil
}
