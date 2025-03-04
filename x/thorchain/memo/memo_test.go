package thorchain

import (
	"fmt"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	storemetrics "cosmossdk.io/store/metrics"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
	kv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type MemoSuite struct {
	ctx sdk.Context
	k   keeper.Keeper
}

func TestPackage(t *testing.T) { TestingT(t) }

var _ = Suite(&MemoSuite{})

func (s *MemoSuite) SetUpSuite(c *C) {
	types.SetupConfigForTest()
	keyAcc := cosmos.NewKVStoreKey(authtypes.StoreKey)
	keyBank := cosmos.NewKVStoreKey(banktypes.StoreKey)
	keyThorchain := cosmos.NewKVStoreKey(types.StoreKey)
	keyUpgrade := cosmos.NewKVStoreKey(upgradetypes.StoreKey)

	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	ms.MountStoreWithDB(keyAcc, cosmos.StoreTypeIAVL, db)
	ms.MountStoreWithDB(keyThorchain, cosmos.StoreTypeIAVL, db)
	ms.MountStoreWithDB(keyBank, cosmos.StoreTypeIAVL, db)
	err := ms.LoadLatestVersion()
	c.Assert(err, IsNil)

	ctx := cosmos.NewContext(ms, tmproto.Header{ChainID: "thorchain"}, false, log.NewNopLogger())
	s.ctx = ctx.WithBlockHeight(18)

	encodingConfig := testutil.MakeTestEncodingConfig(
		bank.AppModuleBasic{},
		auth.AppModuleBasic{},
	)

	ak := authkeeper.NewAccountKeeper(
		encodingConfig.Codec,
		runtime.NewKVStoreService(keyAcc),
		authtypes.ProtoBaseAccount,
		map[string][]string{
			types.ModuleName:  {authtypes.Minter, authtypes.Burner},
			types.AsgardName:  {},
			types.BondName:    {},
			types.ReserveName: {},
			types.LendingName: {},
		},
		authcodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		sdk.GetConfig().GetBech32AccountAddrPrefix(),
		authtypes.NewModuleAddress(types.ModuleName).String(),
	)

	bk := bankkeeper.NewBaseKeeper(
		encodingConfig.Codec,
		runtime.NewKVStoreService(keyBank),
		ak,
		nil,
		authtypes.NewModuleAddress(types.ModuleName).String(),
		log.NewNopLogger(),
	)
	c.Assert(bk.MintCoins(ctx, types.ModuleName, cosmos.Coins{
		cosmos.NewCoin(common.RuneAsset().Native(), cosmos.NewInt(200_000_000_00000000)),
	}), IsNil)
	uk := upgradekeeper.NewKeeper(
		nil,
		runtime.NewKVStoreService(keyUpgrade),
		encodingConfig.Codec,
		c.MkDir(),
		nil,
		authtypes.NewModuleAddress(types.ModuleName).String(),
	)
	s.k = kv1.NewKVStore(encodingConfig.Codec, bk, ak, uk, keyThorchain, types.GetCurrentVersion())
}

func (s *MemoSuite) TestTxType(c *C) {
	for _, trans := range []TxType{TxAdd, TxWithdraw, TxSwap, TxOutbound, TxDonate, TxBond, TxUnbond, TxLeave} {
		tx, err := StringToTxType(trans.String())
		c.Assert(err, IsNil)
		c.Check(tx, Equals, trans)
		c.Check(tx.IsEmpty(), Equals, false)
	}
}

func (s *MemoSuite) TestParseWithAbbreviated(c *C) {
	ctx := s.ctx
	k := s.k

	// happy paths
	memo, err := ParseMemoWithTHORNames(ctx, k, "d:"+common.RuneAsset().String())
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxDonate), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "+:"+common.RuneAsset().String())
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxAdd), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	_, err = ParseMemoWithTHORNames(ctx, k, "add:BTC.BTC:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:xxxx")
	c.Assert(err, NotNil)

	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("-:%s:25", common.RuneAsset().String()))
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxWithdraw), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetAmount().Uint64(), Equals, uint64(25), Commentf("%d", memo.GetAmount().Uint64()))
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "=:r:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:87e7")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Equal(cosmos.NewUint(870000000)), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)
	c.Check(memo.GetAsset().String(), Equals, "THOR.RUNE")

	// custom refund address
	refundAddr := types.GetRandomTHORAddress()
	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:b:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a/%s:87e7", refundAddr.String()))
	c.Assert(err, IsNil)
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetRefundAddress().String(), Equals, refundAddr.String())

	// if refund address is present, but destination is not, should return an err
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:b:/%s:87e7", refundAddr.String()))
	c.Assert(err, NotNil)

	// test multiple affiliates
	ms := "=:e:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::t/t1/t2:10/20/30"
	memo, err = ParseMemoWithTHORNames(ctx, k, ms)
	c.Assert(err, IsNil)
	c.Check(len(memo.GetAffiliates()), Equals, 3)
	c.Check(len(memo.GetAffiliatesBasisPoints()), Equals, 3)
	c.Check(memo.GetAffiliates()[0], Equals, "t")
	c.Check(memo.GetAffiliatesBasisPoints()[0].Uint64(), Equals, uint64(10))
	c.Check(memo.GetAffiliates()[1], Equals, "t1")
	c.Check(memo.GetAffiliatesBasisPoints()[1].Uint64(), Equals, uint64(20))
	c.Check(memo.GetAffiliates()[2], Equals, "t2")
	c.Check(memo.GetAffiliatesBasisPoints()[2].Uint64(), Equals, uint64(30))

	// thornames + rune addrs
	affRune := types.GetRandomTHORAddress()
	ms = fmt.Sprintf("=:e:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::t/%s/t2:10/20/30", affRune.String())
	memo, err = ParseMemoWithTHORNames(ctx, k, ms)
	c.Assert(err, IsNil)
	c.Check(memo.GetAffiliatesBasisPoints()[0].Uint64(), Equals, uint64(10))
	c.Check(memo.GetAffiliates()[1], Equals, affRune.String())
	c.Check(memo.GetAffiliatesBasisPoints()[1].Uint64(), Equals, uint64(20))
	c.Check(memo.GetAffiliates()[2], Equals, "t2")
	c.Check(memo.GetAffiliatesBasisPoints()[2].Uint64(), Equals, uint64(30))

	// one affiliate bps defined, should apply to all affiliates
	ms = "=:e:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::t/t1/t2:10"
	memo, err = ParseMemoWithTHORNames(ctx, k, ms)
	c.Assert(err, IsNil)
	c.Check(memo.GetAffiliatesBasisPoints()[0].Uint64(), Equals, uint64(10))
	c.Check(memo.GetAffiliatesBasisPoints()[1].Uint64(), Equals, uint64(10))
	c.Check(memo.GetAffiliatesBasisPoints()[2].Uint64(), Equals, uint64(10))

	// affiliates + bps mismatch
	ms = "=:e:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::t/t1/t2:10/20"
	_, err = ParseMemoWithTHORNames(ctx, k, ms)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "affiliate thornames and affiliate fee bps count mismatch")

	// total affiliate fee too high
	ms = "=:e:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::t/t1/t2:10000/10000/10000"
	_, err = ParseMemoWithTHORNames(ctx, k, ms)
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "total affiliate fee basis points can't be more than 10000")

	// test streaming swap
	memo, err = ParseMemoWithTHORNames(ctx, k, "=:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/10/20")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Equal(cosmos.NewUint(1200)), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)
	swapMemo, ok := memo.(SwapMemo)
	c.Assert(ok, Equals, true)
	c.Check(swapMemo.GetStreamQuantity(), Equals, uint64(20), Commentf("%d", swapMemo.GetStreamQuantity()))
	c.Check(swapMemo.GetStreamInterval(), Equals, uint64(10))
	c.Check(swapMemo.String(), Equals, "=:THOR.RUNE:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/10/20")

	memo, err = ParseMemoWithTHORNames(ctx, k, "=:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a://")
	c.Assert(err, IsNil)
	c.Check(memo.GetSlipLimit().String(), Equals, "0")
	swapMemo, ok = memo.(SwapMemo)
	c.Assert(ok, Equals, true)
	c.Check(swapMemo.GetStreamQuantity(), Equals, uint64(0))
	c.Check(swapMemo.GetStreamInterval(), Equals, uint64(0))

	// wacky lending tests
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:%s:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/10/20abc", common.RuneAsset()))
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:%s:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/10/////", common.RuneAsset()))
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:%s:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/10/-20", common.RuneAsset()))
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:%s:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/-10/20", common.RuneAsset()))
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("=:%s:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1200/102103980982304982058230492830429384080/20", common.RuneAsset()))
	c.Assert(err, NotNil)

	memo, err = ParseMemoWithTHORNames(ctx, k, "=:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Uint64(), Equals, uint64(0))
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.GetDexAggregator(), Equals, "")

	memo, err = ParseMemoWithTHORNames(ctx, k, "=:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::::123:0x2354234523452345:1234444")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Equal(cosmos.ZeroUint()), Equals, true)
	c.Check(memo.GetDexAggregator(), Equals, "123")
	c.Check(memo.GetDexTargetAddress(), Equals, "0x2354234523452345")
	c.Check(memo.GetDexTargetLimit().Equal(cosmos.NewUint(1234444)), Equals, true)

	// test dex agg limit with scientific notation - long number
	memo, err = ParseMemoWithTHORNames(ctx, k, "=:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a::::123:0x2354234523452345:1425e18")
	c.Assert(err, IsNil)
	c.Check(memo.GetDexTargetLimit().Equal(cosmos.NewUintFromString("1425000000000000000000")), Equals, true) // noting the large number overflows `cosmos.NewUint`

	memo, err = ParseMemoWithTHORNames(ctx, k, "OUT:MUKVQILIHIAUSEOVAXBFEZAJKYHFJYHRUUYGQJZGFYBYVXCXYNEMUOAIQKFQLLCX")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxOutbound), Equals, true, Commentf("%s", memo.GetType()))
	c.Check(memo.IsOutbound(), Equals, true)
	c.Check(memo.IsInbound(), Equals, false)
	c.Check(memo.IsInternal(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "REFUND:MUKVQILIHIAUSEOVAXBFEZAJKYHFJYHRUUYGQJZGFYBYVXCXYNEMUOAIQKFQLLCX")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxRefund), Equals, true)
	c.Check(memo.IsOutbound(), Equals, true)

	memo, err = ParseMemoWithTHORNames(ctx, k, "leave:whatever")
	c.Assert(err, NotNil)
	c.Check(memo.IsType(TxLeave), Equals, true)

	addr := types.GetRandomBech32Addr()
	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("leave:%s", addr.String()))
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxLeave), Equals, true)
	c.Check(memo.GetAccAddress().String(), Equals, addr.String())

	memo, err = ParseMemoWithTHORNames(ctx, k, "migrate:100")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxMigrate), Equals, true)
	c.Check(memo.IsInternal(), Equals, true)

	memo, err = ParseMemoWithTHORNames(ctx, k, "ragnarok:100")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxRagnarok), Equals, true)
	c.Check(memo.IsOutbound(), Equals, true)

	memo, err = ParseMemoWithTHORNames(ctx, k, "reserve")
	c.Check(err, IsNil)
	c.Check(memo.IsType(TxReserve), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "noop")
	c.Check(err, IsNil)
	c.Check(memo.IsType(TxNoOp), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "noop:novault")
	c.Check(err, IsNil)
	c.Check(memo.IsType(TxNoOp), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "$+:BTC.BTC:bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxLoanOpen), Equals, true)
	c.Check(memo.IsType(TxLoanOpen), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)

	memo, err = ParseMemoWithTHORNames(ctx, k, "$+:BTC.BTC:bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej:45e3:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:1000:aggie:aggtar:55")
	c.Assert(err, IsNil)
	m, ok := memo.(LoanOpenMemo)
	c.Assert(ok, Equals, true)
	c.Check(m.MinOut.Uint64(), Equals, uint64(45000))
	c.Check(m.TargetAddress.String(), Equals, "bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej")
	c.Check(m.TargetAsset.Equals(common.BTCAsset), Equals, true)
	c.Check(m.AffiliateAddress.String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(m.AffiliateBasisPoints.Uint64(), Equals, uint64(1000))
	c.Check(m.DexAggregator, Equals, "aggie")
	c.Check(m.DexTargetAddress, Equals, "aggtar")
	c.Check(m.DexTargetLimit.Uint64(), Equals, uint64(55))

	memo, err = ParseMemoWithTHORNames(ctx, k, "$-:BTC.BTC:bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej:78e4")
	c.Check(err, IsNil)
	c.Check(memo.IsType(TxLoanRepayment), Equals, true)
	c.Check(memo.IsInbound(), Equals, true)
	c.Check(memo.IsInternal(), Equals, false)
	c.Check(memo.IsOutbound(), Equals, false)
	mLoanRepayment, ok := memo.(LoanRepaymentMemo)
	c.Assert(ok, Equals, true)
	c.Check(mLoanRepayment.Asset.Equals(common.BTCAsset), Equals, true)
	c.Check(mLoanRepayment.Owner.String(), Equals, "bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej")
	c.Check(mLoanRepayment.MinOut.Uint64(), Equals, uint64(780000))

	// unhappy paths
	_, err = ParseMemoWithTHORNames(ctx, k, "")
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "bogus")
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "CREATE") // missing symbol
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "c:") // bad symbol
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "-:eth") // withdraw basis points is optional
	c.Assert(err, IsNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "-:eth:twenty-two") // bad amount
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "=:eth:bad_DES:5.6") // bad destination
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, ">:eth:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:five") // bad slip limit
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "!:key:val") // not enough arguments
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "!:bogus:key:value") // bogus admin command type
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "nextpool:whatever")
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "migrate")
	c.Assert(err, NotNil)
}

