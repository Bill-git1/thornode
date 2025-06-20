package thorchain

import (
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"

	"github.com/jinzhu/copier"
)

// SwapQueueAdvVCUR is going to manage the swaps queue
type SwapQueueAdvVCUR struct {
	k          keeper.Keeper
	limitSwaps swapItems
}

// newSwapQueueAdvVCUR create a new vault manager
func newSwapQueueAdvVCUR(k keeper.Keeper) *SwapQueueAdvVCUR {
	return &SwapQueueAdvVCUR{k: k, limitSwaps: make(swapItems, 0)}
}

// FetchQueue - grabs all swap queue items from the kvstore and returns them
func (vm *SwapQueueAdvVCUR) FetchQueue(ctx cosmos.Context, mgr Manager, pairs tradePairs, pools Pools) (swapItems, error) { // nolint
	items := make(swapItems, 0)

	// if the network is doing a pool cycle, no swaps are executed this
	// block. This is because the change of active pools can cause the
	// mechanism to index/encode the selected pools/trading pairs that need to
	// be checked (proc).
	poolCycle := mgr.Keeper().GetConfigInt64(ctx, constants.PoolCycle)
	if ctx.BlockHeight()%poolCycle == 0 {
		return nil, nil
	}

	proc, err := vm.k.GetAdvSwapQueueProcessor(ctx)
	if err != nil {
		return nil, err
	}

	todo, ok := vm.convertProcToAssetArrays(proc, pairs)
	if !ok {
		// number of pools has changed from the previous block. Skip processing
		// swaps for this block. This is due to our total pair list (aka
		// reference table) changing underneath our feet.
		return nil, nil
	}

	val := vm.k.GetConfigInt64(ctx, constants.EnableAdvSwapQueue)

	// get market swap
	hashes, err := vm.k.GetAdvSwapQueueIndex(ctx, MsgSwap{SwapType: MarketSwap})
	if err != nil {
		return nil, err
	}
	for _, hash := range hashes {
		msg, err := vm.k.GetAdvSwapQueueItem(ctx, hash)
		if err != nil {
			ctx.Logger().Error("fail to fetch adv swap item", "error", err)
			continue
		}

		// skip processing limit swaps if EnableAdvSwapQueue is set to market only mode
		if types.AdvSwapQueueMode(val) == types.AdvSwapQueueModeMarketOnly && msg.SwapType == LimitSwap {
			continue
		}

		items = append(items, swapItem{
			msg:   msg,
			index: 0,
			fee:   cosmos.ZeroUint(),
			slip:  cosmos.ZeroUint(),
		})
	}

	for _, pair := range todo {
		newItems, done := vm.discoverLimitSwaps(ctx, pair, pools)
		items = append(items, newItems...)
		if done {
			break
		}
	}

	return items, nil
}

func (vm *SwapQueueAdvVCUR) discoverLimitSwaps(ctx cosmos.Context, pair tradePair, pools Pools) (swapItems, bool) {
	items := make(swapItems, 0)
	done := false

	iter := vm.k.GetAdvSwapQueueIndexIterator(ctx, LimitSwap, pair.source, pair.target)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		ratio, err := vm.parseRatioFromKey(string(iter.Key()))
		if err != nil {
			ctx.Logger().Error("fail to parse ratio", "key", string(iter.Key()), "error", err)
			continue
		}

		// if a fee-less swap doesn't meet the ratio requirement, then we
		// can be assured that all adv swap items in this index and every
		// index there after will not be met.
		if ok := vm.checkFeelessSwap(pools, pair, ratio); !ok {
			done = true
			break
		}

		record := make([]string, 0)
		value := ProtoStrings{Value: record}
		if err := vm.k.Cdc().Unmarshal(iter.Value(), &value); err != nil {
			ctx.Logger().Error("fail to fetch indexed txn hashes", "error", err)
			continue
		}

		for i, rec := range value.Value {
			hash, err := common.NewTxID(rec)
			if err != nil {
				ctx.Logger().Error("fail to parse tx hash", "error", err)
				continue
			}
			msg, err := vm.k.GetAdvSwapQueueItem(ctx, hash)
			if err != nil {
				ctx.Logger().Error("fail to fetch msg swap", "error", err)
				continue
			}

			// do a swap, including swap fees and outbound fees. If this passes attempt the swap.
			if ok := vm.checkWithFeeSwap(ctx, pools, msg); !ok {
				continue
			}

			items = append(items, swapItem{
				msg:   msg,
				index: i,
				fee:   cosmos.ZeroUint(),
				slip:  cosmos.ZeroUint(),
			})
		}
	}
	return items, done
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

