package thorchain

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cosmos/cosmos-sdk/telemetry"
	"github.com/hashicorp/go-metrics"
	"github.com/jinzhu/copier"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// SwapQueueAdvVCUR is going to manage the swaps queue
type SwapQueueAdvVCUR struct {
	k keeper.Keeper
}

// newSwapQueueAdvVCUR create a new vault manager
func newSwapQueueAdvVCUR(k keeper.Keeper) *SwapQueueAdvVCUR {
	return &SwapQueueAdvVCUR{k: k}
}

// FetchQueue - grabs all swap queue items from the kvstore and returns them
func (vm *SwapQueueAdvVCUR) FetchQueue(ctx cosmos.Context, mgr Manager, pairs tradePairs, pools Pools, todo tradePairs) (swapItems, error) { // nolint
	items := make(swapItems, 0)

	// if the network is doing a pool cycle, no swaps are executed this
	// block. This is because the change of active pools can cause the
	// mechanism to index/encode the selected pools/trading pairs that need to
	// be checked.
	poolCycle := mgr.Keeper().GetConfigInt64(ctx, constants.PoolCycle)
	if ctx.BlockHeight()%poolCycle == 0 {
		return nil, nil
	}

	// If todo is empty, set it to every pair
	if len(todo) == 0 {
		todo = pairs
	}

	// get market swap
	marketItems, err := vm.k.GetAdvSwapQueueIndex(ctx, MsgSwap{SwapType: MarketSwap})
	if err != nil {
		return nil, err
	}
	for _, item := range marketItems {
		msg, err := vm.k.GetAdvSwapQueueItem(ctx, item.TxID, item.Index)
		if err != nil {
			ctx.Logger().Error("fail to fetch adv swap item", "error", err)
			continue
		}

		if !vm.isSwapReady(ctx, msg) {
			continue
		}

		items = append(items, swapItem{
			msg:   msg,
			index: item.Index,
			fee:   cosmos.ZeroUint(),
			slip:  cosmos.ZeroUint(),
		})
	}

	for _, pair := range todo {
		newItems := vm.discoverLimitSwaps(ctx, mgr, pair, pools)
		items = append(items, newItems...)
	}

	return items, nil
}

func (vm *SwapQueueAdvVCUR) isSwapReady(ctx cosmos.Context, msg MsgSwap) bool {
	// skip processing limit swaps if EnableAdvSwapQueue is set to market only mode
	val := vm.k.GetConfigInt64(ctx, constants.EnableAdvSwapQueue)
	if types.AdvSwapQueueMode(val) == types.AdvSwapQueueModeMarketOnly && msg.IsLimitSwap() {
		return false
	}

	pausedStreaming := vm.k.GetConfigInt64(ctx, constants.StreamingSwapPause)
	if pausedStreaming > 0 && msg.IsStreaming() {
		return false
	}

	// Check if it's the right interval for the next sub-swap
	if msg.State.Interval > 0 && (ctx.BlockHeight()-msg.State.LastHeight)%int64(msg.State.Interval) != 0 {
		return false
	}

	// if interval is 0, then allow a market swap to execute multiple times in the same block
	// if interval is 1 or greater, then only swap one time per block
	if msg.State.Interval > 0 && msg.State.LastHeight >= ctx.BlockHeight() {
		return false // skip
	}

	if vm.k.IsTradingHalt(ctx, &msg) {
		// if trading/chain is halted, skip
		return false // skip
	}

	// if either source or target in ragnarok, streaming is not allowed
	for _, asset := range []common.Asset{msg.Tx.Coins[0].Asset, msg.TargetAsset} {
		key := "RAGNAROK-" + asset.MimirString()
		ragnarok, err := vm.k.GetMimir(ctx, key)
		if err == nil && ragnarok > 0 {
			return false
		}
	}

	return true
}

