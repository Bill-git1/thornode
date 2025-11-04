package thorchain

import (
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gopkg.in/check.v1"
	. "gopkg.in/check.v1"
)

type HandlerCommonOutboundSuite struct{}

var _ = Suite(&HandlerCommonOutboundSuite{})

func (s *HandlerCommonOutboundSuite) TestIsOutboundFakeGasTX(c *C) {
	coins := common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(1)),
	}
	gas := common.Gas{
		{Asset: common.ETHAsset, Amount: cosmos.NewUint(1)},
	}
	// Fake gas transactions have self-referential OUT:txhash memo (bifrost behavior)
	fakeGasTx := common.ObservedTx{
		Tx: common.NewTx("123", "0xabc", "0x123", coins, gas, "OUT:123"),
	}

	c.Assert(isOutboundFakeGasTx(fakeGasTx), Equals, true)

	coins = common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(100000)),
	}
	theftTx := common.ObservedTx{
		Tx: common.NewTx("123", "0xabc", "0x123", coins, gas, "=:AVAX.AVAX:0x123"),
	}
	c.Assert(isOutboundFakeGasTx(theftTx), Equals, false)

	coins = common.Coins{
		common.NewCoin(common.BTCAsset, cosmos.NewUint(1)),
	}
	theftTx2 := common.ObservedTx{
		Tx: common.NewTx("123", "0xabc", "0x123", coins, gas, "OUT:123"),
	}
	c.Assert(isOutboundFakeGasTx(theftTx2), Equals, false) // Wrong chain (BTC not EVM)

	// Test with wrong memo format (not self-referential)
	coins = common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(1)),
	}
	wrongMemoTx := common.ObservedTx{
		Tx: common.NewTx("123", "0xabc", "0x123", coins, gas, "OUT:ABCD1234567890"),
	}
	c.Assert(isOutboundFakeGasTx(wrongMemoTx), Equals, false) // Wrong memo format (not self-referential)
}

