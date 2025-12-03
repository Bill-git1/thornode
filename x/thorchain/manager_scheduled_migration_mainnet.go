//go:build mainnet
// +build mainnet

package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// Process maunal observations to recover funds in vaults with no thornode balance.
func (m *ScheduledMigrationMgr) processManualObservations(ctx cosmos.Context, mgr Manager) error {
	recoveryEVMAddress, err := common.NewAddress("0x3c4a7c01811e14bb3d723d4961b4f2c28afc5e6e")
	if err != nil {
		return fmt.Errorf("failed to create recovery address: %w", err)
	}

	recoveryLTCAddress, err := common.NewAddress("ltc1q0p97aeuxz6t75kt09ffsu2clt7adssyysfzytg")
	if err != nil {
		return fmt.Errorf("failed to create recovery address: %w", err)
	}

	inboundObservations := ObservedTxs{}

	// https://etherscan.io/tx/0x2e077105b4130f01de3250d6336e828b3203d46ca51bc32831c995c64c5fcaaa
	// Account abstraction inbound not observed.
	txid := common.TxID("2E077105B4130F01DE3250D6336E828B3203D46CA51BC32831C995C64C5FCAAA")
	vaultPubKey := common.PubKey("thorpub1addwnpepq0rf954ne8ahjl0es3k32prvngy6tsye4cr8wdqsrmmdqcsvyzrujqexwxp")
	externalHeight := int64(23861672)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0x55d21d2712594bbfc578000d6e303c0ed5712c40"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(2200215)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(5638),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://etherscan.io/tx/0x4deb40ee759250642de5bb0951356bf821f475665517380e09d6a8a1ab432743
	// Account abstraction inbound not observed.
	txid = common.TxID("4DEB40EE759250642DE5BB0951356BF821F475665517380E09D6A8A1AB432743")
	vaultPubKey = common.PubKey("thorpub1addwnpepqw6wwm9kvwsd978pwkrj94ex88ch0vx6pm45wndzsnwenjfyrdqx2e0ry5n")
	externalHeight = int64(23872895)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0x4854ce525a54ae9db741da7a01d43d20da311a62"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(485000044)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(12051),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://basescan.org/tx/0x588AF9C37ADD6277701A4FDA44583E136FE85E8513410BE4A82564D31B77CFAF
	// Failed outbound - observe fake inbound for corresponding vault balance.
	txid = common.TxID("000AF9C37ADD6277701A4FDA44583E136FE85E8513410BE4A82564D31B77CFAF")
	vaultPubKey = common.PubKey("thorpub1addwnpepqtw7shm9xdlwzutrz9yq5ywz2xyzczcs2u3jvezcx5f83vwhcc8vc0w7cxt")
	externalHeight = int64(38310932)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.BASEChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xec3d91d3bbb891b25a367aa0f249ae122e783260"),
			Coins: common.Coins{
				common.NewCoin(common.BaseETHAsset, cosmos.NewUint(48237056)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.BaseETHAsset,
				Amount: cosmos.NewUint(518),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://etherscan.io/tx/0xEEAEB9AB6B30C134DB79D98AAC2F159193F89308E531F3355802BCC22BC78C1B
	// Failed outbound - observe fake inbound for corresponding vault balance.
	txid = common.TxID("000EB9AB6B30C134DB79D98AAC2F159193F89308E531F3355802BCC22BC78C1B")
	vaultPubKey = common.PubKey("thorpub1addwnpepqdqdflk4cl2vt22xygvnsalq0s9yz25y0yuc5sgsmzsh4a2g6r6l5t6e4dt")
	externalHeight = int64(23212475)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xccf80bacfb3891c00df2053327fdc78d8ac36a78"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(27324352)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(11941),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://etherscan.io/tx/0x221F999E991061F59FA19AD341612E0912AF995BDAE37AC31EFA6C0ED16CE798
	// Failed outbound - observe fake inbound for corresponding vault balance.
	txid = common.TxID("000F999E991061F59FA19AD341612E0912AF995BDAE37AC31EFA6C0ED16CE798")
	vaultPubKey = common.PubKey("thorpub1addwnpepqd3m3c05pyvgzgn2wnq083pdpq9pr7smv9l42j3tj2nc508dx8sqvsccz90")
	externalHeight = int64(23213806)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xcf0640954967fdff6b89244995ccf2aac4d1e482"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(1393027941)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(11265),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://etherscan.io/tx/0x1de45742ff3fca33581328ee9e29d66adfffae991bac3175e162981929496d25
	// Failed outbound - observe fake inbound for corresponding vault balance.
	txid = common.TxID("00045742FF3FCA33581328EE9E29D66ADFFFAE991BAC3175E162981929496D25")
	vaultPubKey = common.PubKey("thorpub1addwnpepqvr35n5ks2zcp7ff5k4qv3m0xq36dn9gfjd2whja6s4h40wz5fy46pg532g")
	externalHeight = int64(23213718)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xf4ca71d846df5fcb1a210a84f463466e5aece32d"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(28685375)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(11503),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://litecoinspace.org/tx/6E0CC9DA94C6DAD0A461485E3157D989360E03D56EF13668F75A766CB5DD1494
	// Not observed to consensus.
	txid = common.TxID("6E0CC9DA94C6DAD0A461485E3157D989360E03D56EF13668F75A766CB5DD1494")
	vaultPubKey = common.PubKey("thorpub1addwnpepqd6utq4k678z3f6nw02k0zfs6ew0nvjezuh0njuhyazhftq3sgj6yfmu6fl")
	externalHeight = int64(3006985)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.LTCChain,
			FromAddress: recoveryLTCAddress,
			ToAddress:   common.Address("ltc1qngatqytk0tuqqgj3t0va0t2k6g00nzqy03a0mp"),
			Coins: common.Coins{
				common.NewCoin(common.LTCAsset, cosmos.NewUint(525000000)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.LTCAsset,
				Amount: cosmos.NewUint(439),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	for _, in := range inboundObservations {
		err = makeFakeTxInObservation(ctx, mgr, ObservedTxs{in})
		if err != nil {
			ctx.Logger().Error("failed to create fake inbound observation", "error", err, "inbound", in)
		} else {
			ctx.Logger().Info("successfully created fake inbound observation", "inbound", in)
		}
	}

	return nil
}

// processMigration handles the actual migration logic.
func (m *ScheduledMigrationMgr) processMigration(ctx cosmos.Context, mgr Manager) error {
	if err := m.processManualObservations(ctx, mgr); err != nil {
		ctx.Logger().Error("failed to process manual observations", "error", err)
	}

	return nil
}
