//go:build regtest
// +build regtest

package thorchain

import (
	_ "embed"
	"encoding/json"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// (No thor -> tthor conversion necessary for regression test performance check only,
// since many of the addresses are for external chains anyway.)
//
//go:embed manager_store_v136_clout.json
var jsonV136Clouts []byte

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

	// #2012, refund of BNB Ragnarok synth-burn Reserve RUNE as per ADR-015.
	// Amounts are floored to 1e-8 precision.
	// Of the 46 specified addresses, the two identical
	// thor1kv4jdrnekdfqcajez78d2drvhp3ep6akx6865j
	// amounts are merged as specified (BNB/BTCB and BNB/ETH),
	// and the Pool Module address's 2,443.07558984 RUNE for stuck synths is removed
	// ( thor1g98cy3n9mmjrpn0sxmn63lztelera37n8n67c0 ),
	// leaving 43 distinct transfers of 21,749.75450656 RUNE total.
	refundDetails := []struct {
		AddressString string
		AmountUint64  uint64
	}{
		// For regression tests, all bech32 addresses converted to tthor prefixes.
		{"tthor1vsnj373g9uqke6z5fdpps8p8udrls3a8lf9m7l", 11716154162},
		{"tthor1hkyzhmf85kfl87mgjdh42yplgumj7gsrmmr50q", 4701295785},
		{"tthor1tcr7q0tmj3863d03w6yfc2w5ghzfqup6s97gnq", 4440625518},
		{"tthor1rgdhwaj3nr937ftlnrh7sdw38d9ene6ymz595u", 3677532492},
		{"tthor13uw2awf9k2nqnmzqz8ugckfljtwzngqfqd8gwv", 2531525404},
		{"tthor1er9ys0ymy9lt4tet77ztaexh6dp3xdw5lfjxch", 2385425454},
		{"tthor1fq3vlugshpg90lewgcf44nux2qrxk855pfjqcs", 2277559930},
		{"tthor19gu67axrmeeknl2k2tf38h92vek3suwnkpzytz", 1890543217},
		{"tthor1afs68s095v58tc6e560w2xfmzfy4tdznqrq6uu", 1852327786},
		{"tthor1zl6el90vw3ncjzh28mcautrkjn9jagreuz0dp0", 1154715724},
		{"tthor1fq9sculy0ej2p9sa52e304m0hq96edgq4he4d7", 1100925415},
		{"tthor12z9pwgw50pepl7mazv4nf2868c6ke882edv77r", 808502228779},
		{"tthor1mq8mzl9272dzhee3flkndyz2lux9nvzqr4zlsm", 230920778872},
		{"tthor1uunxme33cufhtcm00ygkfx6th3vvjmatgs6d26", 117358353467},
		{"tthor1eyvhjlezafz36cz3vev60u6p2zn24dhuplalhv", 69322921547},
		{"tthor1tp8l7ygmnhmny9dknjjj8d65x49t28nuq0yfg0", 65516063210},
		{"tthor1s9cv0mcp80lehzyjhjtkcaq2yjc4ez369s38g5", 50404959208},
		{"tthor17kq48hspxnaku6pd4crj9hnywjcpehvx5w2efx", 39003121809},
		{"tthor125rz8fe8spjhjsknfmejddtxfh44v9jntpw8pr", 23263527044},
		{"tthor10rthwfd27zhs3lrk2ck8gv2yx3zgz3wtcm60qz", 14739474681},
		{"tthor10j3d4dgq0sekw3vywtt8kgeeympujsmchvnlwc", 12822773036},
		{"tthor1kv4jdrnekdfqcajez78d2drvhp3ep6akzdk2dh", 15960846102},
		{"tthor1l7p2rlckkclsgauall0v4hfy94rdpk2jr6z4ek", 3295477653},
		{"tthor1mge35algk7x6r4gygauvxcwn5nhqcchme24vax", 2142333731},
		{"tthor1nc8kha8hn74h0w7zdyclncq5h039wecga76rak", 1136748510},
		{"tthor1x4d5g75v67affmr9qjuu75swjsgme4jsu3rwc6", 117441744148},
		{"tthor1dmgdpkqngg3amua5hffjcharngzdp4wv4jvjt4", 44782043380},
		{"tthor1atev7k3xzsqsenrwjht3k3r70t9mtz0pss3rrg", 35558585112},
		{"tthor1p8as4v08l6t04tahqgajgw5ycj2a5k3yvuxje6", 28083870141},
		{"tthor1fptyt9rkukvn80sda7q8e446ft2lp6jkhlxhc7", 23353164702},
		{"tthor1vqd6djdqn4hlrquxuwz2na3mac8qm8qg27dwc4", 17746077327},
		{"tthor155cljp4fcarppqupzh9s3uvu6525hd5g0p67k5", 4493133386},
		{"tthor1wudr3yyc0d436k8cml7c8dvtqxwsvwlz0mes74", 1329048855},
		{"tthor1j47es49m8llprlpcxrt3a2hqyhperwc702txf7", 46008641784},
		{"tthor1rpu6ndvg0r25y6xqdl2svqlwglc3yhh32m0zuf", 41040827546},
		{"tthor140dy78lz5vv84gknn2wdwv7ed4nhs2kacer4h0", 3634392367},
		{"tthor1tu9xulcjw76mp7ky7v9l2hrwk2chvgla9n4g6c", 1288358184},
		{"tthor196svmy67vm4ya3rnlfnjq4sc9u5c5sku80vrsq", 1275639579},
		{"tthor1dsk8smfqt6xxjs8lzuy4hpxrh0wfklf50er89u", 1116636690},
		{"tthor19pkfd9ygch6dfa067ddn0fwul8g3x0syryhr4x", 1106152983},
		{"tthor1a8m2shzvyya0ckvq76fxsnd5800hm0uxz9d5gd", 1046501504},
		{"tthor1rzxvqhepnqqcn7973jp4y0ygasr709gpkd0pxt", 312191389293},
		{"tthor1sth9gz5asawsvfrq08ag7wzwqqlrjxl0alhva7", 1361005139},
	}

	// Sanity checks.
	var sum uint64
	ok := true
	for i := range refundDetails {
		sum += refundDetails[i].AmountUint64
	}
	if len(refundDetails) != 43 {
		ctx.Logger().Error("store migration recipient number was not 43", "number", len(refundDetails))
		ok = false
	}
	// ~24,192.83 RUNE minus the Pool Module's ~2,443.08 RUNE ~= 21,749.75 RUNE
	if sum != 2174975450656 {
		ctx.Logger().Error("store migration recipient sum was not 2174975450656", "sum", sum)
		ok = false
	}

	if ok {
		for i := range refundDetails {
			recipient, err := cosmos.AccAddressFromBech32(refundDetails[i].AddressString)
			if err != nil {
				ctx.Logger().Error("error parsing address in store migration", "error", err)
				continue
			}
			amount := cosmos.NewUint(refundDetails[i].AmountUint64)
			refundCoins := common.NewCoins(common.NewCoin(common.RuneAsset(), amount))
			if err := mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, recipient, refundCoins); err != nil {
				ctx.Logger().Error("fail to store migration transfer RUNE from Reserve to recipient", "error", err, "recipient", recipient, "amount", amount)
			}
		}
	}

	// #2065, export of dropped clouts over 1000 RUNE from the end of thorchain-mainnet-v1

	var clouts []SwapperClout
	if err := json.Unmarshal(jsonV136Clouts, &clouts); err != nil {
		ctx.Logger().Error("error on unmarshal of clouts json", "error", err)
		return
	}

	cloutsLength := len(clouts)
	if cloutsLength != 1616 {
		ctx.Logger().Error("clouts not the expected number of 1616", "length", cloutsLength)
		return
	}

	for i := range clouts {
		if cosmos.NewUint(1000 * common.One).GTE(clouts[i].Score) {
			ctx.Logger().Error("json clout not over 1000 rune score", "clout", clouts[i].String())
			continue
		}

		clout, err := mgr.Keeper().GetSwapperClout(ctx, clouts[i].Address)
		if err != nil {
			ctx.Logger().Error("error on get swapper clout", "error", err)
			continue
		}

		if clouts[i].LastSpentHeight > clout.LastSpentHeight {
			clout.LastSpentHeight = clouts[i].LastSpentHeight
		}
		if clouts[i].LastReclaimHeight > clout.LastReclaimHeight {
			clout.LastReclaimHeight = clouts[i].LastReclaimHeight
		}
		clout.Score = clout.Score.Add(clouts[i].Score)
		clout.Reclaimed = clout.Reclaimed.Add(clouts[i].Reclaimed)
		clout.Spent = clout.Spent.Add(clouts[i].Spent)

		if err := mgr.Keeper().SetSwapperClout(ctx, clout); err != nil {
			ctx.Logger().Error("error on set swapper clout", "error", err)
		}
	}
}
