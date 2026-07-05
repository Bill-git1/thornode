//go:build mainnet
// +build mainnet

package thorchain

import (
	"fmt"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// TestMigrate16to17DanglingSettlementSpecs mirrors the migration's hard-coded
// mainnet rows in synthetic state so reviewers can see the full migration flow
// exercised for every settlement spec we intend to resolve on-chain.
func (s *MigrationCommonSuite) TestMigrate16to17DanglingSettlementSpecs(c *C) {
	ctx, mgr := setupManagerForTest(c)
	ctx = ctx.WithBlockHeight(1000)

	specs, err := mainnetDanglingSettlementSpecs16to17()
	c.Assert(err, IsNil)
	c.Assert(specs, HasLen, 26)

	saveMigrationNetworkFee(c, ctx, mgr, common.ETHChain)
	saveMigrationNetworkFee(c, ctx, mgr, common.GAIAChain)
	saveMigrationNetworkFee(c, ctx, mgr, common.BTCChain)
	saveMigrationNetworkFee(c, ctx, mgr, common.LTCChain)

	ethUSDT := mustMigrationAsset(c, "ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7")
	ethUSDC := mustMigrationAsset(c, "ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48")
	ethWBTC := mustMigrationAsset(c, "ETH.WBTC-0X2260FAC5E5542A773AA44FBCFEDF7C193BC2C599")

	activeVault := GetRandomVault()
	activeVault.PubKeyFrost = GetRandomEd25519PubKey()
	activeVault.Chains = common.Chains{common.ETHChain, common.GAIAChain, common.BTCChain, common.LTCChain}.Strings()
	activeVault.Coins = common.Coins{
		common.NewCoin(common.ETHAsset, cosmos.NewUint(1_000*common.One)),
		common.NewCoin(ethUSDT, cosmos.NewUint(10_000_000*common.One)),
		common.NewCoin(ethUSDC, cosmos.NewUint(10_000_000*common.One)),
		common.NewCoin(ethWBTC, cosmos.NewUint(10*common.One)),
		common.NewCoin(common.ATOMAsset, cosmos.NewUint(1_000_000*common.One)),
		common.NewCoin(common.BTCAsset, cosmos.NewUint(1_000*common.One)),
		common.NewCoin(common.LTCAsset, cosmos.NewUint(1_000_000*common.One)),
	}
	c.Assert(mgr.Keeper().SetVault(ctx, activeVault), IsNil)

	originalActions := make(map[string]TxOutItem, len(specs))
	for i, spec := range specs {
		oldVault := GetRandomVault()
		oldVault.Status = RetiringVault
		oldVault.PubKeyFrost = GetRandomEd25519PubKey()
		oldVault.Chains = common.Chains{spec.Chain}.Strings()
		c.Assert(mgr.Keeper().SetVault(ctx, oldVault), IsNil)

		action := TxOutItem{
			Chain:            spec.Chain,
			InHash:           spec.TxID,
			ToAddress:        migrationTestAddressForChain(c, spec.Chain),
			VaultPubKey:      oldVault.PubKey,
			VaultPubKeyEddsa: txOutVaultPubKeyEddsa(spec.Chain, oldVault.PubKeyEddsa),
			VaultPubKeyFrost: oldVault.PubKeyFrost,
			Coin:             common.NewCoin(spec.Asset, spec.Amount),
			MaxGas:           common.Gas{common.NewCoin(spec.Chain.GetGasAsset(), cosmos.OneUint())},
			GasRate:          1,
			Memo:             fmt.Sprintf("MIGRATION-TEST:%02d", i),
		}
		voter := NewObservedTxVoter(spec.TxID, nil)
		voter.Actions = []TxOutItem{action}
		voter.FinalisedHeight = 1
		mgr.Keeper().SetObservedTxInVoter(ctx, voter)
		originalActions[spec.TxID.String()] = action
	}

	err = NewMigrator(mgr).Migrate16to17(ctx)
	c.Assert(err, IsNil)

	outboundHeight := ctx.BlockHeight() + 300
	txOut, err := mgr.Keeper().GetTxOut(ctx, outboundHeight)
	c.Assert(err, IsNil)
	c.Assert(txOut.TxArray, HasLen, len(specs))

	queued := make(map[string]TxOutItem, len(txOut.TxArray))
	for _, item := range txOut.TxArray {
		queued[item.InHash.String()] = item
	}

	for _, spec := range specs {
		key := spec.TxID.String()
		original := originalActions[key]
		item, ok := queued[key]
		c.Assert(ok, Equals, true, Commentf("missing queued item for %s", key))

		c.Assert(item.Chain.Equals(spec.Chain), Equals, true, Commentf(key))
		c.Assert(item.ToAddress.Equals(original.ToAddress), Equals, true, Commentf(key))
		c.Assert(item.Coin.Equals(original.Coin), Equals, true, Commentf(key))
		c.Assert(item.Memo, Equals, original.Memo, Commentf(key))
		c.Assert(item.VaultPubKey.Equals(activeVault.PubKey), Equals, true, Commentf(key))
		c.Assert(item.VaultPubKeyEddsa.Equals(txOutVaultPubKeyEddsa(spec.Chain, activeVault.PubKeyEddsa)), Equals, true, Commentf(key))
		c.Assert(item.VaultPubKeyFrost.Equals(activeVault.PubKeyFrost), Equals, true, Commentf(key))
		c.Assert(item.GasRate, Equals, int64(mgr.GasMgr().GetGasRate(ctx, spec.Chain).Uint64()), Commentf(key))

		maxGas, err := mgr.GasMgr().GetMaxGas(ctx, spec.Chain)
		c.Assert(err, IsNil, Commentf(key))
		c.Assert(item.MaxGas.Equals(common.Gas{maxGas}), Equals, true, Commentf(key))

		voter, err := mgr.Keeper().GetObservedTxInVoter(ctx, spec.TxID)
		c.Assert(err, IsNil, Commentf(key))
		c.Assert(voter.FinalisedHeight, Equals, ctx.BlockHeight(), Commentf(key))
		c.Assert(voter.Actions, HasLen, 1, Commentf(key))
		c.Assert(voter.Actions[0].VaultPubKey.Equals(item.VaultPubKey), Equals, true, Commentf(key))
		c.Assert(voter.Actions[0].VaultPubKeyEddsa.Equals(item.VaultPubKeyEddsa), Equals, true, Commentf(key))
		c.Assert(voter.Actions[0].VaultPubKeyFrost.Equals(item.VaultPubKeyFrost), Equals, true, Commentf(key))
		c.Assert(voter.Actions[0].MaxGas.Equals(item.MaxGas), Equals, true, Commentf(key))
		c.Assert(voter.Actions[0].GasRate, Equals, item.GasRate, Commentf(key))
	}
}

func migrationTestAddressForChain(c *C, chain common.Chain) common.Address {
	switch {
	case chain.Equals(common.ETHChain):
		return GetRandomETHAddress()
	case chain.Equals(common.GAIAChain):
		return GetRandomGAIAAddress()
	case chain.Equals(common.BTCChain):
		return GetRandomBTCAddress()
	case chain.Equals(common.LTCChain):
		return GetRandomLTCAddress()
	default:
		c.Fatalf("unsupported migration test chain: %s", chain.String())
		return common.NoAddress
	}
}
