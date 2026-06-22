package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type OracleMgr struct {
	keeper keeper.Keeper
}

// newOracleMgr creates a new instance of OracleMgr
func newOracleMgr(
	keeper keeper.Keeper,
) *OracleMgr {
	return &OracleMgr{
		keeper: keeper,
	}
}

func (om *OracleMgr) BeginBlock(ctx cosmos.Context) error {
	iterator := om.keeper.GetPriceIterator(ctx)
	defer iterator.Close()

	// Collect keys to delete after iteration so a corrupted entry (whose
	// Symbol cannot be recovered via Unmarshal) is still removed from the
	// store via its raw key. Without this, malformed entries would persist
	// forever and produce per-block error logs.
	var keys [][]byte
	for ; iterator.Valid(); iterator.Next() {
		key := append([]byte(nil), iterator.Key()...)
		keys = append(keys, key)
		var price OraclePrice
		if err := om.keeper.Cdc().Unmarshal(iterator.Value(), &price); err != nil {
			ctx.Logger().Error("failed to unmarshal price, removing malformed entry by key", "key", key, "error", err)
		}
	}

	for _, key := range keys {
		om.keeper.DeleteKey(ctx, key)
	}

	return nil
}
