//go:build !stagenet && !mocknet && !regtest
// +build !stagenet,!mocknet,!regtest

package thorchain

import (
	_ "embed"
	"encoding/json"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

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
		{"thor1vsnj373g9uqke6z5fdpps8p8udrls3a8m75t86", 11716154162},
		{"thor1hkyzhmf85kfl87mgjdh42yplgumj7gsrlvjyk9", 4701295785},
		{"thor1tcr7q0tmj3863d03w6yfc2w5ghzfqup65j0c29", 4440625518},
		{"thor1rgdhwaj3nr937ftlnrh7sdw38d9ene6yl494de", 3677532492},
		{"thor13uw2awf9k2nqnmzqz8ugckfljtwzngqfy6kchf", 2531525404},
		{"thor1er9ys0ymy9lt4tet77ztaexh6dp3xdw5m7rkpj", 2385425454},
		{"thor1fq3vlugshpg90lewgcf44nux2qrxk85597rsp4", 2277559930},
		{"thor19gu67axrmeeknl2k2tf38h92vek3suwnjkn5j8", 1890543217},
		{"thor1afs68s095v58tc6e560w2xfmzfy4tdzny5329e", 1852327786},
		{"thor1zl6el90vw3ncjzh28mcautrkjn9jagrec47ac2", 1154715724},
		{"thor1fq9sculy0ej2p9sa52e304m0hq96edgq3qg95m", 1100925415},
		{"thor12z9pwgw50pepl7mazv4nf2868c6ke882a6aw8x", 808502228779},
		{"thor1mq8mzl9272dzhee3flkndyz2lux9nvzq8zn0f7", 230920778872},
		{"thor1uunxme33cufhtcm00ygkfx6th3vvjmatv8tanl", 117358353467},
		{"thor1eyvhjlezafz36cz3vev60u6p2zn24dhu9gv0wf", 69322921547},
		{"thor1tp8l7ygmnhmny9dknjjj8d65x49t28nuyc4e32", 65516063210},
		{"thor1s9cv0mcp80lehzyjhjtkcaq2yjc4ez36p8qh33", 50404959208},
		{"thor17kq48hspxnaku6pd4crj9hnywjcpehvxsemfsr", 39003121809},
		{"thor125rz8fe8spjhjsknfmejddtxfh44v9jn0klhcx", 23263527044},
		{"thor10rthwfd27zhs3lrk2ck8gv2yx3zgz3wtuvtle8", 14739474681},
		{"thor10j3d4dgq0sekw3vywtt8kgeeympujsmcnmz0ha", 12822773036},
		{"thor1kv4jdrnekdfqcajez78d2drvhp3ep6akx6865j", 15960846102},
		{"thor1l7p2rlckkclsgauall0v4hfy94rdpk2j8dn9qn", 3295477653},
		{"thor1mge35algk7x6r4gygauvxcwn5nhqcchmaayuyr", 2142333731},
		{"thor1nc8kha8hn74h0w7zdyclncq5h039wecgeftnyn", 1136748510},
		{"thor1x4d5g75v67affmr9qjuu75swjsgme4jscxj7pl", 117441744148},
		{"thor1dmgdpkqngg3amua5hffjcharngzdp4wv39azjs", 44782043380},
		{"thor1atev7k3xzsqsenrwjht3k3r70t9mtz0p58qn6d", 35558585112},
		{"thor1p8as4v08l6t04tahqgajgw5ycj2a5k3ygthzql", 28083870141},
		{"thor1fptyt9rkukvn80sda7q8e446ft2lp6jkngh8pm", 23353164702},
		{"thor1vqd6djdqn4hlrquxuwz2na3mac8qm8qgwfu7ps", 17746077327},
		{"thor155cljp4fcarppqupzh9s3uvu6525hd5gtktw03", 4493133386},
		{"thor1wudr3yyc0d436k8cml7c8dvtqxwsvwlztvgq8s", 1329048855},
		{"thor1j47es49m8llprlpcxrt3a2hqyhperwc7ta6ksm", 46008641784},
		{"thor1rpu6ndvg0r25y6xqdl2svqlwglc3yhh3wv7j9v", 41040827546},
		{"thor140dy78lz5vv84gknn2wdwv7ed4nhs2kauwj9w2", 3634392367},
		{"thor1tu9xulcjw76mp7ky7v9l2hrwk2chvglapyycra", 1288358184},
		{"thor196svmy67vm4ya3rnlfnjq4sc9u5c5skurcanf9", 1275639579},
		{"thor1dsk8smfqt6xxjs8lzuy4hpxrh0wfklf5twjhue", 1116636690},
		{"thor19pkfd9ygch6dfa067ddn0fwul8g3x0sy8nxnvr", 1106152983},
		{"thor1a8m2shzvyya0ckvq76fxsnd5800hm0uxxjuy3g", 1046501504},
		{"thor1rzxvqhepnqqcn7973jp4y0ygasr709gpj673lw", 312191389293},
		{"thor1sth9gz5asawsvfrq08ag7wzwqqlrjxl0egxuym", 1361005139},
	}

	// Sanity checks.
	var sum uint64
	ok := true
	for i := range refundDetails {
		sum += refundDetails[i].AmountUint64
	}
	if len(refundDetails) != 43 {
		ctx.Logger().Error("store migration recipient number was not 46", "number", len(refundDetails))
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

	restoreTotalCollateral(ctx, mgr)

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
