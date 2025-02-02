package thorchain

import (
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type MigrationHelpersTestSuite struct{}

var _ = Suite(&MigrationHelpersTestSuite{})

func (MigrationHelpersTestSuite) TestUnsafeAddRefundOutbound(c *C) {
	w := getHandlerTestWrapper(c, 1, true, true)

	// add a vault
	vault := GetRandomVault()
	vault.Coins = common.Coins{
		// common.NewCoin(common.RuneAsset(), cosmos.NewUint(10000*common.One)),
		common.NewCoin(common.ETHAsset, cosmos.NewUint(10000*common.One)),
	}
	c.Assert(w.keeper.SetVault(w.ctx, vault), IsNil)

	// add node
	acc1 := GetRandomValidatorNode(NodeActive)
	c.Assert(w.keeper.SetNodeAccount(w.ctx, acc1), IsNil)

	// Create inbound
	inTxID := GetRandomTxHash()
	ethAddr := GetRandomETHAddress()
	vaultAddr, err := vault.PubKey.GetAddress(common.ETHChain)
	c.Assert(err, IsNil)
	coin := common.NewCoin(common.ETHAsset, cosmos.NewUint(100*common.One))
	height := w.ctx.BlockHeight()

	tx := common.Tx{
		ID:          inTxID,
		Chain:       common.ETHChain,
		FromAddress: ethAddr,
		ToAddress:   vaultAddr,
		Coins:       common.Coins{coin},
		Gas:         common.Gas{common.NewCoin(common.ETHAsset, cosmos.NewUint(1))},
		Memo:        "bad memo",
	}

	voter := NewObservedTxVoter(inTxID, ObservedTxs{
		ObservedTx{
			Tx:             tx,
			Status:         types.Status_incomplete,
			BlockHeight:    1,
			Signers:        []string{w.activeNodeAccount.NodeAddress.String(), acc1.NodeAddress.String()},
			KeysignMs:      0,
			FinaliseHeight: 1,
		},
	})
	w.keeper.SetObservedTxInVoter(w.ctx, voter)

	mgr, ok := w.mgr.(*Mgrs)
	c.Assert(ok, Equals, true)

	// add outbound using migration helper
	err = unsafeAddRefundOutbound(w.ctx, mgr, inTxID.String(), ethAddr.String(), coin, height)
	c.Assert(err, IsNil)

	items, err := w.mgr.TxOutStore().GetOutboundItems(w.ctx)
	c.Assert(err, IsNil)
	c.Assert(items, HasLen, 1)
	c.Assert(items[0].Chain, Equals, common.ETHChain)
	c.Assert(items[0].InHash.Equals(inTxID), Equals, true)
	c.Assert(items[0].ToAddress.Equals(ethAddr), Equals, true)
	c.Assert(items[0].VaultPubKey.Equals(vault.PubKey), Equals, true)
	c.Assert(items[0].Coin.Equals(coin), Equals, true)
}
