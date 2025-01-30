package thorchain

import (
	"strings"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

func fuzzyAssetMatchV3_0_0(ctx cosmos.Context, keeper keeper.Keeper, origAsset common.Asset) common.Asset {
	asset := origAsset.GetLayer1Asset()
	// if it's already an exact match with successfully-added liquidity, return it immediately
	pool, err := keeper.GetPool(ctx, asset)
	if err != nil {
		return origAsset
	}
	// Only check BalanceRune after checking the error so that no panic if there were an error.
	if !pool.BalanceRune.IsZero() {
		return origAsset
	}

	parts := strings.Split(asset.Symbol.String(), "-")
	hasNoSymbol := len(parts) < 2 || len(parts[1]) == 0
	var symbol string
	if !hasNoSymbol {
		symbol = strings.ToLower(parts[1])
	}
	winner := NewPool()
	// if no asset found, return original asset
	winner.Asset = origAsset
	iterator := keeper.GetPoolIterator(ctx)
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		if err = keeper.Cdc().Unmarshal(iterator.Value(), &pool); err != nil {
			ctx.Logger().Error("fail to fetch pool", "asset", asset, "err", err)
			continue
		}

		// check chain match
		if !asset.Chain.Equals(pool.Asset.Chain) {
			continue
		}

		// check ticker match
		if !asset.Ticker.Equals(pool.Asset.Ticker) {
			continue
		}

		// check if no symbol given (ie "USDT" or "USDT-")
		if hasNoSymbol {
			// Use LTE rather than LT so this function can only return origAsset or a match
			if winner.BalanceRune.LTE(pool.BalanceRune) {
				winner = pool
			}
			continue
		}

		if strings.HasSuffix(strings.ToLower(pool.Asset.Symbol.String()), symbol) {
			// Use LTE rather than LT so this function can only return origAsset or a match
			if winner.BalanceRune.LTE(pool.BalanceRune) {
				winner = pool
			}
			continue
		}
	}
	winner.Asset.Synth = origAsset.Synth
	return winner.Asset
}
