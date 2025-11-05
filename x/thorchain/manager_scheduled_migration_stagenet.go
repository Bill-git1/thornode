//go:build stagenet
// +build stagenet

package thorchain

import (
	"fmt"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

// processMigration handles the actual migration logic.
func (m *ScheduledMigrationMgr) processMigration(ctx cosmos.Context, mgr Manager) error {
	recoveryEVMAddress, err := common.NewAddress("0x9ac38f2e060f058c57a0898a788ad2b0dce862ca")
	if err != nil {
		return fmt.Errorf("failed to create recovery address: %w", err)
	}
	maxGasCoinETH, err := mgr.GasMgr().GetMaxGas(ctx, common.ETHChain)
	if err != nil {
		return fmt.Errorf("fail to get max gas: %w", err)
	}

	// balance has been placed in retired stagenet vault for this test
	txid := common.TxID("00048A12C1815688FCD3551967288DD1795C5107CFD4CC45D8FA09AE9CDB4849")
	vaultPubKey := common.PubKey("sthorpub1addwnpepqwncnmuawsa3klxm3g632etgpz7wknx2yyvfnjh6ypwu4367y57puxzecq3")
	refundCoin := common.NewCoin(common.ETHAsset, cosmos.NewUint(535844))
	refundCoin.Amount = refundCoin.Amount.Sub(maxGasCoinETH.Amount)
	out := TxOutItem{
		Chain:       common.ETHChain,
		InHash:      txid,
		ToAddress:   recoveryEVMAddress,
		VaultPubKey: vaultPubKey,
		Coin:        refundCoin,
		Memo:        fmt.Sprintf("REFUND:%s", txid),
		MaxGas:      common.Gas{maxGasCoinETH},
	}

	err = m.mgr.TxOutStore().UnSafeAddTxOutItem(ctx, m.mgr, out, ctx.BlockHeight())
	if err != nil {
		ctx.Logger().Error("failed to add manual outbound", "error", err, "outbound", out)
	} else {
		ctx.Logger().Info("successfully added manual outbound", "outbound", out)
	}

	return nil
}
