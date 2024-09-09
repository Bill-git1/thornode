//go:build regtest
// +build regtest

package thorchain

import (
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
)

func migrateStoreV136(ctx cosmos.Context, mgr *Mgrs) {
	defer func() {
		if err := recover(); err != nil {
			ctx.Logger().Error("fail to migrate store to v136", "error", err)
		}
	}()

	// #2054, clean up Affiliate Collector Module and Pool Module oversolvencies after v2 hardfork.

	// https://thornode-v2.ninerealms.com/thorchain/invariant/affiliate_collector?height=17562001
	affCols := []struct {
		address string
		amount  uint64
	}{
		{"tthor14lkndecaw0zkzu0yq4a0qq869hrs8hh7chr7s5", 6789165444}, // tthor version of thor14lkndecaw0zkzu0yq4a0qq869hrs8hh7uqjwf3
	}
	// Single Owner for regression testing.

	for i := range affCols {
		accAddr, err := cosmos.AccAddressFromBech32(affCols[i].address)
		if err != nil {
			ctx.Logger().Error("failed to convert to acc address", "error", err, "addr", affCols[i].address)
			continue
		}
		affCol, err := mgr.Keeper().GetAffiliateCollector(ctx, accAddr)
		if err != nil {
			ctx.Logger().Error("failed to get affiliate collector", "error", err, "addr", affCols[i].address)
			continue
		}
		affCol.RuneAmount = cosmos.NewUint(affCols[i].amount).Add(affCol.RuneAmount)
		mgr.Keeper().SetAffiliateCollector(ctx, affCol)
	}

	// https://thornode-v2.ninerealms.com/thorchain/invariant/asgard?height=17562001
	poolOversol := []struct {
		amount uint64
		asset  string
	}{
		{1588356075, "bnb/bnb"},
		{5973894700, "ltc/ltc"},
		{27251950916, "rune"}, // BalanceAsset of Suspended (Ragnaroked) BNB.BNB pool dropped in hardfork
	}
	// Three coins for regression testing (BNB synth, non-BNB synth, RUNE).

	var coinsToSend, coinsToBurn common.Coins
	for i := range poolOversol {
		amount := cosmos.NewUint(poolOversol[i].amount)
		asset, err := common.NewAsset(poolOversol[i].asset)
		if err != nil {
			ctx.Logger().Error("failed to create asset", "error", err, "asset", poolOversol[i].asset)
			continue
		}
		coin := common.NewCoin(asset, amount)

		// Attempt to burn Ragnaroked (worthless) BNB assets directly, rather than transferring them.
		if asset.Chain.String() == "BNB" {
			coinsToBurn = append(coinsToBurn, coin)
		} else {
			coinsToSend = append(coinsToSend, coin)
		}
	}

	// Send the non-BNB coins to the Reserve Module.
	if len(coinsToSend) > 0 {
		if err := mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ReserveName, coinsToSend); err != nil {
			ctx.Logger().Error("failed to migrate pool module oversolvencies to reserve", "error", err)
		}
	}

	// Send the non-BNB coins to the Minter Module for burning, then if successful burn them.
	if len(coinsToBurn) > 0 {
		if err := mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ModuleName, coinsToBurn); err != nil {
			ctx.Logger().Error("failed to migrate bnb coins to minter for burning", "error", err)
		} else {
			for i := range coinsToBurn {
				if err := mgr.Keeper().BurnFromModule(ctx, ModuleName, coinsToBurn[i]); err != nil {
					ctx.Logger().Error("failed to burn bnb coin from minter module", "error", err, "coin", coinsToBurn[i].String())
				}
			}
		}
	}
}
