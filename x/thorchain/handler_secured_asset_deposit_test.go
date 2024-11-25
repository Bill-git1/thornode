package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	. "gopkg.in/check.v1"
)

type HandlerSecuredAssetDeposit struct{}

var _ = Suite(&HandlerSecuredAssetDeposit{})

func (HandlerSecuredAssetDeposit) TestSecuredAssetDeposit(c *C) {
	ctx, mgr := setupManagerForTest(c)
	h := NewSecuredAssetDepositHandler(mgr)
	asset := common.BTCAsset
	addr := GetRandomBech32Addr()
	dummyTx := common.Tx{ID: "test"}

	msg := NewMsgSecuredAssetDeposit(asset, cosmos.NewUint(350), addr, addr, dummyTx)

	_, err := h.Run(ctx, msg)
	c.Assert(err, NotNil)

	pool, err := mgr.K.GetPool(ctx, asset)
	c.Assert(err, IsNil)
	pool.Asset = asset
	err = mgr.K.SetPool(ctx, pool)
	c.Assert(err, IsNil)

	_, err = h.Run(ctx, msg)
	c.Assert(err, IsNil)

	bal := mgr.SecuredAssetManager().BalanceOf(ctx, asset, addr)
	c.Check(bal.String(), Equals, "350")

	bankBals := mgr.coinKeeper.GetAllBalances(ctx, addr)
	expected := cosmos.NewCoins(cosmos.NewCoin(asset.GetSecuredAsset().Native(), cosmos.NewInt(350)))
	c.Check(bankBals.String(), Equals, expected.String())
}