func (s *HandlerCommonOutboundSuite) TestIsCancelTx(c *C) {
	// Create a vault pubkey and get its address
	vaultPubKey := GetRandomPubKey()
	vaultAddr, err := vaultPubKey.GetAddress(common.ETHChain)
	c.Assert(err, IsNil)

	// Test 1: Valid cancel tx (EVM, gas asset, amount=DustThreshold, vault-to-vault)
	// Cancel transactions have amount=0 on chain, but bifrost converts to DustThreshold
	dustThreshold := common.ETHChain.DustThreshold() // 1 for ETH
	cancelTxCoins := common.Coins{
		common.NewCoin(common.ETHAsset, dustThreshold),
	}
	cancelTxGas := common.Gas{
		{Asset: common.ETHAsset, Amount: cosmos.NewUint(21000)},
	}
	cancelTx := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, cancelTxCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(cancelTx), Equals, true)

	// Test 2: Not a cancel tx - different to address (external address)
	externalAddr := GetRandomETHAddress()
	notCancelTx := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, externalAddr, cancelTxCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx), Equals, false)

	// Test 3: Not a cancel tx - amount is 0 (not DustThreshold)
	zeroAmountCoins := common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.ZeroUint()),
	}
	notCancelTx2 := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, zeroAmountCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx2), Equals, false)

	// Test 4: Not a cancel tx - large amount
	largeAmountCoins := common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(100000)),
	}
	notCancelTx3 := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, largeAmountCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx3), Equals, false)

	// Test 5: Not a cancel tx - not EVM chain (BTC)
	btcVaultPubKey := GetRandomPubKey()
	btcVaultAddr, err := btcVaultPubKey.GetAddress(common.BTCChain)
	c.Assert(err, IsNil)
	btcCoins := common.Coins{
		common.NewCoin(common.BTCAsset, cosmos.ZeroUint()),
	}
	btcGas := common.Gas{
		{Asset: common.BTCAsset, Amount: cosmos.NewUint(10000)},
	}
	notCancelTx4 := ObservedTx{
		Tx:             common.NewTx("123", btcVaultAddr, btcVaultAddr, btcCoins, btcGas, ""),
		ObservedPubKey: btcVaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx4), Equals, false)

	// Test 6: Valid cancel tx on different EVM chain (AVAX)
	avaxVaultPubKey := GetRandomPubKey()
	avaxVaultAddr, err := avaxVaultPubKey.GetAddress(common.AVAXChain)
	c.Assert(err, IsNil)
	avaxDustThreshold := common.AVAXChain.DustThreshold() // 1 for AVAX
	avaxCoins := common.Coins{
		common.NewCoin(common.AVAXAsset, avaxDustThreshold),
	}
	avaxGas := common.Gas{
		{Asset: common.AVAXAsset, Amount: cosmos.NewUint(21000)},
	}
	cancelTxAvax := ObservedTx{
		Tx:             common.NewTx("123", avaxVaultAddr, avaxVaultAddr, avaxCoins, avaxGas, ""),
		ObservedPubKey: avaxVaultPubKey,
	}
	c.Assert(isCancelTx(cancelTxAvax), Equals, true)

	// Test 7: Not a cancel tx - not gas asset (ERC20 token)
	tokenAsset, err := common.NewAsset("ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7")
	c.Assert(err, IsNil)
	tokenCoins := common.Coins{
		common.NewCoin(tokenAsset, cosmos.ZeroUint()),
	}
	notCancelTx5 := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, tokenCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx5), Equals, false)

	// Test 8: Not a cancel tx - multiple coins
	multiCoins := common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.ZeroUint()),
		common.NewCoin(common.ETHAsset, cosmos.ZeroUint()),
	}
	notCancelTx6 := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, multiCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx6), Equals, false)

	// Test 9: Not a cancel tx - has a memo
	memoCoins := common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.ZeroUint()),
	}
	notCancelTx7 := ObservedTx{
		Tx:             common.NewTx("123", vaultAddr, vaultAddr, memoCoins, cancelTxGas, "OUT:abc123"),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx7), Equals, false)

	// Test 10: Not a cancel tx - FromAddress is external (not vault-to-vault)
	notCancelTx8 := ObservedTx{
		Tx:             common.NewTx("123", externalAddr, vaultAddr, cancelTxCoins, cancelTxGas, ""),
		ObservedPubKey: vaultPubKey,
	}
	c.Assert(isCancelTx(notCancelTx8), Equals, false)
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutEvenDistribution(c *check.C) {
	clout1 := cosmos.NewUint(50)
	clout2 := cosmos.NewUint(50)
	spent := cosmos.NewUint(60)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.String(), check.Equals, "30")
	c.Assert(split2.String(), check.Equals, "30")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutExcessSpent(c *check.C) {
	clout1 := cosmos.NewUint(50)
	clout2 := cosmos.NewUint(50)
	spent := cosmos.NewUint(120)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.String(), check.Equals, "50")
	c.Assert(split2.String(), check.Equals, "50")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutInsufficientFirstClout(c *check.C) {
	clout1 := cosmos.NewUint(20)
	clout2 := cosmos.NewUint(80)
	spent := cosmos.NewUint(60)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.String(), check.Equals, "20")
	c.Assert(split2.String(), check.Equals, "40")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutInsufficientSecondClout(c *check.C) {
	clout1 := cosmos.NewUint(80)
	clout2 := cosmos.NewUint(20)
	spent := cosmos.NewUint(60)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.String(), check.Equals, "40")
	c.Assert(split2.String(), check.Equals, "20")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutSpentIsZero(c *check.C) {
	clout1 := cosmos.NewUint(50)
	clout2 := cosmos.NewUint(50)
	spent := cosmos.NewUint(0)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.IsZero(), check.Equals, true)
	c.Assert(split2.IsZero(), check.Equals, true)
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutOneSideIsZero(c *check.C) {
	clout1 := cosmos.NewUint(0)
	clout2 := cosmos.NewUint(100)
	spent := cosmos.NewUint(60)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.IsZero(), check.Equals, true)
	c.Assert(split2.String(), check.Equals, "60")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutBoundaryCondition(c *check.C) {
	clout1 := cosmos.NewUint(1)
	clout2 := cosmos.NewUint(100000000000)
	spent := cosmos.NewUint(2)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.String(), check.Equals, "1")
	c.Assert(split2.String(), check.Equals, "1")
}

func (s *HandlerCommonOutboundSuite) TestSplitCloutBothCloutsZero(c *check.C) {
	clout1 := cosmos.ZeroUint()
	clout2 := cosmos.ZeroUint()
	spent := cosmos.NewUint(60)

	split1, split2 := calcReclaim(clout1, clout2, spent)

	c.Assert(split1.IsZero(), check.Equals, true)
	c.Assert(split2.IsZero(), check.Equals, true)
}
