package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type OracleMgrVCUR struct {
	keeper keeper.Keeper
}

// newOracleMgrVCUR creates a new instance of OracleMgrVCUR
func newOracleMgrVCUR(
	keeper keeper.Keeper,
) *OracleMgrVCUR {
	return &OracleMgrVCUR{
		keeper: keeper,
	}
}

func (om *OracleMgrVCUR) BeginBlock(ctx cosmos.Context) error {
	iterator := om.keeper.GetPriceIterator(ctx)
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		var price OraclePrice
		if err := om.keeper.Cdc().Unmarshal(iterator.Value(), &price); err != nil {
			ctx.Logger().Error("failed to unmarshal price", "error", err)
			continue
		}
		om.keeper.DelPrice(ctx, price.Symbol)
	}

	return nil
}