func (vm *SwapQueueAdvVCUR) discoverLimitSwaps(ctx cosmos.Context, mgr Manager, pair tradePair, pools Pools) swapItems {
	items := make(swapItems, 0)

	iter := vm.k.GetAdvSwapQueueIndexIterator(ctx, LimitSwap, pair.source, pair.target)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		ratio, err := vm.parseRatioFromKey(string(iter.Key()))
		if err != nil {
			ctx.Logger().Error("fail to parse ratio", "key", string(iter.Key()), "error", err)
			continue
		}

		// Check if fee-less swap meets the ratio requirement
		canExecute := vm.checkFeelessSwap(pools, pair, ratio)

		record := make([]string, 0)
		value := ProtoStrings{Value: record}
		if err := vm.k.Cdc().Unmarshal(iter.Value(), &value); err != nil {
			ctx.Logger().Error("fail to fetch indexed txn hashes", "error", err)
			continue
		}

		for i, rec := range value.Value {
			// Parse format "txID-index" using last hyphen to handle Cosmos indexed TxIDs
			lastHyphenIndex := strings.LastIndex(rec, "-")
			if lastHyphenIndex == -1 {
				ctx.Logger().Error("invalid swap queue index format - no hyphen found", "record", rec)
				continue
			}
			parts := []string{rec[:lastHyphenIndex], rec[lastHyphenIndex+1:]}
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				ctx.Logger().Error("invalid swap queue index format", "record", rec)
				continue
			}

			hash, err := common.NewTxID(parts[0])
			if err != nil {
				ctx.Logger().Error("fail to parse tx hash", "error", err)
				continue
			}

			index, err := strconv.Atoi(parts[1])
			if err != nil {
				ctx.Logger().Error("fail to parse index", "error", err)
				continue
			}

			msg, err := vm.k.GetAdvSwapQueueItem(ctx, hash, index)
			if err != nil {
				ctx.Logger().Error("fail to fetch msg swap", "error", err)
				continue
			}

			if !vm.isSwapReady(ctx, msg) {
				continue
			}

			// Check if our swap is already completed, ie a limit swap has expired
			if vm.IsDone(ctx, msg) {
				if err := settleSwap(ctx, mgr, msg, "swap has been completed."); err != nil {
					ctx.Logger().Error("fail to handle completed streaming limit swap", "error", err)
				}
				continue
			}

			// Only try to execute if the price check passed
			if !canExecute {
				continue
			}

			// do a swap, including swap fees and outbound fees. If this passes attempt the swap.
			if ok := vm.checkWithFeeSwap(ctx, mgr, pools, msg); !ok {
				continue
			}

			items = append(items, swapItem{
				msg:   msg,
				index: i,
				fee:   cosmos.ZeroUint(),
				slip:  cosmos.ZeroUint(),
			})
		}

		// If fee-less swap doesn't meet the ratio requirement, we can stop
		// checking further ratio indices since they're sorted
		if !canExecute {
			break
		}
	}
	return items
}

func (vm *SwapQueueAdvVCUR) IsDone(ctx cosmos.Context, msg MsgSwap) bool {
	if msg.IsDone() {
		return true
	}

	if msg.IsLimitSwap() {
		ttl := vm.getTTL(ctx, msg)
		if ctx.BlockHeight()-msg.InitialBlockHeight >= ttl {
			return true
		}
	}

	return false
}

// getTTL calculates the custom TTL for a limit swap message
// Uses State.Interval if specified and valid, otherwise falls back to maxAge
func (vm *SwapQueueAdvVCUR) getTTL(ctx cosmos.Context, msg MsgSwap) int64 {
	maxAge := vm.k.GetConfigInt64(ctx, constants.StreamingLimitSwapMaxAge)

	// Use custom TTL if specified via State.Interval, with validation
	ttl := maxAge // default to maxAge
	if msg.State.Interval > 0 && msg.State.Interval <= uint64(maxAge) {
		ttl = int64(msg.State.Interval)
	}

	return ttl
}

func (vm *SwapQueueAdvVCUR) checkFeelessSwap(pools Pools, pair tradePair, indexRatio uint64) bool {
	var ratio cosmos.Uint
	switch {
	case !pair.HasRune():
		sourcePool, ok := pools.Get(pair.source.GetLayer1Asset())
		if !ok {
			return false
		}
		targetPool, ok := pools.Get(pair.target.GetLayer1Asset())
		if !ok {
			return false
		}
		one := cosmos.NewUint(common.One)
		runeAmt := common.GetSafeShare(one, sourcePool.BalanceAsset, sourcePool.BalanceRune)
		emit := common.GetSafeShare(runeAmt, targetPool.BalanceRune, targetPool.BalanceAsset)
		ratio = vm.getRatio(one, emit)
	case pair.source.IsRune():
		pool, ok := pools.Get(pair.target.GetLayer1Asset())
		if !ok {
			return false
		}
		ratio = vm.getRatio(pool.BalanceRune, pool.BalanceAsset)
	case pair.target.IsRune():
		pool, ok := pools.Get(pair.source.GetLayer1Asset())
		if !ok {
			return false
		}
		ratio = vm.getRatio(pool.BalanceAsset, pool.BalanceRune)
	}
	return cosmos.NewUint(indexRatio).GT(ratio)
}

