//go:build mainnet
// +build mainnet

package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// processManualObservations recovers funds in vaults with no thornode balance.
// Note: This function is naturally idempotent - if scheduled migrations are triggered
// multiple times, duplicate observations cause no change in vault balances or triggered
// actions. This was demonstrated in 3.14 when scheduled migrations ran multiple times
// with no adverse effects. Therefore, no explicit idempotency protection is needed.
func (m *ScheduledMigrationMgr) processManualObservations(ctx cosmos.Context, mgr Manager) error {
	recoveryEVMAddress, err := common.NewAddress("0x3c4a7c01811e14bb3d723d4961b4f2c28afc5e6e")
	if err != nil {
		return fmt.Errorf("failed to create recovery address: %w", err)
	}

	inboundObservations := ObservedTxs{}

	// https://etherscan.io/tx/0x8250565e8621f1a21d8d126f63dcd956ecc72926edee8a975e08b0ef1beca311
	// Account abstraction inbound not observed.
	txid := common.TxID("8250565E8621F1A21D8D126F63DCD956ECC72926EDEE8A975E08B0EF1BECA311")
	vaultPubKey := common.PubKey("thorpub1addwnpepqwht8xtersz90wsyu6fgx9tj5s3lx4w6q5adcm3mxzetgaa3rxv9sshhjmu")
	externalHeight := int64(24179184)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.ETHChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xeb0598673d2ab0e74a9514d9879280503ceed418"),
			Coins: common.Coins{
				common.NewCoin(common.ETHAsset, cosmos.NewUint(290142949)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.ETHAsset,
				Amount: cosmos.NewUint(1486),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://basescan.org/tx/0x6CFC8A78EBB8AEDAD11F7A4D18D84AA8D413E41E0F3C0776AA9801B3E7953535
	// https://basescan.org/tx/0x56F0E1B3765924D4DCFDE626AEE32B5A6C1AB3EA06D7E5B21A5DBAB47B566AB1
	// Account abstraction outbounds failed.
	//
	// Coalesce the two failed outbounds from the same vault into single recovery. Fake
	// txid with padded zeros to avoid collision with real txids.
	txid = common.TxID("000C8A78EBB8AEDAD11F7A4D18D84AA8D413E41E0F3C0776AA9801B3E7953535")
	vaultPubKey = common.PubKey("thorpub1addwnpepqwm8e5jdyjkm43hlf9mkm38vqvhj8cf6l3y9a745cn076u223vvyzxlaz2u")
	externalHeight = int64(40529982)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.BASEChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0x300d9ad8a3c52b52bf0b3ddcb897e62cfadb1ca6"),
			Coins: common.Coins{
				common.NewCoin(common.BaseETHAsset, cosmos.NewUint(35453457)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.BaseETHAsset,
				Amount: cosmos.NewUint(516),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://basescan.org/tx/0x725D6A1BA77857A5A7AFB19F58705C7DB2CB032C00A0E9A001FD0FE473713941
	// Account abstraction outbound failed.
	//
	// Fake txid with padded zeros to avoid collision with real txid.
	txid = common.TxID("000D6A1BA77857A5A7AFB19F58705C7DB2CB032C00A0E9A001FD0FE473713941")
	vaultPubKey = common.PubKey("thorpub1addwnpepqwku796ak4ke2hj356m8yfq549m3fs96t57sukgg9we7u6tsqs6ajrsandz")
	externalHeight = int64(40544316)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.BASEChain,
			FromAddress: recoveryEVMAddress,
			ToAddress:   common.Address("0xca1e95109a7bab3fe5e0ec480cc62b5f99b00d82"),
			Coins: common.Coins{
				common.NewCoin(common.BaseETHAsset, cosmos.NewUint(7348861)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.BaseETHAsset,
				Amount: cosmos.NewUint(800),
			}},
			Memo: "RECOVERY",
		},
		externalHeight, vaultPubKey, externalHeight,
	))

	// https://xrpscan.com/tx/4C7BA782EF00DAC8E3D5C24EC035B90A8CF87735B7DA1FF3C8F0D1A0628B0601
	// Inbound lacking consensus observation - observe and refund direct to user.
	txid = common.TxID("4C7BA782EF00DAC8E3D5C24EC035B90A8CF87735B7DA1FF3C8F0D1A0628B0601")
	vaultPubKey = common.PubKey("thorpub1addwnpepqdc348zt7v8pqrncxzf0gz47jz5jcey9tcfpvv7zlsj50qfgmw7nuj296rh")
	externalHeight = int64(101633228)
	inboundObservations = append(inboundObservations, NewObservedTx(
		common.Tx{
			ID:          txid,
			Chain:       common.XRPChain,
			FromAddress: common.Address("rE8rxiPiz4SHpapAmEHqxzgPcgQ7zSD7Kq"),
			ToAddress:   common.Address("rE6a19gyWwhozB8CUsamgXaf7CaTjPVpxZ"),
			Coins: common.Coins{
				common.NewCoin(common.XRPAsset, cosmos.NewUint(5000017000)),
			},
			Gas: common.Gas{common.Coin{
				Asset:  common.XRPAsset,
				Amount: cosmos.NewUint(500000),
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
