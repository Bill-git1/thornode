//go:build mainnet
// +build mainnet

package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// Inbounds to be manually brought to consensus:
// - 2CCD3793045076A60308711FF341215B727411C087D2955B20DD9358565CEFC5
// - 311A6D72B15DDB6FF24F831B13C387D98D28AFC06393C350D6C27579BD980611
func (m *ScheduledMigrationMgr) processManualObservations(ctx cosmos.Context, mgr Manager) error {
	recoveryLTCAddress, err := common.NewAddress("ltc1q0p97aeuxz6t75kt09ffsu2clt7adssyysfzytg")
	if err != nil {
		return fmt.Errorf("failed to create recovery LTC address: %w", err)
	}

	unobservedTxs := ObservedTxs{}

	// https://litecoinspace.org/tx/2CCD3793045076A60308711FF341215B727411C087D2955B20DD9358565CEFC5
	txid := common.TxID("2CCD3793045076A60308711FF341215B727411C087D2955B20DD9358565CEFC5")
	vaultPubKey := common.PubKey("thorpub1addwnpepqf4gn52qjlyjjlret6yhudg57qwm2hj2fvj96nevp8w2w885x38cu35kwq6")
	externalHeight := int64(2997764)
	unobservedTxs = append(unobservedTxs, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.LTCChain,
			FromAddress: recoveryLTCAddress,
			ToAddress:   common.Address("ltc1q5sjcwtyxfprqwe8mn6dfpe2s6kyxf4erwsur9y"),
			Coins: common.Coins{
				common.NewCoin(common.LTCAsset, cosmos.NewUint(10007247422)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.LTCAsset,
				Amount: cosmos.NewUint(28644),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://litecoinspace.org/tx/311A6D72B15DDB6FF24F831B13C387D98D28AFC06393C350D6C27579BD980611
	txid = common.TxID("311A6D72B15DDB6FF24F831B13C387D98D28AFC06393C350D6C27579BD980611")
	vaultPubKey = common.PubKey("thorpub1addwnpepqf4gn52qjlyjjlret6yhudg57qwm2hj2fvj96nevp8w2w885x38cu35kwq6")
	externalHeight = int64(2997762)
	unobservedTxs = append(unobservedTxs, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.LTCChain,
			FromAddress: recoveryLTCAddress,
			ToAddress:   common.Address("ltc1q5sjcwtyxfprqwe8mn6dfpe2s6kyxf4erwsur9y"),
			Coins: common.Coins{
				common.NewCoin(common.LTCAsset, cosmos.NewUint(3199000000)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.LTCAsset,
				Amount: cosmos.NewUint(470),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	for _, in := range unobservedTxs {
		err = makeFakeTxInObservation(ctx, mgr, ObservedTxs{in})
		if err != nil {
			ctx.Logger().Error("failed to create fake inbound observation", "error", err, "inbound", in)
		} else {
			ctx.Logger().Info("successfully created fake inbound observation", "inbound", in)
		}
	}

	return nil
}

// Retired vault refunds to be manually re-enqueued:
// - F5288AA5DAEE7B32546051F5C5818AA4F70CA252AAB6B5E64944E30D5A4DED48
// - A7AB916100A8A41A6410E0A575205258852663770CDF01E0BF24A4AF6300ED2E
// - B1A5DDE91EA8CA17F041627ACA14BC0E2CFF64F082654BA25A9B358485E6DFCE
// - D47A4FBDB18094CEDB541BF4E0604FF037C7BB91D97C3989D06EBFD4AA09253A
// - 258DE7AC769716EC273C0517FE51548AA394547B1274D825B3A82FE3BAB96A00
// - EC209C679F1983D9341DD0899FB46597C4F7977D7A1D69998ED5CF9365181F99
// - F46C220A24B70CF9E0A7EAEA34F9ED4E28F9FFDD0FCDC563709D4C3B93C4E3B5
// - 31BD1E0C1BC0ABA73E55386BC10FEE4494B4529019B8A2DB64C42C35E3123FF7 (issue #2267)
func (m *ScheduledMigrationMgr) processRetiredVaultRecoveries(ctx cosmos.Context, mgr Manager) error {
	recoveryEVMAddress, err := common.NewAddress("0x3c4a7c01811e14bb3d723d4961b4f2c28afc5e6e")
	if err != nil {
		return fmt.Errorf("failed to create recovery address: %w", err)
	}

	recoveryGAIAAddress, err := common.NewAddress("cosmos1tw7fk5j5puzwkqtxxkg6lz3qv2rn6htneanxs2")
	if err != nil {
		return fmt.Errorf("failed to create recovery GAIA address: %w", err)
	}

	recoveryXRPAddress, err := common.NewAddress("rUqpw8SyMtQypPWvUnazDNbNEPnXfUMRh5")
	if err != nil {
		return fmt.Errorf("failed to create recovery XRP address: %w", err)
	}

	maxGasCoinETH, err := mgr.GasMgr().GetMaxGas(ctx, common.ETHChain)
	if err != nil {
		return fmt.Errorf("fail to get max gas: %w", err)
	}

	maxGasCoinGAIA, err := mgr.GasMgr().GetMaxGas(ctx, common.GAIAChain)
	if err != nil {
		return fmt.Errorf("fail to get max gas: %w", err)
	}

	maxGasCoinBASE, err := mgr.GasMgr().GetMaxGas(ctx, common.BASEChain)
	if err != nil {
		return fmt.Errorf("fail to get max gas: %w", err)
	}

	maxGasCoinXRP, err := mgr.GasMgr().GetMaxGas(ctx, common.XRPChain)
	if err != nil {
		return fmt.Errorf("fail to get max gas: %w", err)
	}

	ethUSDC, err := common.NewAsset("ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48")
	if err != nil {
		return fmt.Errorf("failed to create asset: %w", err)
	}

	baseUSDC, err := common.NewAsset("BASE.USDC-0X833589FCD6EDB6E08F4C7C32D4F71B54BDA02913")
	if err != nil {
		return fmt.Errorf("failed to create asset: %w", err)
	}

	var outbounds []TxOutItem

	txid := common.TxID("F5288AA5DAEE7B32546051F5C5818AA4F70CA252AAB6B5E64944E30D5A4DED48")
	vaultPubKey := common.PubKey("thorpub1addwnpepqgcr65ua7393ff3fyg9se9uvggsjlcmu7yhu7unjlv5k28tq5depw6643hd")
	refundCoin := common.NewCoin(common.ATOMAsset, cosmos.NewUint(16300000000))
	refundCoin.Amount = refundCoin.Amount.Sub(maxGasCoinGAIA.Amount)
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.GAIAChain,
		InHash:      txid,
		ToAddress:   recoveryGAIAAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinGAIA},
	})

	txid = common.TxID("A7AB916100A8A41A6410E0A575205258852663770CDF01E0BF24A4AF6300ED2E")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(ethUSDC, cosmos.NewUint(49983021600))
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.ETHChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinETH},
	})

	txid = common.TxID("B1A5DDE91EA8CA17F041627ACA14BC0E2CFF64F082654BA25A9B358485E6DFCE")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(common.ATOMAsset, cosmos.NewUint(2517500000))
	refundCoin.Amount = refundCoin.Amount.Sub(maxGasCoinGAIA.Amount)
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.GAIAChain,
		InHash:      txid,
		ToAddress:   recoveryGAIAAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinGAIA},
	})

	txid = common.TxID("D47A4FBDB18094CEDB541BF4E0604FF037C7BB91D97C3989D06EBFD4AA09253A")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(baseUSDC, cosmos.NewUint(20000000000))
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.BASEChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinBASE},
	})

	txid = common.TxID("258DE7AC769716EC273C0517FE51548AA394547B1274D825B3A82FE3BAB96A00")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(ethUSDC, cosmos.NewUint(64100000000))
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.ETHChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinETH},
	})

	txid = common.TxID("EC209C679F1983D9341DD0899FB46597C4F7977D7A1D69998ED5CF9365181F99")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(ethUSDC, cosmos.NewUint(10200000000))
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.ETHChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinETH},
	})

	txid = common.TxID("F46C220A24B70CF9E0A7EAEA34F9ED4E28F9FFDD0FCDC563709D4C3B93C4E3B5")
	vaultPubKey = common.PubKey("thorpub1addwnpepq24ps386qn5vd7scgs6tq83gtv6wekpzauuawukva2arumknf5gtckk0pve")
	refundCoin = common.NewCoin(baseUSDC, cosmos.NewUint(18189131400))
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.BASEChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinBASE},
	})

	txid = common.TxID("31BD1E0C1BC0ABA73E55386BC10FEE4494B4529019B8A2DB64C42C35E3123FF7")
	vaultPubKey = common.PubKey("thorpub1addwnpepqfqurecj24sanfuy54h3e8r6extldwnapwkfnf248trpnrttqf4twunu8yq")
	refundCoin = common.NewCoin(common.XRPAsset, cosmos.NewUint(667272152300))
	refundCoin.Amount = refundCoin.Amount.Sub(maxGasCoinXRP.Amount)
	outbounds = append(outbounds, TxOutItem{
		Chain:       common.XRPChain,
		InHash:      txid,
		ToAddress:   recoveryXRPAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinXRP},
	})

	for _, out := range outbounds {
		outboundHeight := ctx.BlockHeight()
		err = m.mgr.TxOutStore().UnSafeAddTxOutItem(ctx, m.mgr, out, outboundHeight)
		if err != nil {
			ctx.Logger().Error("failed to add manual outbound", "error", err, "outbound", out)
		} else {
			ctx.Logger().Info("successfully added manual outbound", "outbound", out)
		}
	}

	return nil
}

// processMigration handles the actual migration logic.
func (m *ScheduledMigrationMgr) processMigration(ctx cosmos.Context, mgr Manager) error {
	if err := m.processManualObservations(ctx, mgr); err != nil {
		ctx.Logger().Error("failed to process manual observations", "error", err)
	}

	if err := m.processRetiredVaultRecoveries(ctx, mgr); err != nil {
		ctx.Logger().Error("failed to process retired vault recoveries", "error", err)
	}

	return nil
}
