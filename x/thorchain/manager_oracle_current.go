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
		om.keeper.Cdc().MustUnmarshal(iterator.Value(), &price)
		om.keeper.DelPrice(ctx, price.Symbol)
	}

	return nil
}
