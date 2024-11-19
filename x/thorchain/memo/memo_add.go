package thorchain

import (
	"strconv"
	"strings"

	"gitlab.com/thorchain/thornode/v3/common"
	cosmos "gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type AddLiquidityMemo struct {
	MemoBase
	Address              common.Address
	AffiliateAddress     common.Address
	AffiliateBasisPoints cosmos.Uint
}

func (m AddLiquidityMemo) GetDestination() common.Address { return m.Address }

func (m AddLiquidityMemo) String() string {
	txType := m.TxType.String()
	if m.TxType == TxAdd {
		txType = "+"
	}

	args := []string{
		txType,
		m.Asset.String(),
		m.Address.String(),
		m.AffiliateAddress.String(),
		m.AffiliateBasisPoints.String(),
	}

	last := 2
	if !m.Address.IsEmpty() {
		last = 3
	}
	if !m.AffiliateAddress.IsEmpty() {
		last = 5
	}

	return strings.Join(args[:last], ":")
}

func NewAddLiquidityMemo(asset common.Asset, addr, affAddr common.Address, affPts cosmos.Uint) AddLiquidityMemo {
	return AddLiquidityMemo{
		MemoBase:             MemoBase{TxType: TxAdd, Asset: asset},
		Address:              addr,
		AffiliateAddress:     affAddr,
		AffiliateBasisPoints: affPts,
	}
}

func (p *parser) ParseAddLiquidityMemo() (AddLiquidityMemo, error) {
	if p.keeper == nil {
		return ParseAddLiquidityMemoV1(p.ctx, p.keeper, p.getAsset(1, true, common.EmptyAsset), p.parts)
	}

	asset := p.getAsset(1, true, common.EmptyAsset)
	addr := p.getAddressWithKeeper(2, false, common.NoAddress, asset.Chain)
	affChain := common.THORChain
	if asset.IsSyntheticAsset() {
		// For a Savers add, an Affiliate THORName must be resolved
		// to an address for the Layer 1 Chain of the synth to succeed.
		affChain = asset.GetLayer1Asset().GetChain()
	}
	affAddr := p.getAddressWithKeeper(3, false, common.NoAddress, affChain)
	affPts := p.getUintWithMaxValue(4, false, 0, constants.MaxBasisPts)
	return NewAddLiquidityMemo(asset, addr, affAddr, affPts), p.Error()
}

func ParseAddLiquidityMemoV1(ctx cosmos.Context, keeper keeper.Keeper, asset common.Asset, parts []string) (AddLiquidityMemo, error) {
	var err error
	addr := common.NoAddress
	affAddr := common.NoAddress
	affPts := uint64(0)
	if len(parts) >= 3 && len(parts[2]) > 0 {
		if keeper == nil {
			addr, err = common.NewAddress(parts[2])
		} else {
			addr, err = FetchAddress(ctx, keeper, parts[2], asset.Chain)
		}
		if err != nil {
			return AddLiquidityMemo{}, err
		}
	}

	if len(parts) > 4 && len(parts[3]) > 0 && len(parts[4]) > 0 {
		if keeper == nil {
			affAddr, err = common.NewAddress(parts[3])
		} else {
			affAddr, err = FetchAddress(ctx, keeper, parts[3], common.THORChain)
		}
		if err != nil {
			return AddLiquidityMemo{}, err
		}
		affPts, err = strconv.ParseUint(parts[4], 10, 64)
		if err != nil {
			return AddLiquidityMemo{}, err
		}
	}
	return NewAddLiquidityMemo(asset, addr, affAddr, cosmos.NewUint(affPts)), nil
}
