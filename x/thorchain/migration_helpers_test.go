package thorchain

import (
	"fmt"

	math "cosmossdk.io/math"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	tcyclaimlist "gitlab.com/thorchain/thornode/v3/common/tcyclaimlist"
)

type MigrationHelpersTestSuite struct{}

var _ = Suite(&MigrationHelpersTestSuite{})

// TestUnsafeAddRefundOutbound ensures migration refunds use the same
// compromised-vault exclusion as regular outbound scheduling, even though this
// helper selects an Active Asgard directly.
func (MigrationHelpersTestSuite) TestUnsafeAddRefundOutbound(c *C) {
	w := getHandlerTestWrapper(c, 1, true, true)

	// add a compromised vault
	vault := GetRandomVault()
	vault.PubKeyFrost = GetRandomEd25519PubKey()
	vault.Coins = common.Coins{
		// common.NewCoin(common.RuneAsset(), cosmos.NewUint(10000*common.One)),
		common.NewCoin(common.ETHAsset, cosmos.NewUint(10000*common.One)),
	}
	c.Assert(w.keeper.SetVault(w.ctx, vault), IsNil)

	// add a safe vault for the helper to select
	safeVault := GetRandomVault()
	safeVault.PubKeyFrost = GetRandomEd25519PubKey()
	safeVault.Coins = common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(10000*common.One)),
	}
	c.Assert(w.keeper.SetVault(w.ctx, safeVault), IsNil)

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
			Status:         common.Status_incomplete,
			BlockHeight:    1,
			Signers:        []string{w.activeNodeAccount.NodeAddress.String(), acc1.NodeAddress.String()},
			KeysignMs:      0,
			FinaliseHeight: 1,
		},
	})
	w.keeper.SetObservedTxInVoter(w.ctx, voter)

	mgr, ok := w.mgr.(*Mgrs)
	c.Assert(ok, Equals, true)
	w.keeper.SetMimir(w.ctx, compromisedVaultMimirKeyPrefix+vault.PubKey.String(), 1)

	// add outbound using migration helper
	err = unsafeAddRefundOutbound(w.ctx, mgr, inTxID.String(), ethAddr.String(), coin, height)
	c.Assert(err, IsNil)

	items, err := w.mgr.TxOutStore().GetOutboundItems(w.ctx)
	c.Assert(err, IsNil)
	c.Assert(items, HasLen, 1)
	c.Assert(items[0].Chain, Equals, common.ETHChain)
	c.Assert(items[0].InHash.Equals(inTxID), Equals, true)
	c.Assert(items[0].ToAddress.Equals(ethAddr), Equals, true)
	c.Assert(items[0].VaultPubKey.Equals(vault.PubKey), Equals, false)
	c.Assert(items[0].VaultPubKey.Equals(safeVault.PubKey), Equals, true)
	c.Assert(items[0].VaultPubKeyEddsa.Equals(safeVault.PubKeyEddsa), Equals, true)
	c.Assert(items[0].VaultPubKeyFrost.Equals(safeVault.PubKeyFrost), Equals, true)
	c.Assert(items[0].Coin.Equals(coin), Equals, true)
}

func (MigrationHelpersTestSuite) TestGetTCYClaimsFromData(c *C) {
	rawClaims := tcyclaimlist.GetTCYClaimsList()
	claimer, err := getTCYClaimsFromData()
	c.Assert(err, IsNil)
	c.Assert(len(claimer), Equals, len(rawClaims))

	sampleIndexes := []int{0, len(rawClaims) / 2, len(rawClaims) - 1}
	for _, idx := range sampleIndexes {
		rawClaim := rawClaims[idx]
		expectedAsset, err := common.NewAsset(rawClaim.Asset)
		c.Assert(err, IsNil)

		c.Assert(claimer[idx].Amount.Equal(math.NewUint(rawClaim.TCYClaim)), Equals, true)
		c.Assert(claimer[idx].Asset.Equals(expectedAsset), Equals, true)
		c.Assert(claimer[idx].L1Address.Equals(common.Address(rawClaim.Address)), Equals, true)
	}
}

func (MigrationHelpersTestSuite) TestSetTCYClaims(c *C) {
	ctx, mgr := setupManagerForTest(c)
	type claimExpectation struct {
		address common.Address
		asset   common.Asset
		amount  cosmos.Uint
	}

	rawClaims := tcyclaimlist.GetTCYClaimsList()
	expectations := make(map[string]claimExpectation)
	sampleKeys := make([]string, 0, 3)
	for _, rawClaim := range rawClaims {
		asset, err := common.NewAsset(rawClaim.Asset)
		c.Assert(err, IsNil)

		address := common.Address(rawClaim.Address)
		key := fmt.Sprintf("%s|%s", address.String(), asset.String())
		expectation, exists := expectations[key]
		if !exists {
			expectation = claimExpectation{
				address: address,
				asset:   asset,
				amount:  cosmos.ZeroUint(),
			}
			if len(sampleKeys) < 3 {
				sampleKeys = append(sampleKeys, key)
			}
		}
		expectation.amount = expectation.amount.Add(cosmos.NewUint(rawClaim.TCYClaim))
		expectations[key] = expectation
	}

	for _, key := range sampleKeys {
		expectation := expectations[key]
		_, err := mgr.Keeper().GetTCYClaimer(ctx, expectation.address, expectation.asset)
		c.Assert(err.Error(), Equals, fmt.Sprintf("TCYClaimer doesn't exist: %s", expectation.address.String()))
	}

	c.Assert(setTCYClaims(ctx, mgr), IsNil)

	for _, key := range sampleKeys {
		expectation := expectations[key]
		claimer, err := mgr.Keeper().GetTCYClaimer(ctx, expectation.address, expectation.asset)
		c.Assert(err, IsNil)
		c.Assert(claimer.Amount.Equal(expectation.amount), Equals, true)
		c.Assert(claimer.Asset.Equals(expectation.asset), Equals, true)
		c.Assert(claimer.L1Address.Equals(expectation.address), Equals, true)
	}
}
