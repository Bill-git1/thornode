package thorchain

import (
	"strconv"

	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/mimir"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

func ParseAffiliateBasisPoints(ctx cosmos.Context, keeper keeper.Keeper, affBasisPoints string) (cosmos.Uint, error) {
	maxAffFeeBasisPoints := int64(10_000)
	if keeper != nil {
		mimirMaxAffFeeBasisPoints := mimir.NewAffiliateFeeBasisPointsMax().FetchValue(ctx, keeper)
		if mimirMaxAffFeeBasisPoints >= 0 && mimirMaxAffFeeBasisPoints <= 10_000 {
			maxAffFeeBasisPoints = mimirMaxAffFeeBasisPoints
		}
	}
	pts, err := strconv.ParseUint(affBasisPoints, 10, 64)
	if err != nil {
		return cosmos.ZeroUint(), err
	}
	if pts > uint64(maxAffFeeBasisPoints) {
		pts = uint64(maxAffFeeBasisPoints)
	}
	return cosmos.NewUint(pts), nil
}