func (s *MemoSuite) TestParse(c *C) {
	ctx := s.ctx
	k := s.k

	thorAddr := types.GetRandomTHORAddress()
	thorAccAddr, _ := thorAddr.AccAddress()
	name := types.NewTHORName("hello", 50, []types.THORNameAlias{{Chain: common.THORChain, Address: thorAddr}})
	name.Owner = thorAccAddr
	k.SetTHORName(ctx, name)

	// happy paths
	memo, err := ParseMemoWithTHORNames(ctx, k, "d:"+common.RuneAsset().String())
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxDonate), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.String(), Equals, "DONATE:"+common.RuneAsset().String())

	memo, err = ParseMemoWithTHORNames(ctx, k, "ADD:"+common.RuneAsset().String())
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxAdd), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.String(), Equals, "+:THOR.RUNE")

	_, err = ParseMemoWithTHORNames(ctx, k, "ADD:BTC.BTC")
	c.Assert(err, IsNil)
	memo, err = ParseMemoWithTHORNames(ctx, k, "ADD:BTC.BTC:bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej")
	c.Assert(err, IsNil)
	c.Check(memo.GetDestination().String(), Equals, "bc1qwqdg6squsna38e46795at95yu9atm8azzmyvckulcc7kytlcckxswvvzej")
	c.Check(memo.IsType(TxAdd), Equals, true, Commentf("MEMO: %+v", memo))

	_, err = ParseMemoWithTHORNames(ctx, k, "ADD:ETH.ETH:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:tthor176xrckly4p7efq7fshhcuc2kax3dyxu9hguzl7:1000")
	c.Assert(err, IsNil)

	// trade account unit tests
	trAccAddr := types.GetRandomBech32Addr()
	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("trade+:%s", trAccAddr))
	c.Assert(err, IsNil)
	tr1, ok := memo.(TradeAccountDepositMemo)
	c.Assert(ok, Equals, true)
	c.Check(tr1.GetAccAddress().Equals(trAccAddr), Equals, true)

	ethAddr := types.GetRandomETHAddress()
	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("trade-:%s", ethAddr))
	c.Assert(err, IsNil)
	tr2, ok := memo.(TradeAccountWithdrawalMemo)
	c.Assert(ok, Equals, true)
	fmt.Println(tr2)
	c.Check(tr2.GetAddress().Equals(ethAddr), Equals, true)

	memo, err = ParseMemoWithTHORNames(ctx, k, "WITHDRAW:"+common.RuneAsset().String()+":25")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxWithdraw), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetAmount().Equal(cosmos.NewUint(25)), Equals, true, Commentf("%d", memo.GetAmount().Uint64()))

	memo, err = ParseMemoWithTHORNames(ctx, k, "SWAP:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:870000000:hello:100")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Equal(cosmos.NewUint(870000000)), Equals, true)
	c.Check(len(memo.GetAffiliates()), Equals, 1)
	c.Check(len(memo.GetAffiliatesBasisPoints()), Equals, 1)
	c.Check(memo.GetAffiliates()[0], Equals, "hello")
	c.Check(memo.GetAffiliatesBasisPoints()[0].Uint64(), Equals, uint64(100))

	memo, err = ParseMemoWithTHORNames(ctx, k, "SWAP:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Uint64(), Equals, uint64(0))

	memo, err = ParseMemoWithTHORNames(ctx, k, "SWAP:"+common.RuneAsset().String()+":0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:")
	c.Assert(err, IsNil)
	c.Check(memo.GetAsset().String(), Equals, common.RuneAsset().String())
	c.Check(memo.IsType(TxSwap), Equals, true, Commentf("MEMO: %+v", memo))
	c.Check(memo.GetDestination().String(), Equals, "0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(memo.GetSlipLimit().Uint64(), Equals, uint64(0))

	whiteListAddr := types.GetRandomBech32Addr()
	bondProvider := types.GetRandomBech32Addr()
	memo, err = ParseMemoWithTHORNames(ctx, k, fmt.Sprintf("BOND:%s:%s", whiteListAddr, bondProvider))
	c.Assert(err, IsNil)
	c.Assert(memo.IsType(TxBond), Equals, true)
	c.Assert(memo.GetAccAddress().String(), Equals, whiteListAddr.String())
	// trunk-ignore(golangci-lint/govet): shadow false positive
	parser, _ := newParser(ctx, k, k.GetVersion(), fmt.Sprintf("BOND:%s:%s", whiteListAddr.String(), bondProvider.String()))
	mem, err := parser.ParseBondMemo()
	c.Assert(err, IsNil)
	c.Assert(mem.BondProviderAddress.String(), Equals, bondProvider.String())
	c.Assert(mem.NodeOperatorFee, Equals, int64(-1))
	parser, _ = newParser(ctx, k, k.GetVersion(), fmt.Sprintf("BOND:%s:%s:0", whiteListAddr.String(), bondProvider.String()))
	mem, err = parser.ParseBondMemo()
	c.Assert(err, IsNil)
	c.Assert(mem.BondProviderAddress.String(), Equals, bondProvider.String())
	c.Assert(mem.NodeOperatorFee, Equals, int64(0))
	parser, _ = newParser(ctx, k, k.GetVersion(), fmt.Sprintf("BOND:%s:%s:1000", whiteListAddr.String(), bondProvider.String()))
	mem, err = parser.ParseBondMemo()
	c.Assert(err, IsNil)
	c.Assert(mem.BondProviderAddress.String(), Equals, bondProvider.String())
	c.Assert(mem.NodeOperatorFee, Equals, int64(1000))

	memo, err = ParseMemoWithTHORNames(ctx, k, "leave:"+types.GetRandomBech32Addr().String())
	c.Assert(err, IsNil)
	c.Assert(memo.IsType(TxLeave), Equals, true)

	memo, err = ParseMemoWithTHORNames(ctx, k, "unbond:"+whiteListAddr.String()+":300")
	c.Assert(err, IsNil)
	c.Assert(memo.IsType(TxUnbond), Equals, true)
	c.Assert(memo.GetAccAddress().String(), Equals, whiteListAddr.String())
	c.Assert(memo.GetAmount().Equal(cosmos.NewUint(300)), Equals, true)
	parser, _ = newParser(ctx, k, k.GetVersion(), fmt.Sprintf("UNBOND:%s:400:%s", whiteListAddr.String(), bondProvider.String()))
	unbondMemo, err := parser.ParseUnbondMemo()
	c.Assert(err, IsNil)
	c.Assert(unbondMemo.BondProviderAddress.String(), Equals, bondProvider.String())

	memo, err = ParseMemoWithTHORNames(ctx, k, "migrate:100")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxMigrate), Equals, true)
	c.Check(memo.GetBlockHeight(), Equals, int64(100))
	c.Check(memo.String(), Equals, "MIGRATE:100")

	txID := types.GetRandomTxHash()
	memo, err = ParseMemoWithTHORNames(ctx, k, "OUT:"+txID.String())
	c.Check(err, IsNil)
	c.Check(memo.IsOutbound(), Equals, true)
	c.Check(memo.GetTxID(), Equals, txID)
	c.Check(memo.String(), Equals, "OUT:"+txID.String())

	refundMemo := "REFUND:" + txID.String()
	memo, err = ParseMemoWithTHORNames(ctx, k, refundMemo)
	c.Check(err, IsNil)
	c.Check(memo.GetTxID(), Equals, txID)
	c.Check(memo.String(), Equals, refundMemo)

	ragnarokMemo := "RAGNAROK:1024"
	memo, err = ParseMemoWithTHORNames(ctx, k, ragnarokMemo)
	c.Check(err, IsNil)
	c.Check(memo.IsType(TxRagnarok), Equals, true)
	c.Check(memo.GetBlockHeight(), Equals, int64(1024))
	c.Check(memo.String(), Equals, ragnarokMemo)

	baseMemo := MemoBase{}
	c.Check(baseMemo.String(), Equals, "")
	c.Check(baseMemo.GetAmount().Uint64(), Equals, cosmos.ZeroUint().Uint64())
	c.Check(baseMemo.GetDestination(), Equals, common.NoAddress)
	c.Check(baseMemo.GetSlipLimit().Uint64(), Equals, cosmos.ZeroUint().Uint64())
	c.Check(baseMemo.GetTxID(), Equals, common.TxID(""))
	c.Check(baseMemo.GetAccAddress().Empty(), Equals, true)
	c.Check(baseMemo.IsEmpty(), Equals, true)
	c.Check(baseMemo.GetBlockHeight(), Equals, int64(0))

	// swap memo parsing

	// aff fee too high, should be reset to 10_000
	_, err = ParseMemoWithTHORNames(ctx, k, "swap:eth.eth:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:100:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:20000")
	c.Assert(err, NotNil)

	// aff fee valid, don't change
	memo, err = ParseMemoWithTHORNames(ctx, k, "swap:eth.eth:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:100:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:5000")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxSwap), Equals, true)
	c.Check(memo.String(), Equals, "=:ETH.ETH:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:100:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:5000")

	// add memo parsing

	_, err = ParseMemoWithTHORNames(ctx, k, "add:eth.eth:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:20000")
	c.Assert(err, NotNil)

	// aff fee valid, don't change
	memo, err = ParseMemoWithTHORNames(ctx, k, "add:btc.btc:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:5000")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxAdd), Equals, true)
	c.Check(memo.String(), Equals, "+:BTC.BTC:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj:5000")

	// aff fee savers memo
	_, err = ParseMemoWithTHORNames(ctx, k, "+:BSC/BNB::t:0")
	// should fail, thorname not registered
	c.Assert(err.Error(), Equals, "MEMO: +:BSC/BNB::t:0\nPARSE FAILURE(S): cannot parse 't' as an Address: t is not recognizable")
	// register thorname
	thorname := types.NewTHORName("t", 50, []types.THORNameAlias{{Chain: common.THORChain, Address: thorAddr}})
	k.SetTHORName(ctx, thorname)
	_, err = ParseMemoWithTHORNames(ctx, k, "+:BSC/BNB::t:15")
	c.Assert(err, IsNil)

	// no address or aff fee
	memo, err = ParseMemoWithTHORNames(ctx, k, "add:btc.btc")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxAdd), Equals, true)
	c.Check(memo.String(), Equals, "+:BTC.BTC")

	// no aff fee
	memo, err = ParseMemoWithTHORNames(ctx, k, "add:btc.btc:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxAdd), Equals, true)
	c.Check(memo.String(), Equals, "+:BTC.BTC:thor1z83z5t9vqxys8nhpkxk5zp6zym0lalcp8ywhvj")

	// unhappy paths
	memo, err = ParseMemoWithTHORNames(ctx, k, "")
	c.Assert(err, NotNil)
	c.Assert(memo.IsEmpty(), Equals, true)
	_, err = ParseMemoWithTHORNames(ctx, k, "bogus")
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "CREATE") // missing symbol
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "CREATE:") // bad symbol
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "withdraw") // not enough parameters
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "withdraw:eth") // withdraw basis points is optional
	c.Assert(err, IsNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "withdraw:eth:twenty-two") // bad amount
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "swap") // not enough parameters
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "swap:eth:PROVIDER-1:5.6") // bad destination
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "swap:eth:bad_DES:5.6") // bad destination
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "swap:eth:0x90f2b1ae50e6018230e90a33f98c7844a0ab635a:five") // bad slip limit
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "admin:key:val") // not enough arguments
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "admin:bogus:key:value") // bogus admin command type
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "migrate:abc")
	c.Assert(err, NotNil)

	_, err = ParseMemoWithTHORNames(ctx, k, "withdraw:A")
	c.Assert(err, IsNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "leave")
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "out") // not enough parameter
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "bond") // not enough parameter
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "refund") // not enough parameter
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "ragnarok") // not enough parameter
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "ragnarok:what") // not enough parameter
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "bond:what") // invalid address
	c.Assert(err, NotNil)
	_, err = ParseMemoWithTHORNames(ctx, k, "whatever") // not support
	c.Assert(err, NotNil)

	memo, err = ParseMemoWithTHORNames(ctx, k, "x:tthor14hj2tavq8fpesdwxxcu44rty3hh90vhujrvcmstl4zr3txmfvw9sw58u9f:AA==")
	c.Assert(err, IsNil)
	c.Check(memo.IsType(TxExec), Equals, true)
	c.Check(memo.String(), Equals, "x:tthor14hj2tavq8fpesdwxxcu44rty3hh90vhujrvcmstl4zr3txmfvw9sw58u9f:AA==")
}
