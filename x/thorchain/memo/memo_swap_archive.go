package thorchain

import (
	"fmt"
	"strconv"
	"strings"

	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

func (p *parser) ParseSwapMemoV135() (SwapMemo, error) {
	if p.keeper == nil {
		return ParseSwapMemoV1(p.ctx, p.keeper, p.getAsset(1, true, common.EmptyAsset), p.parts)
	}

	var err error
	asset := p.getAsset(1, true, common.EmptyAsset)
	var order types.OrderType
	if strings.EqualFold(p.parts[0], "limito") || strings.EqualFold(p.parts[0], "lo") {
		order = types.OrderType_limit
	}

	// DESTADDR can be empty , if it is empty , it will swap to the sender address
	destination, refundAddress := p.getAddressAndRefundAddressWithKeeper(2, false, common.NoAddress, asset.Chain)

	// price limit can be empty , when it is empty , there is no price protection
	var slip cosmos.Uint
	var streamInterval, streamQuantity uint64
	if strings.Contains(p.get(3), "/") {
		parts := strings.SplitN(p.get(3), "/", 3)
		for i := range parts {
			if parts[i] == "" {
				parts[i] = "0"
			}
		}
		if len(parts) < 1 {
			return SwapMemo{}, fmt.Errorf("invalid streaming swap format: %s", p.get(3))
		}
		slip, err = parseTradeTarget(parts[0])
		if err != nil {
			return SwapMemo{}, fmt.Errorf("swap price limit:%s is invalid: %s", parts[0], err)
		}
		if len(parts) > 1 {
			streamInterval, err = strconv.ParseUint(parts[1], 10, 64)
			if err != nil {
				return SwapMemo{}, fmt.Errorf("failed to parse stream frequency: %s: %s", parts[1], err)
			}
		}
		if len(parts) > 2 {
			streamQuantity, err = strconv.ParseUint(parts[2], 10, 64)
			if err != nil {
				return SwapMemo{}, fmt.Errorf("failed to parse stream quantity: %s: %s", parts[2], err)
			}
		}
	} else {
		slip = p.getUintWithScientificNotation(3, false, 0)
	}

	affAddr := p.getAddressWithKeeper(4, false, common.NoAddress, common.THORChain)
	affPts := p.getUintWithMaxValue(5, false, 0, constants.MaxBasisPts)

	dexAgg := p.get(6)
	dexTargetAddress := p.get(7)
	dexTargetLimit := p.getUintWithScientificNotation(8, false, 0)

	tn := p.getTHORName(4, false, types.NewTHORName("", 0, nil), -1)

	return NewSwapMemo(asset, destination, slip, affAddr, affPts, dexAgg, dexTargetAddress, dexTargetLimit, order, streamQuantity, streamInterval, tn, refundAddress, nil, nil), p.Error()
}

func ParseSwapMemoV1(ctx cosmos.Context, keeper keeper.Keeper, asset common.Asset, parts []string) (SwapMemo, error) {
	var err error
	var order types.OrderType
	if len(parts) < 2 {
		return SwapMemo{}, fmt.Errorf("not enough parameters")
	}
	// DESTADDR can be empty , if it is empty , it will swap to the sender address
	destination := common.NoAddress
	affAddr := common.NoAddress
	affPts := uint64(0)
	if len(parts) > 2 {
		if len(parts[2]) > 0 {
			if keeper == nil {
				destination, err = common.NewAddress(parts[2])
			} else {
				destination, err = FetchAddress(ctx, keeper, parts[2], asset.Chain)
			}
			if err != nil {
				return SwapMemo{}, err
			}
		}
	}
	// price limit can be empty , when it is empty , there is no price protection
	slip := cosmos.ZeroUint()
	if len(parts) > 3 && len(parts[3]) > 0 {
		slip, err = cosmos.ParseUint(parts[3])
		if err != nil {
			return SwapMemo{}, fmt.Errorf("swap price limit:%s is invalid", parts[3])
		}
	}

	if len(parts) > 5 && len(parts[4]) > 0 && len(parts[5]) > 0 {
		if keeper == nil {
			affAddr, err = common.NewAddress(parts[4])
		} else {
			affAddr, err = FetchAddress(ctx, keeper, parts[4], common.THORChain)
		}
		if err != nil {
			return SwapMemo{}, err
		}
		affPts, err = strconv.ParseUint(parts[5], 10, 64)
		if err != nil {
			return SwapMemo{}, err
		}
	}

	return NewSwapMemo(asset, destination, slip, affAddr, cosmos.NewUint(affPts), "", "", cosmos.ZeroUint(), order, 0, 0, types.NewTHORName("", 0, nil), "", nil, nil), nil
}
