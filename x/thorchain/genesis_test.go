package thorchain

import (
	"os"
	"regexp"
	"strings"

	"github.com/btcsuite/btcutil/bech32"
	"gitlab.com/thorchain/thornode/v3/cmd"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type GenesisTestSuite struct{}

var _ = Suite(&GenesisTestSuite{})

var genesisFixtureBech32 = regexp.MustCompile(`thor[a-z]*1[0-9a-z]+`)

func normalizeGenesisFixtureBech32(input string) string {
	return genesisFixtureBech32.ReplaceAllStringFunc(input, func(raw string) string {
		_, data, err := bech32.DecodeNoLimit(raw)
		if err != nil {
			return raw
		}

		targetPrefix := cmd.Bech32PrefixAccAddr
		switch {
		case strings.HasPrefix(raw, "thorcpub1"):
			targetPrefix = cmd.Bech32PrefixConsPub
		case strings.HasPrefix(raw, "thorpub1"):
			targetPrefix = cmd.Bech32PrefixAccPub
		case strings.HasPrefix(raw, "thorc1"):
			targetPrefix = cmd.Bech32PrefixConsAddr
		}

		normalized, err := bech32.Encode(targetPrefix, data)
		if err != nil {
			return raw
		}
		return normalized
	})
}

func (GenesisTestSuite) TestGenesis(c *C) {
	SetupConfigForTest()
	genesisState := DefaultGenesisState()
	c.Assert(ValidateGenesis(genesisState), IsNil)
	ctx, mgr := setupManagerForTest(c)
	gs := ExportGenesis(ctx, mgr.Keeper())
	c.Assert(ValidateGenesis(gs), IsNil)
	content, err := os.ReadFile("../../test/fixtures/genesis/genesis.json")
	c.Assert(err, IsNil)
	c.Assert(content, NotNil)
	normalizedContent := normalizeGenesisFixtureBech32(string(content))
	ctx, mgr = setupManagerForTest(c)
	var state GenesisState
	c.Assert(ModuleCdc.UnmarshalJSON([]byte(normalizedContent), &state), IsNil)
	result := InitGenesis(ctx, mgr.Keeper(), state)
	c.Assert(result, NotNil)
	gs1 := ExportGenesis(ctx, mgr.Keeper())
	c.Assert(len(gs1.Pools) > 0, Equals, true)
}

func (GenesisTestSuite) TestGenesisPOLReserveDepositRoundTrip(c *C) {
	SetupConfigForTest()

	srcCtx, srcMgr := setupManagerForTest(c)
	usdc, err := common.NewAsset("ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48")
	c.Assert(err, IsNil)

	btcDeposit := NewPOLReserveDeposit(common.BTCAsset)
	btcDeposit.RuneDeposited = cosmos.NewUint(12345 * common.One)
	c.Assert(srcMgr.Keeper().SetPOLReserveDeposit(srcCtx, btcDeposit), IsNil)

	usdcDeposit := NewPOLReserveDeposit(usdc)
	usdcDeposit.RuneDeposited = cosmos.NewUint(678 * common.One)
	c.Assert(srcMgr.Keeper().SetPOLReserveDeposit(srcCtx, usdcDeposit), IsNil)

	exported := ExportGenesis(srcCtx, srcMgr.Keeper())
	found := map[string]cosmos.Uint{}
	for _, prd := range exported.PolReserveDeposits {
		found[prd.Asset.String()] = prd.RuneDeposited
	}
	c.Check(found[common.BTCAsset.String()].String(), Equals, btcDeposit.RuneDeposited.String())
	c.Check(found[usdc.String()].String(), Equals, usdcDeposit.RuneDeposited.String())

	dstCtx, dstMgr := setupManagerForTest(c)
	InitGenesis(dstCtx, dstMgr.Keeper(), exported)

	gotBTC, err := dstMgr.Keeper().GetPOLReserveDeposit(dstCtx, common.BTCAsset)
	c.Assert(err, IsNil)
	c.Check(gotBTC.Asset.Equals(common.BTCAsset), Equals, true)
	c.Check(gotBTC.RuneDeposited.String(), Equals, btcDeposit.RuneDeposited.String())

	gotUSDC, err := dstMgr.Keeper().GetPOLReserveDeposit(dstCtx, usdc)
	c.Assert(err, IsNil)
	c.Check(gotUSDC.Asset.Equals(usdc), Equals, true)
	c.Check(gotUSDC.RuneDeposited.String(), Equals, usdcDeposit.RuneDeposited.String())
}

// TestGenesisDynamicFeeRoundtrip seeds a DynamicFeeRecord, accumulator, and
// last-observed-epoch-blocks value into one keeper, exports the state,
// imports into a fresh keeper, and re-exports. It then asserts the two
// exports are equivalent. Without wiring in ExportGenesis / initGenesis
// the re-export would silently drop the dynamic-fee state — leaving the
// controller blind to its prior direction across chain restarts and
// losing in-flight pre-seal volume.
func (GenesisTestSuite) TestGenesisDynamicFeeRoundtrip(c *C) {
	SetupConfigForTest()
	ctx, mgr := setupManagerForTest(c)
	k := mgr.Keeper()

	rec := types.DynamicFeeRecord{
		Thorname:        "bentley",
		PairAssetA:      "BTC.BTC",
		PairAssetB:      "THOR.RUNE",
		DynamicBps:      7,
		LastActiveEpoch: 42,
		History: []types.DynamicFeeEpochRecord{
			{Epoch: 41, VolumeTor: cosmos.NewUint(100), FeesTor: cosmos.NewUint(10), BpsAtClose: 6},
			{Epoch: 42, VolumeTor: cosmos.NewUint(200), FeesTor: cosmos.NewUint(20), BpsAtClose: 7},
		},
	}
	c.Assert(k.SetDynamicFeeRecord(ctx, rec), IsNil)

	acc := types.DynamicFeeAccumulator{
		Thorname:   "bentley",
		PairAssetA: "BTC.BTC",
		PairAssetB: "THOR.RUNE",
		VolumeTor:  cosmos.NewUint(333),
		FeesTor:    cosmos.NewUint(33),
	}
	c.Assert(k.SetDynamicFeeAccumulator(ctx, acc), IsNil)
	k.SetLastObservedDynamicFeeEpochBlocks(ctx, 1337)

	exported := ExportGenesis(ctx, k)
	c.Check(len(exported.DynamicFeeRecords), Equals, 1)
	c.Check(len(exported.DynamicFeeAccumulators), Equals, 1)
	c.Check(exported.LastObservedDynamicFeeEpochBlocks, Equals, int64(1337))
	c.Check(exported.DynamicFeeRecords[0].DynamicBps, Equals, uint64(7))
	c.Check(len(exported.DynamicFeeRecords[0].History), Equals, 2)

	// Fresh keeper → initGenesis from exported → re-export → deep compare.
	ctx2, mgr2 := setupManagerForTest(c)
	k2 := mgr2.Keeper()
	InitGenesis(ctx2, k2, exported)

	reimported, err := k2.GetDynamicFeeRecord(ctx2, "bentley", "BTC.BTC", "THOR.RUNE")
	c.Assert(err, IsNil)
	c.Check(reimported.DynamicBps, Equals, uint64(7))
	c.Check(reimported.LastActiveEpoch, Equals, uint64(42))
	c.Check(len(reimported.History), Equals, 2)
	c.Check(reimported.History[1].BpsAtClose, Equals, uint64(7))

	reimportedAcc, err := k2.GetDynamicFeeAccumulator(ctx2, "bentley", "BTC.BTC", "THOR.RUNE")
	c.Assert(err, IsNil)
	c.Check(reimportedAcc.VolumeTor.Equal(cosmos.NewUint(333)), Equals, true)
	c.Check(reimportedAcc.FeesTor.Equal(cosmos.NewUint(33)), Equals, true)

	observed, err := k2.GetLastObservedDynamicFeeEpochBlocks(ctx2)
	c.Assert(err, IsNil)
	c.Check(observed, Equals, int64(1337))

	reExported := ExportGenesis(ctx2, k2)
	c.Check(len(reExported.DynamicFeeRecords), Equals, 1)
	c.Check(len(reExported.DynamicFeeAccumulators), Equals, 1)
	c.Check(reExported.LastObservedDynamicFeeEpochBlocks, Equals, int64(1337))
}
