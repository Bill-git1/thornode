package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	"gitlab.com/thorchain/thornode/x/thorchain/keeper"
)

// TradeMgrVCUR is VCUR implementation of slasher
type TradeMgrVCUR struct {
	keeper keeper.Keeper
}

// newTradeMgrVCUR create a new instance of Slasher
func newTradeMgrVCUR(keeper keeper.Keeper) *TradeMgrVCUR {
	return &TradeMgrVCUR{keeper: keeper}
}

func (s *TradeMgrVCUR) EndBlock(ctx cosmos.Context, keeper keeper.Keeper) error {
	// TODO: implement liquidation
	return nil
}

func (s *TradeMgrVCUR) BalanceOf(ctx cosmos.Context, asset common.Asset, addr cosmos.AccAddress) cosmos.Uint {
	asset = asset.GetTradeAsset()
	tu, err := s.keeper.GetTradeUnit(ctx, asset)
	if err != nil {
		return cosmos.ZeroUint()
	}

	tr, err := s.keeper.GetTradeAccount(ctx, addr, asset)
	if err != nil {
		return cosmos.ZeroUint()
	}

	return common.GetSafeShare(tu.Units, tu.Depth, tr.Units)
}

func (s *TradeMgrVCUR) Deposit(ctx cosmos.Context, asset common.Asset, amount cosmos.Uint, addr cosmos.AccAddress) (cosmos.Uint, error) {
	asset = asset.GetTradeAsset()
	tu, err := s.keeper.GetTradeUnit(ctx, asset)
	if err != nil {
		return cosmos.ZeroUint(), err
	}

	tr, err := s.keeper.GetTradeAccount(ctx, addr, asset)
	if err != nil {
		return cosmos.ZeroUint(), err
	}
	tr.LastAddHeight = ctx.BlockHeight()

	units := s.calcDepositUnits(tu.Units, tu.Depth, amount)
	tu.Units = tu.Units.Add(units)
	tr.Units = tr.Units.Add(units)
	tu.Depth = tu.Depth.Add(amount)

	s.keeper.SetTradeUnit(ctx, tu)
	s.keeper.SetTradeAccount(ctx, tr)

	return amount, nil
}

func (s *TradeMgrVCUR) calcDepositUnits(oldUnits, depth, add cosmos.Uint) cosmos.Uint {
	if oldUnits.IsZero() || depth.IsZero() {
		return add
	}
	if add.IsZero() {
		return cosmos.ZeroUint()
	}
	return common.GetUncappedShare(add, depth, oldUnits)
}

func (s *TradeMgrVCUR) Withdrawal(ctx cosmos.Context, asset common.Asset, amount cosmos.Uint, addr cosmos.AccAddress) (cosmos.Uint, error) {
	asset = asset.GetTradeAsset()
	tu, err := s.keeper.GetTradeUnit(ctx, asset)
	if err != nil {
		return cosmos.ZeroUint(), err
	}

	tr, err := s.keeper.GetTradeAccount(ctx, addr, asset)
	if err != nil {
		return cosmos.ZeroUint(), err
	}
	tr.LastWithdrawHeight = ctx.BlockHeight()

	assetAvailable := common.GetSafeShare(tu.Units, tu.Depth, tr.Units)
	unitsToClaim := common.GetSafeShare(amount, assetAvailable, tr.Units)

	tokensToClaim := common.GetSafeShare(unitsToClaim, tu.Units, tu.Depth)
	tu.Units = common.SafeSub(tu.Units, unitsToClaim)
	tr.Units = common.SafeSub(tr.Units, unitsToClaim)
	tu.Depth = common.SafeSub(tu.Depth, tokensToClaim)

	s.keeper.SetTradeUnit(ctx, tu)
	s.keeper.SetTradeAccount(ctx, tr)

	return tokensToClaim, nil
}