func (vm *SwapQueueAdvVCUR) checkWithFeeSwap(ctx cosmos.Context, pools Pools, msg MsgSwap) bool {
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

	// txout manager has fees as well, that might fail the swap. That is NOT
	// accounted for here, because its prob more work computationally than its
	// worth to check (?).

	return emit.GT(target.Amount)
}

func (vm *SwapQueueAdvVCUR) getRatio(input, output cosmos.Uint) cosmos.Uint {
	if output.IsZero() {
		return cosmos.ZeroUint()
	}
	return input.MulUint64(1e8).Quo(output)
}

// converts a proc, cosmos.Uint, into a series of selected pairs from the pairs
// input (ie asset pairs that need to be check for executable swaps)
func (vm *SwapQueueAdvVCUR) convertProcToAssetArrays(proc []bool, pairs tradePairs) (tradePairs, bool) {
	result := make(tradePairs, 0)
	if len(proc) != len(pairs) {
		return result, false
	}
	for i, b := range proc {
		if len(pairs)-1 < i {
			break // pairs length < bin length
		}
		if b {
			result = append(result, pairs[i])
		}
	}
	return result, true
}

// converts a list of selected pairs from a list of total pairs, to be represented as a uint64
func (vm *SwapQueueAdvVCUR) convertAssetArraysToProc(toProc, pairs tradePairs) []bool {
	builder := make([]bool, len(pairs))
	for i, pair := range pairs {
		builder[i] = false
		for _, p := range toProc {
			if pair.Equals(p) {
				builder[i] = true
				break
			}
		}
	}
	return builder
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

func (vm *SwapQueueAdvVCUR) AddSwapQueueItem(ctx cosmos.Context, msg MsgSwap) error {
	// If advanced swap queue is in market-only mode, force all swaps to be market swaps
	val := vm.k.GetConfigInt64(ctx, constants.EnableAdvSwapQueue)
	if types.AdvSwapQueueMode(val) == types.AdvSwapQueueModeMarketOnly {
		msg.SwapType = MarketSwap
	}
	if err := vm.k.SetAdvSwapQueueItem(ctx, msg); err != nil {
		ctx.Logger().Error("fail to add swap item", "error", err)
		return err
	}
	if msg.SwapType == LimitSwap {
		if err := vm.k.SetAdvSwapQueueIndex(ctx, msg); err != nil {
			ctx.Logger().Error("fail to add limit swap index", "error", err)
			return err
		}
		vm.limitSwaps = append(vm.limitSwaps, swapItem{
			msg:   msg,
			index: 0,
			fee:   cosmos.ZeroUint(),
			slip:  cosmos.ZeroUint(),
		})
	}
	return nil
}

// EndBlock trigger the real swap to be processed
func (vm *SwapQueueAdvVCUR) EndBlock(ctx cosmos.Context, mgr Manager) error {
	handler := NewInternalHandler(mgr)

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

	todo := make(tradePairs, 0)
	pairs, pools := vm.getAssetPairs(ctx)

	swaps, err := vm.FetchQueue(ctx, mgr, pairs, pools)
	if err != nil {
		ctx.Logger().Error("fail to fetch swap queue from store", "error", err)
		return err
	}

	// pull new limit swaps added this block (if not already added)
	for _, item := range vm.limitSwaps {
		if !swaps.HasItem(item.msg.Tx.ID) {
			swaps = append(swaps, item)
		}
	}
	vm.limitSwaps = make(swapItems, 0)

	swaps, err = vm.scoreMsgs(ctx, swaps, synthVirtualDepthMult)
	if err != nil {
		ctx.Logger().Error("fail to fetch swap items", "error", err)
		// continue, don't exit, just do them out of order (instead of not at all)
	}
	swaps = swaps.Sort()

	refund := func(msg MsgSwap, err error) {
		ctx.Logger().Error("fail to execute swap", "msg", msg.Tx.String(), "error", err)

		var refundErr error

		// Get the full ObservedTx from the TxID, for the vault ObservedPubKey to first try to refund from.
		voter, voterErr := mgr.Keeper().GetObservedTxInVoter(ctx, msg.Tx.ID)
		if voterErr == nil && !voter.Tx.IsEmpty() {
			refundErr = refundTx(ctx, ObservedTx{Tx: msg.Tx, ObservedPubKey: voter.Tx.ObservedPubKey}, mgr, CodeSwapFail, err.Error(), "")
		} else {
			// If the full ObservedTx could not be retrieved, proceed with just the MsgSwap's Tx (no ObservedPubKey).
			ctx.Logger().Error("fail to get non-empty observed tx", "error", voterErr)
			refundErr = refundTx(ctx, ObservedTx{Tx: msg.Tx}, mgr, CodeSwapFail, err.Error(), "")
		}

		if nil != refundErr {
			ctx.Logger().Error("fail to refund swap", "error", err)
		}
	}

	for i := int64(0); i < vm.getTodoNum(int64(len(swaps)), minSwapsPerBlock, maxSwapsPerBlock); i++ {
		pick := swaps[i]
		var msg, affiliateSwap MsgSwap
		if err := copier.Copy(&msg, &pick.msg); err != nil {
			ctx.Logger().Error("fail copy msg", "msg", msg.Tx.String(), "error", err)
			continue
		}
		if !msg.AffiliateBasisPoints.IsZero() && msg.AffiliateAddress.IsChain(common.THORChain) {
			affiliateAmt := common.GetSafeShare(
				msg.AffiliateBasisPoints,
				cosmos.NewUint(10000),
				msg.Tx.Coins[0].Amount,
			)
			msg.Tx.Coins[0].Amount = common.SafeSub(msg.Tx.Coins[0].Amount, affiliateAmt)

			affiliateSwap = *NewMsgSwap(
				msg.Tx,
				common.RuneAsset(),
				msg.AffiliateAddress,
				cosmos.ZeroUint(),
				common.NoAddress,
				cosmos.ZeroUint(),
				"",
				"", nil,
				MarketSwap,
				0, 0, msg.Signer,
			)
			if affiliateSwap.Tx.Coins[0].Amount.GTE(affiliateAmt) {
				affiliateSwap.Tx.Coins[0].Amount = affiliateAmt
			}
		}

		// make the primary swap
		_, err := handler(ctx, &msg)
		if err != nil {
			switch pick.msg.SwapType {
			case MarketSwap:
				refund(pick.msg, err)
			case LimitSwap:
				// if swap fails due to not enough outbound amounts, don't
				// remove the adv swap item and try again later
				if strings.Contains(err.Error(), "less than price limit") || strings.Contains(err.Error(), "outbound amount does not meet requirements") {
					continue
				}
				refund(pick.msg, err)
			default:
				// non-supported adv swap item, refund
				refund(pick.msg, err)
			}
		} else {
			todo = todo.findMatchingTrades(genTradePair(msg.Tx.Coins[0].Asset, msg.TargetAsset), pairs)
			if !affiliateSwap.Tx.IsEmpty() {
				// if asset sent in is native rune, no need
				if affiliateSwap.Tx.Coins[0].IsRune() {
					toAddress, err := msg.AffiliateAddress.AccAddress()
					if err != nil {
						ctx.Logger().Error("fail to convert address into AccAddress", "msg", msg.AffiliateAddress, "error", err)
						continue
					}
					// since native transaction fee has been charged to inbound from address, thus for affiliated fee , the network doesn't need to charge it again
					coin := common.NewCoin(common.RuneAsset(), affiliateSwap.Tx.Coins[0].Amount)
					sdkErr := mgr.Keeper().SendFromModuleToAccount(ctx, AsgardName, toAddress, common.NewCoins(coin))
					if sdkErr != nil {
						ctx.Logger().Error("fail to send native asset to affiliate", "msg", msg.AffiliateAddress, "error", err, "asset", coin.Asset)
					}
				} else {
					// make the affiliate fee swap
					_, err := handler(ctx, &affiliateSwap)
					if err != nil {
						ctx.Logger().Error("fail to execute affiliate swap", "msg", affiliateSwap.Tx.String(), "error", err)
					}
				}
			}
		}
		if err := vm.k.RemoveAdvSwapQueueItem(ctx, pick.msg.Tx.ID); err != nil {
			ctx.Logger().Error("fail to remove adv swap item", "msg", pick.msg.Tx.String(), "error", err)
		}
	}

	if err := vm.k.SetAdvSwapQueueProcessor(ctx, vm.convertAssetArraysToProc(todo, pairs)); err != nil {
		ctx.Logger().Error("fail to set book processor", "error", err)
	}

	return nil
}

// getTodoNum - determine how many swaps to do.
func (vm *SwapQueueAdvVCUR) getTodoNum(queueLen, minSwapsPerBlock, maxSwapsPerBlock int64) int64 {
	// Do half the length of the queue. Unless...
	//	1. The queue length is greater than maxSwapsPerBlock
	//  2. The queue length is less than minSwapsPerBlock
	todo := queueLen / 2
	if minSwapsPerBlock >= queueLen {
		todo = queueLen
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
