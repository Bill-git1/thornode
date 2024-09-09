//go:build !stagenet && !mocknet && !regtest
// +build !stagenet,!mocknet,!regtest

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
		{"thor14lkndecaw0zkzu0yq4a0qq869hrs8hh7uqjwf3", 6789165444},
		{"thor1a6l03m03qf6z0j7mwnzx2f9zryxzgf2dqcqdwe", 20821864426},
		{"thor1cz7wx9m85mzsyaaqmjd5903txudv68mdacmdtd", 2000000},
		{"thor1dw0ts754jaxn44y455aq97svgcaf69lnrmqyuq", 97488204},
		{"thor1h9phyj0rqgng3hft8pctj050ykmgawurctx5z8", 8710931612},
		{"thor1qf0ujhl4qfap5nde6r5kgys4877hc77myvjdw3", 38751978},
		{"thor1ssrm9cu7yctz3wlm63f87jveuag7tn5vzp3wal", 15069726185},
		{"thor1svfwxevnxtm4ltnw92hrqpqk4vzuzw9a4jzy04", 9720239600},
		{"thor1y8yryaf3ju5hkh6puh25ktwajstsz3exmqzhur", 62546040},
		{"thor1yknea055suzu0xhqyvq48t9uks7hsdcf4l35mr", 9716450000},
		{"thor1ymkrqd4klk2sjdqk7exufa4rlm89rp0h8n7hr2", 26832962828},
	}
	// These eleven values, obtained from GetAffiliateCollectors in an archive node at the end of thorchain-mainnet-v1,
	// sum to exactly the oversolvent 97862126317.

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
		{11970000, "bnb/btcb-1de"},
		{10368725099, "bnb/busd-bd1"},
		{1688100000, "bnb/twt-8c2"},
		{14326209633, "bsc/bnb"},
		{4745942336700, "eth/usdc-0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"},
		{173198253248, "eth/usdt-0xdac17f958d2ee523a2206206994597c13d831ec7"},
		{5973894700, "ltc/ltc"},
		{27251950916, "rune"}, // BalanceAsset of Suspended (Ragnaroked) BNB.BNB pool dropped in hardfork
		{1381124490000, "tor"},
	}

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