func (vm *SwapQueueAdvVCUR) checkWithFeeSwap(ctx cosmos.Context, mgr Manager, pools Pools, msg MsgSwap) bool {
	swapper, err := GetSwapper(vm.k.GetVersion())
	if err != nil {
		panic(err)
	}

	// account for affiliate fee
	source := msg.Tx.Coins[0]
	if !msg.AffiliateBasisPoints.IsZero() {
		maxBasisPoints := cosmos.NewUint(10_000)
		source.Amount = common.GetSafeShare(common.SafeSub(maxBasisPoints, msg.AffiliateBasisPoints), maxBasisPoints, source.Amount)
	}

	target := common.NewCoin(msg.TargetAsset, msg.TradeTarget)
	var emit cosmos.Uint
	switch {
	case !source.IsRune() && !target.IsRune():
		sourcePool, ok := pools.Get(source.Asset.GetLayer1Asset())
		if !ok {
			return false
		}
		targetPool, ok := pools.Get(target.Asset.GetLayer1Asset())
		if !ok {
			return false
		}
		emit = swapper.CalcAssetEmission(sourcePool.BalanceAsset, source.Amount, sourcePool.BalanceRune)
		emit = swapper.CalcAssetEmission(targetPool.BalanceRune, emit, targetPool.BalanceAsset)
	case source.IsRune():
		pool, ok := pools.Get(target.Asset.GetLayer1Asset())
		if !ok {
			return false
		}
		emit = swapper.CalcAssetEmission(pool.BalanceRune, source.Amount, pool.BalanceAsset)
	case target.IsRune():
		pool, ok := pools.Get(source.Asset.GetLayer1Asset())
		if !ok {
			return false
		}
		emit = swapper.CalcAssetEmission(pool.BalanceAsset, source.Amount, pool.BalanceRune)
	}

	// Check if this would be the last swap by temporarily simulating the state after this swap
	swapSize, _ := msg.NextSize()
	wouldBeLastSwap := false
	if msg.SwapType == MarketSwap {
		// For market swaps, check if count+1 >= quantity. We do >= instead of == just in case
		// of some unexpected dev error and count exceeds quantity somehow
		wouldBeLastSwap = msg.State.Count+1 >= msg.State.Quantity
	} else if msg.SwapType == LimitSwap {
		// For limit swaps, check if in + swapSize would equal deposit
		tempIn := msg.State.In.Add(swapSize)
		wouldBeLastSwap = tempIn.Equal(msg.State.Deposit)
	}

	// If this would be the last swap and target is not RUNE, account for outbound fee
	if wouldBeLastSwap && !target.IsRune() {
		// Get the outbound fee from the gas manager
		outboundFee, err := mgr.GasMgr().GetAssetOutboundFee(ctx, target.Asset, false)
		if err == nil && !outboundFee.IsZero() {
			// Deduct the outbound fee from emit amount before comparing
			emit = common.SafeSub(emit, outboundFee)
		}
	}

	return emit.GTE(target.Amount)
}

func (vm *SwapQueueAdvVCUR) getRatio(input, output cosmos.Uint) cosmos.Uint {
	if output.IsZero() {
		return cosmos.ZeroUint()
	}
	return input.MulUint64(1e8).Quo(output)
}

// getAssetPairs - fetches a list of strings that represents directional trading pairs
func (vm *SwapQueueAdvVCUR) getAssetPairs(ctx cosmos.Context) (tradePairs, Pools) {
	result := make(tradePairs, 0)
	var pools Pools

	assets := []common.Asset{common.RuneAsset()}
	iterator := vm.k.GetPoolIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var pool Pool
		err := vm.k.Cdc().Unmarshal(iterator.Value(), &pool)
		if err != nil {
			ctx.Logger().Error("fail to unmarshal pool", "error", err)
			continue
		}
		if pool.Status != PoolAvailable {
			continue
		}
		if pool.Asset.IsSyntheticAsset() {
			continue
		}
		assets = append(assets, pool.Asset)
		pools = append(pools, pool)
	}

	for _, a1 := range assets {
		for _, a2 := range assets {
			if a1.Equals(a2) {
				continue
			}
			result = append(result, genTradePair(a1, a2))
		}
	}

	return result, pools
}

