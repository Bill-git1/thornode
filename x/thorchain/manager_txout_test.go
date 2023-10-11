package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

type TestCalcKeeper struct {
	keeper.KVStoreDummy
	value map[int64]cosmos.Uint
	mimir map[string]int64
}

func (k *TestCalcKeeper) GetPool(ctx cosmos.Context, asset common.Asset) (types.Pool, error) {
	pool := NewPool()
	pool.Asset = asset
	pool.BalanceRune = cosmos.NewUint(90527581399649)
	pool.BalanceAsset = cosmos.NewUint(1402011488988)
	return pool, nil
}

func (k *TestCalcKeeper) GetMimir(ctx cosmos.Context, key string) (int64, error) {
	return k.mimir[key], nil
}

func (k *TestCalcKeeper) GetConfigInt64(ctx cosmos.Context, key constants.ConstantName) int64 {
	val, err := k.GetMimir(ctx, key.String())
	if val < 0 || err != nil {
		val = k.GetConstants().GetInt64Value(key)
	}
	return val
}

func (k *TestCalcKeeper) GetTxOutValue(ctx cosmos.Context, height int64) (cosmos.Uint, error) {
	val, ok := k.value[height]
	if !ok {
		return cosmos.ZeroUint(), nil
	}
	return val, nil
}