func (vm *SwapQueueAdvVCUR) getMaxSwapQuantity(ctx cosmos.Context, mgr Manager, sourceAsset, targetAsset common.Asset, msg MsgSwap) (uint64, error) {
	// collect pools involved in this swap
	minSwapSize := cosmos.ZeroUint()
	var sourceAssetPool types.Pool
	for i, asset := range []common.Asset{sourceAsset, targetAsset} {
		if asset.IsRune() {
			continue
		}

		// get the asset pool
		pool, err := vm.k.GetPool(ctx, asset.GetLayer1Asset())
		if err != nil {
			ctx.Logger().Error("fail to fetch pool", "error", err)
			return 0, err
		}

		// store the source asset pool for later conversion of RUNE to asset
		if i == 0 {
			sourceAssetPool = pool
		}

		// get the configured min slip for this asset
		minSlip := getMinSlipBps(ctx, vm.k, asset)
		if minSlip.IsZero() {
			continue
		}

		// compute the minimum rune swap size for this leg of the swap
		minRuneSwapSize := common.GetSafeShare(minSlip, cosmos.NewUint(constants.MaxBasisPts), pool.BalanceRune)
		if minSwapSize.IsZero() || minRuneSwapSize.LT(minSwapSize) {
			minSwapSize = minRuneSwapSize
		}
	}

	var maxSwapQuantity cosmos.Uint

	// calculate the max swap quantity
	if !sourceAsset.IsRune() {
		minSwapSize = sourceAssetPool.RuneValueInAsset(minSwapSize)
	}
	if minSwapSize.IsZero() {
		// If no minimum slip is configured, respect the user's requested quantity
		// but still check against max length limits below
		maxSwapQuantity = cosmos.NewUint(msg.State.Quantity)
	} else {
		maxSwapQuantity = msg.State.Deposit.Quo(minSwapSize)
	}

	// make sure maxSwapQuantity doesn't infringe on max length that a
	// streaming swap can exist
	var maxLength int64
	if sourceAsset.IsNative() && targetAsset.IsNative() {
		maxLength = vm.k.GetConfigInt64(ctx, constants.StreamingSwapMaxLengthNative)
	} else {
		maxLength = vm.k.GetConfigInt64(ctx, constants.StreamingSwapMaxLength)
	}
	interval := msg.State.Interval
	if interval == 0 {
		interval = 1
	}
	maxSwapInMaxLength := uint64(maxLength) / interval
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
		// get the rune depth of the anchor pool(s)
		runeDepth, _, _ := mgr.NetworkMgr().CalcAnchor(ctx, mgr, asset)
		dpool, _ := vm.k.GetPool(ctx, asset) // get the derived asset pool
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

func (vm *SwapQueueAdvVCUR) AddSwapQueueItem(ctx cosmos.Context, mgr Manager, msg *MsgSwap) error {
	// If advanced swap queue is in market-only mode, reject limit swaps
	val := vm.k.GetConfigInt64(ctx, constants.EnableAdvSwapQueue)
	if types.AdvSwapQueueMode(val) == types.AdvSwapQueueModeMarketOnly && msg.IsLimitSwap() {
		return fmt.Errorf("limit swaps are not allowed in market-only mode")
	}

	// Set initial block height when adding the swap
	if msg.InitialBlockHeight == 0 {
		msg.InitialBlockHeight = ctx.BlockHeight()
	}

	// Initialize deposit state if not already set
	if msg.State.Deposit.IsZero() {
		msg.State.Deposit = msg.Tx.Coins[0].Amount
	}

	maxSwapQuantity, err := vm.getMaxSwapQuantity(ctx, mgr, msg.Tx.Coins[0].Asset, msg.TargetAsset, *msg)
	if err != nil {
		return err
	}

	if msg.State.Quantity == 0 {
		msg.State.Quantity = maxSwapQuantity
	}

	if msg.State.Quantity > maxSwapQuantity {
		msg.State.Quantity = maxSwapQuantity
	}

	swapHandler := NewSwapHandler(mgr)
	if err := swapHandler.validate(ctx, *msg); err != nil {
		ctx.Logger().Error("MsgSwap failed validation", "error", err)
		return err
	}

	// Add TTL tracking for limit swaps
	if msg.IsLimitSwap() && msg.InitialBlockHeight > 0 {
		ttl := vm.getTTL(ctx, *msg)
		expiryHeight := msg.InitialBlockHeight + ttl
		if err := vm.k.AddToLimitSwapTTL(ctx, expiryHeight, msg.Tx.ID); err != nil {
			ctx.Logger().Error("fail to add limit swap to TTL", "error", err, "txID", msg.Tx.ID.String(), "expiryHeight", expiryHeight)
		}
	}

	if err := vm.k.SetAdvSwapQueueItem(ctx, *msg); err != nil {
		ctx.Logger().Error("fail to add swap item", "error", err)
		return err
	}

	return nil
}

// EndBlock trigger the real swap to be processed
func (vm *SwapQueueAdvVCUR) EndBlock(ctx cosmos.Context, mgr Manager, telemetryEnabled bool) error {
	swapHandler := NewSwapHandler(mgr)

	minSwapsPerBlock, err := vm.k.GetMimir(ctx, constants.MinSwapsPerBlock.String())
	if minSwapsPerBlock < 0 || err != nil {
		minSwapsPerBlock = mgr.GetConstants().GetInt64Value(constants.MinSwapsPerBlock)
	}
	maxSwapsPerBlock, err := vm.k.GetMimir(ctx, constants.MaxSwapsPerBlock.String())
	if maxSwapsPerBlock < 0 || err != nil {
		maxSwapsPerBlock = mgr.GetConstants().GetInt64Value(constants.MaxSwapsPerBlock)
	}
	synthVirtualDepthMult, err := vm.k.GetMimir(ctx, constants.VirtualMultSynthsBasisPoints.String())
	if synthVirtualDepthMult < 1 || err != nil {
		synthVirtualDepthMult = mgr.GetConstants().GetInt64Value(constants.VirtualMultSynthsBasisPoints)
	}

	// Get rapid swap max from config (mimir or constants)
	rapidSwapMax := mgr.Keeper().GetConfigInt64(ctx, constants.AdvSwapQueueRapidSwapMax)

	todo := make(tradePairs, 0)
	pairs, pools := vm.getAssetPairs(ctx)
	iterationCount := int64(0)

	// Telemetry tracking variables
	totalSwapsProcessed := int64(0)
	marketSwapCount := int64(0)
	limitSwapCount := int64(0)
	completedSwapCount := int64(0)

	// Rapid swap iterations
	for iteration := int64(0); iteration < rapidSwapMax; iteration++ {
		iterationCount = iteration + 1

		swaps, err := vm.FetchQueue(ctx, mgr, pairs, pools, todo)
		if err != nil {
			ctx.Logger().Error("fail to fetch swap queue from store", "error", err)
			return err
		}

		// Exit early if no swaps found
		if len(swaps) == 0 {
			break
		}

		swaps, err = vm.scoreMsgs(ctx, swaps, synthVirtualDepthMult)
		if err != nil {
			ctx.Logger().Error("fail to fetch swap items", "error", err)
			// continue, don't exit, just do them out of order (instead of not at all)
		}
		swaps = swaps.Sort()

		for i := int64(0); i < vm.getTodoNum(int64(len(swaps)), minSwapsPerBlock, maxSwapsPerBlock); i++ {
			pick := swaps[i]
			var msg MsgSwap
			if err := copier.Copy(&msg, &pick.msg); err != nil {
				ctx.Logger().Error("fail copy msg", "msg", msg.Tx.String(), "error", err)
				continue
			}

			// Track swap types for telemetry
			if msg.IsLimitSwap() {
				limitSwapCount++
			} else {
				marketSwapCount++
			}
			totalSwapsProcessed++

			// Preserve original values before modification
			originalAmount := msg.Tx.Coins[0].Amount
			originalTradeTarget := msg.TradeTarget

			msg.Tx.Coins[0].Amount, msg.TradeTarget = msg.NextSize()

			// Create a cache context for the swap to ensure state changes are only
			// committed if the swap succeeds (similar to regular swap queue manager)
			cacheCtx, commit := ctx.CacheContext()

			// make the primary swap using the cached context
			var settleMsg string
			_, emit, handleErr := swapHandler.RunWithEmit(cacheCtx, &msg)
			if handleErr != nil {
				// Don't commit - this discards all state changes
				ctx.Logger().Error("fail to handle completed streaming limit swap", "error", handleErr)
				msg.State.FailedSwaps = append(msg.State.FailedSwaps, msg.State.Count)
				msg.State.FailedSwapReasons = append(msg.State.FailedSwapReasons, handleErr.Error())
				settleMsg = handleErr.Error()
			} else {
				// Success - commit the changes
				commit()
				settleMsg = "swap has been completed"

				// Update state for successful swap
				msg.State.In = msg.State.In.Add(msg.Tx.Coins[0].Amount)
				msg.State.Out = msg.State.Out.Add(emit)

				todo = todo.findMatchingTrades(genTradePair(msg.Tx.Coins[0].Asset, msg.TargetAsset), pairs)
			}
			msg.State.Count += 1
			msg.State.LastHeight = ctx.BlockHeight()

			// Restore original values before saving
			msg.Tx.Coins[0].Amount = originalAmount
			msg.TradeTarget = originalTradeTarget

			// Save the updated swap state back to the keeper
			if err := vm.k.SetAdvSwapQueueItem(ctx, msg); err != nil {
				ctx.Logger().Error("fail to save swap item", "error", err)
			}
			if vm.IsDone(ctx, msg) {
				if err := settleSwap(ctx, mgr, msg, settleMsg); err != nil {
					ctx.Logger().Error("fail to handle completed streaming limit swap", "error", err)
				}
				completedSwapCount++
			}
		}
	}

	// Log the number of iterations completed
	ctx.Logger().Info("advanced swap iterations completed", "count", iterationCount)

	// Process expired limit swaps at current block height
	if err := vm.processExpiredLimitSwaps(ctx, mgr); err != nil {
		ctx.Logger().Error("fail to process expired limit swaps", "error", err)
	}

	// Emit telemetry metrics only if telemetry is enabled
	if telemetryEnabled {
		vm.emitAdvSwapQueueTelemetry(ctx, mgr, iterationCount, totalSwapsProcessed, marketSwapCount, limitSwapCount, completedSwapCount)
	}

	return nil
}

// getTodoNum - determine how many swaps to do.
func (vm *SwapQueueAdvVCUR) getTodoNum(queueLen, minSwapsPerBlock, maxSwapsPerBlock int64) int64 {
	// Do half the length of the queue. Unless...
	//  1. Half the queue length is greater than maxSwapsPerBlock
	//  2. Half the queue length is less than minSwapsPerBlock
	todo := queueLen / 2
	if minSwapsPerBlock > todo {
		if queueLen < minSwapsPerBlock {
			todo = queueLen
		} else {
			todo = minSwapsPerBlock
		}
	}
	if maxSwapsPerBlock < todo {
		todo = maxSwapsPerBlock
	}
	return todo
}

// scoreMsgs - this takes a list of MsgSwap, and converts them to a scored
// swapItem list
func (vm *SwapQueueAdvVCUR) scoreMsgs(ctx cosmos.Context, items swapItems, synthVirtualDepthMult int64) (swapItems, error) {
	pools := make(map[common.Asset]Pool)

	for i, item := range items {
		// the asset customer send
		sourceAsset := item.msg.Tx.Coins[0].Asset
		// the asset customer want
		targetAsset := item.msg.TargetAsset

		for _, a := range []common.Asset{sourceAsset, targetAsset} {
			if a.IsRune() {
				continue
			}

			if _, ok := pools[a]; !ok {
				var err error
				pools[a], err = vm.k.GetPool(ctx, a)
				if err != nil {
					ctx.Logger().Error("fail to get pool", "pool", a, "error", err)
					continue
				}
			}
		}

		poolAsset := sourceAsset
		if poolAsset.IsRune() {
			poolAsset = targetAsset
		}
		pool := pools[poolAsset]
		if pool.IsEmpty() || !pool.IsAvailable() || pool.BalanceRune.IsZero() || pool.BalanceAsset.IsZero() {
			continue
		}
		virtualDepthMult := int64(10_000)
		if poolAsset.IsSyntheticAsset() {
			virtualDepthMult = synthVirtualDepthMult
		}
		vm.getLiquidityFeeAndSlip(ctx, pool, item.msg.Tx.Coins[0], &items[i], virtualDepthMult)

		if sourceAsset.IsRune() || targetAsset.IsRune() {
			// single swap , stop here
			continue
		}
		// double swap , thus need to convert source coin to RUNE and calculate fee and slip again
		runeCoin := common.NewCoin(common.RuneAsset(), pool.AssetValueInRune(item.msg.Tx.Coins[0].Amount))
		poolAsset = targetAsset
		pool = pools[poolAsset]
		if pool.IsEmpty() || !pool.IsAvailable() || pool.BalanceRune.IsZero() || pool.BalanceAsset.IsZero() {
			continue
		}
		virtualDepthMult = int64(10_000)
		if targetAsset.IsSyntheticAsset() {
			virtualDepthMult = synthVirtualDepthMult
		}
		vm.getLiquidityFeeAndSlip(ctx, pool, runeCoin, &items[i], virtualDepthMult)
	}

	return items, nil
}

// getLiquidityFeeAndSlip calculate liquidity fee and slip, fee is in RUNE
func (vm *SwapQueueAdvVCUR) getLiquidityFeeAndSlip(ctx cosmos.Context, pool Pool, sourceCoin common.Coin, item *swapItem, virtualDepthMult int64) {
	// Get our X, x, Y values
	var X, x, Y cosmos.Uint
	x = sourceCoin.Amount
	if sourceCoin.IsRune() {
		X = pool.BalanceRune
		Y = pool.BalanceAsset
	} else {
		Y = pool.BalanceRune
		X = pool.BalanceAsset
	}

	X = common.GetUncappedShare(cosmos.NewUint(uint64(virtualDepthMult)), cosmos.NewUint(10_000), X)
	Y = common.GetUncappedShare(cosmos.NewUint(uint64(virtualDepthMult)), cosmos.NewUint(10_000), Y)

	swapper, err := GetSwapper(vm.k.GetVersion())
	if err != nil {
		panic(err)
	}
	fee := swapper.CalcLiquidityFee(X, x, Y)
	if sourceCoin.IsRune() {
		fee = pool.AssetValueInRune(fee)
	}
	slip := swapper.CalcSwapSlip(X, x)
	item.fee = item.fee.Add(fee)
	item.slip = item.slip.Add(slip)
}

func (vm *SwapQueueAdvVCUR) parseRatioFromKey(key string) (uint64, error) {
	parts := strings.Split(key, "/")
	if len(parts) < 5 {
		return 0, fmt.Errorf("invalid key format")
	}
	return strconv.ParseUint(parts[len(parts)-2], 10, 64)
}

// processExpiredLimitSwaps processes limit swaps that have expired (reached TTL) up to the current block height
func (vm *SwapQueueAdvVCUR) processExpiredLimitSwaps(ctx cosmos.Context, mgr Manager) error {
	currentHeight := ctx.BlockHeight()

	// Process TTL entries for all block heights up to and including the current height
	// We check a reasonable range of past blocks to catch any expired swaps
	// Since TTL entries are stored at their expiry height, we need to check all heights <= currentHeight
	maxAge := vm.k.GetConfigInt64(ctx, constants.StreamingLimitSwapMaxAge)
	startHeight := currentHeight - maxAge // Go back maxAge blocks to catch anything we might have missed
	if startHeight < 1 {
		startHeight = 1
	}

	for height := startHeight; height <= currentHeight; height++ {
		// Get all expired transaction hashes for this block height
		expiredTxHashes, err := vm.k.GetLimitSwapTTL(ctx, height)
		if err != nil {
			// If no TTL entries exist for this block, that's normal - continue to next height
			continue
		}

		if len(expiredTxHashes) == 0 {
			continue
		}

		// Process each expired swap for this height
		for _, txHash := range expiredTxHashes {
			// Try to find the swap in the advanced queue
			// Check up to 5 possible indices for this txID
			found := false
			for index := 0; index < 5; index++ {
				if vm.k.HasAdvSwapQueueItem(ctx, txHash, index) {
					swap, err := vm.k.GetAdvSwapQueueItem(ctx, txHash, index)
					if err != nil {
						ctx.Logger().Error("fail to get expired swap item", "error", err, "txID", txHash.String(), "index", index)
						continue
					}

					// Verify this is actually a limit swap that should expire
					if swap.IsLimitSwap() {
						// Settle the expired swap
						if err := settleSwap(ctx, mgr, swap, "limit swap expired"); err != nil {
							ctx.Logger().Error("fail to settle expired limit swap", "error", err, "txID", txHash.String(), "index", index)
						}
						found = true
					}
				} else {
					// No swap found at this hash + index, break to avoid checking higher indices
					break
				}
			}

			if !found {
				ctx.Logger().Debug("expired swap not found in queue", "txID", txHash.String(), "blockHeight", height)
			}
		}

		// Remove the TTL entry (tombstone) since we've processed all expired swaps for this height
		vm.k.RemoveLimitSwapTTL(ctx, height)
	}

	return nil
}

// emitAdvSwapQueueTelemetry emits telemetry metrics for the advanced swap queue
// This method should only be called when telemetry is enabled
func (vm *SwapQueueAdvVCUR) emitAdvSwapQueueTelemetry(ctx cosmos.Context, mgr Manager, iterationCount int64, totalSwapsProcessed int64, marketSwapCount int64, limitSwapCount int64, completedSwapCount int64) {
	// Emit core metrics
	telemetry.SetGauge(float32(iterationCount), "thornode", "adv_swap_queue", "iterations_per_block")
	telemetry.SetGauge(float32(totalSwapsProcessed), "thornode", "adv_swap_queue", "total_swaps_per_block")
	telemetry.SetGauge(float32(marketSwapCount), "thornode", "adv_swap_queue", "market_swaps_per_block")
	telemetry.SetGauge(float32(limitSwapCount), "thornode", "adv_swap_queue", "limit_swaps_per_block")
	telemetry.SetGauge(float32(completedSwapCount), "thornode", "adv_swap_queue", "completed_swaps_per_block")

	// Emit total counters (these will accumulate over time)
	telemetry.IncrCounterWithLabels([]string{"thornode", "adv_swap_queue", "market_swaps_total"}, float32(marketSwapCount), nil)
	telemetry.IncrCounterWithLabels([]string{"thornode", "adv_swap_queue", "limit_swaps_total"}, float32(limitSwapCount), nil)
	telemetry.IncrCounterWithLabels([]string{"thornode", "adv_swap_queue", "swaps_completed_total"}, float32(completedSwapCount), nil)

	// Emit queue depth and trading pair metrics
	vm.emitQueueDepthTelemetry(ctx, mgr)
}

// emitQueueDepthTelemetry emits queue depth metrics per trading pair
func (vm *SwapQueueAdvVCUR) emitQueueDepthTelemetry(ctx cosmos.Context, mgr Manager) {
	pairs, _ := vm.getAssetPairs(ctx)

	// Get 1 RUNE price in USD for value conversions
	runeUSDPrice := vm.telem(mgr.Keeper().DollarsPerRune(ctx))

	totalLimitSwaps := int64(0)
	totalLimitSwapValue := cosmos.ZeroUint()

	// Iterate through each trading pair to collect metrics
	for _, pair := range pairs {
		limitSwapCount := int64(0)
		limitSwapValue := cosmos.ZeroUint()

		// Get limit swap iterator for this pair
		iter := vm.k.GetAdvSwapQueueIndexIterator(ctx, LimitSwap, pair.source, pair.target)
		if iter != nil {
			func() { // Use anonymous function to ensure defer works properly
				defer iter.Close()
				for ; iter.Valid(); iter.Next() {
					record := make([]string, 0)
					value := ProtoStrings{Value: record}
					if err := vm.k.Cdc().Unmarshal(iter.Value(), &value); err != nil {
						continue
					}

					for _, rec := range value.Value {
						lastHyphenIndex := strings.LastIndex(rec, "-")
						if lastHyphenIndex == -1 {
							continue
						}
						parts := []string{rec[:lastHyphenIndex], rec[lastHyphenIndex+1:]}
						if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
							continue
						}

						hash, err := common.NewTxID(parts[0])
						if err != nil {
							continue
						}

						index, err := strconv.Atoi(parts[1])
						if err != nil {
							continue
						}

						msg, err := vm.k.GetAdvSwapQueueItem(ctx, hash, index)
						if err != nil {
							continue
						}

						if msg.IsLimitSwap() {
							limitSwapCount++
							// Add the swap value (remaining deposit amount)
							remainingValue := common.SafeSub(msg.State.Deposit, msg.State.In)

							// Convert to RUNE value if source asset is not RUNE
							runeValue := remainingValue
							if !msg.Tx.Coins[0].Asset.IsRune() {
								// Get the pool to convert asset value to RUNE
								if pool, err := vm.k.GetPool(ctx, msg.Tx.Coins[0].Asset.GetLayer1Asset()); err == nil {
									runeValue = pool.AssetValueInRune(remainingValue)
								}
							}

							limitSwapValue = limitSwapValue.Add(runeValue)
						}
					}
				}
			}()
		}

		totalLimitSwaps += limitSwapCount
		totalLimitSwapValue = totalLimitSwapValue.Add(limitSwapValue)

		// Emit per-trading-pair metrics with labels (only if there are swaps)
		if limitSwapCount > 0 {
			labels := []metrics.Label{
				telemetry.NewLabel("source_asset", pair.source.String()),
				telemetry.NewLabel("target_asset", pair.target.String()),
			}
			telemetry.SetGaugeWithLabels(
				[]string{"thornode", "adv_swap_queue", "limit_swaps_by_pair"},
				float32(limitSwapCount),
				labels,
			)
			// Emit both RUNE and USD values for trading pair
			telemetry.SetGaugeWithLabels(
				[]string{"thornode", "adv_swap_queue", "limit_swap_value_by_pair", "rune"},
				vm.telem(limitSwapValue),
				labels,
			)
			telemetry.SetGaugeWithLabels(
				[]string{"thornode", "adv_swap_queue", "limit_swap_value_by_pair", "usd"},
				vm.telem(limitSwapValue)*runeUSDPrice,
				labels,
			)
		}
	}

	// Emit global metrics in both RUNE and USD
	telemetry.SetGauge(float32(totalLimitSwaps), "thornode", "adv_swap_queue", "total_limit_swaps")
	telemetry.SetGauge(vm.telem(totalLimitSwapValue), "thornode", "adv_swap_queue", "total_limit_swap_value", "rune")
	telemetry.SetGauge(vm.telem(totalLimitSwapValue)*runeUSDPrice, "thornode", "adv_swap_queue", "total_limit_swap_value", "usd")

	// Count market swaps currently in queue
	marketItems, err := vm.k.GetAdvSwapQueueIndex(ctx, MsgSwap{SwapType: MarketSwap})
	if err == nil {
		telemetry.SetGauge(float32(len(marketItems)), "thornode", "adv_swap_queue", "market_swaps_queued")
	}
}

// telem converts cosmos.Uint to float32 for telemetry (similar to helpers.go)
func (vm *SwapQueueAdvVCUR) telem(input cosmos.Uint) float32 {
	if !input.BigInt().IsUint64() {
		return 0
	}
	i := input.Uint64()
	return float32(i) / 100000000
}
