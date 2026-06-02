//go:build mainnet
// +build mainnet

package thorchain

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	v2 "gitlab.com/thorchain/thornode/v3/x/thorchain/migrations/v2"
)

// Migrator is a struct for handling in-place store migrations.
type Migrator struct {
	mgr *Mgrs
}

// NewMigrator returns a new Migrator.
func NewMigrator(mgr *Mgrs) Migrator {
	return Migrator{mgr: mgr}
}

// Migrate1to2 migrates from version 1 to 2.
func (m Migrator) Migrate1to2(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}
	return v2.MigrateStore(ctx, m.mgr.storeService)
}

// Migrate2to3 migrates from version 2 to 3.
func (m Migrator) Migrate2to3(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// refund stagenet funding wallet for user refund
	// original user tx: https://runescan.io/tx/A9AF3ED203079BB246CEE0ACD837FBA024BC846784DE488D5BE70044D8877C52
	// refund to user from stagenet funding wallet: https://bscscan.com/tx/0xba67f3a88f8c998f29e774ffa8328e5625521e37c2db282b29a04ab3d2593f48
	stagenetWallet := "0x3021C479f7F8C9f1D5c7d8523BA5e22C0Bcb5430"
	inTxId := "A9AF3ED203079BB246CEE0ACD837FBA024BC846784DE488D5BE70044D8877C52" // original user tx

	bscUsdt, err := common.NewAsset("BSC.USDT-0X55D398326F99059FF775485246999027B3197955")
	if err != nil {
		return err
	}
	usdtCoin := common.NewCoin(bscUsdt, cosmos.NewUint(4860737515919))
	blockHeight := ctx.BlockHeight()

	// schedule refund
	if err := unsafeAddRefundOutbound(ctx, m.mgr, inTxId, stagenetWallet, usdtCoin, blockHeight); err != nil {
		return err
	}

	return nil
}

// Migrate3to4 migrates from version 4 to 5.
func (m Migrator) Migrate4to5(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// ------------------------------ TCY ------------------------------

	totalTCYCoin := common.NewCoin(common.TCY, cosmos.NewUint(210_000_000_00000000))
	err := m.mgr.Keeper().MintToModule(ctx, ModuleName, totalTCYCoin)
	if err != nil {
		return err
	}

	// Claims 206_606_541_28874864
	claimingModuleCoin := common.NewCoin(common.TCY, cosmos.NewUint(206_606_541_28874864))
	err = m.mgr.Keeper().SendFromModuleToModule(ctx, ModuleName, TCYClaimingName, common.NewCoins(claimingModuleCoin))
	if err != nil {
		return err
	}

	// 210M minus claims: 206_606_541_28874864
	treasuryCoin := common.NewCoin(common.TCY, totalTCYCoin.Amount.Sub(claimingModuleCoin.Amount))
	treasuryAddress, err := common.NewAddress("thor10qh5272ktq4wes8ex343ky9rsuehcypddjh08k")
	if err != nil {
		return err
	}

	treasuryAccAddress, err := treasuryAddress.AccAddress()
	if err != nil {
		return err
	}

	err = m.mgr.Keeper().SendFromModuleToAccount(ctx, ModuleName, treasuryAccAddress, common.NewCoins(treasuryCoin))
	if err != nil {
		return err
	}

	err = setTCYClaims(ctx, m.mgr)
	if err != nil {
		return err
	}

	// ------------------------------ Bond Slash Refund ------------------------------

	for _, slashRefund := range mainnetSlashRefunds4to5 {
		recipient, err := cosmos.AccAddressFromBech32(slashRefund.address)
		if err != nil {
			ctx.Logger().Error("error parsing address in store migration", "error", err)
			continue
		}
		amount := cosmos.NewUint(slashRefund.amount)
		refundCoins := common.NewCoins(common.NewCoin(common.RuneAsset(), amount))
		if err := m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, recipient, refundCoins); err != nil {
			ctx.Logger().Error("fail to store migration transfer RUNE from Reserve to recipient", "error", err, "recipient", recipient, "amount", amount)
		}
	}

	// ------------------------------ Mimir Cleanup ------------------------------

	return m.ClearObsoleteMimirs(ctx)
}

// Migrate5to6 migrates from version 5 to 6.
func (m Migrator) Migrate5to6(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// ------------------------------ Bond Slash Refund ------------------------------

	// Validate Reserve module has sufficient funds before starting refunds
	totalRefundAmount := cosmos.NewUint(14856919212689) // Total amount to be refunded
	reserveBalance := m.mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)
	if reserveBalance.LT(totalRefundAmount) {
		return fmt.Errorf("insufficient reserve balance for migration: have %s, need %s",
			reserveBalance.String(), totalRefundAmount.String())
	}
	ctx.Logger().Info("Reserve balance validation passed",
		"reserve_balance", reserveBalance.String(),
		"required_amount", totalRefundAmount.String())

	for _, slashRefund := range mainnetSlashRefunds5to6 {
		recipient, err := cosmos.AccAddressFromBech32(slashRefund.address)
		if err != nil {
			ctx.Logger().Error("error parsing address in store migration",
				"error", err,
				"address", slashRefund.address)
			continue
		}
		amount := cosmos.NewUint(slashRefund.amount)
		refundCoins := common.NewCoins(common.NewCoin(common.RuneAsset(), amount))
		if err := m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, recipient, refundCoins); err != nil {
			ctx.Logger().Error("fail to store migration transfer RUNE from Reserve to recipient",
				"error", err,
				"recipient", recipient.String(),
				"address", slashRefund.address,
				"amount", amount.String())
		} else {
			ctx.Logger().Debug("successfully transferred bond slash refund",
				"recipient", recipient.String(),
				"amount", amount.String())
		}
	}

	return nil
}

// Migrate6to7 migrates from version 6 to 7.
func (m Migrator) Migrate6to7(ctx sdk.Context) error {
	// loads the manager for this migration
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// handle manual outbounds
	outbounds, err := mainnetManualOutbounds6to7(ctx, m.mgr)
	if err != nil {
		ctx.Logger().Error("failed to create manual outbounds for migration", "error", err)
	}
	for _, out := range outbounds {
		// schedule outbound for 30 minutes after upgrade height
		outboundHeight := ctx.BlockHeight() + 300
		err := m.mgr.TxOutStore().UnSafeAddTxOutItem(ctx, m.mgr, out, outboundHeight)
		if err != nil {
			ctx.Logger().Error("failed to add manual outbound", "error", err, "outbound", out)
		} else {
			ctx.Logger().Info("successfully added manual outbound", "outbound", out)
		}
	}

	// handle manual observations of dropped inbounds
	inbounds, err := mainnetManualInbounds6to7()
	if err != nil {
		ctx.Logger().Error("failed to create manual inbounds for migration", "error", err)
	}
	for _, in := range inbounds {
		err = makeFakeTxInObservation(ctx, m.mgr, ObservedTxs{in})
		if err != nil {
			ctx.Logger().Error("failed to create fake inbound observation", "error", err, "inbound", in)
		} else {
			ctx.Logger().Info("successfully created fake inbound observation", "inbound", in)
		}
	}

	return nil
}

// makeFakeTxInObservation accepts an array of unobserved inbounds, queries for active
// node accounts, and makes a fake observation for each validator and unobserved TxIn.
// Returns an error if there are no active validators or if every per-validator handler
// invocation failed, so that callers can distinguish total failure from success.
func makeFakeTxInObservation(ctx cosmos.Context, mgr Manager, txs ObservedTxs) error {
	activeNodes, err := mgr.Keeper().ListActiveValidators(ctx)
	if err != nil {
		ctx.Logger().Error("Failed to get active nodes", "err", err)
		return err
	}
	if len(activeNodes) == 0 {
		return fmt.Errorf("no active validators to record fake observation")
	}

	handler := NewObservedTxInHandler(mgr)

	successCount := 0
	for _, na := range activeNodes {
		txInMsg := NewMsgObservedTxIn(txs, na.NodeAddress)
		_, err := handler.handle(ctx, *txInMsg)
		if err != nil {
			ctx.Logger().Error("failed ObservedTxIn handler", "error", err)
			continue
		}
		successCount++
	}

	if successCount == 0 {
		return fmt.Errorf("all %d ObservedTxIn handler invocations failed", len(activeNodes))
	}

	return nil
}

// Migrate8to9 migrates from version 8 to 9.
func (m Migrator) Migrate8to9(ctx sdk.Context) error {
	// loads the manager for this migration
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	err := m.CommonMigrate8to9(ctx)
	if err != nil {
		return err
	}

	// handle manual outbounds
	outbounds, err := mainnetManualOutbounds8to9(ctx, m.mgr)
	if err != nil {
		ctx.Logger().Error("failed to create manual outbounds for migration", "error", err)
	}
	for _, out := range outbounds {
		outboundHeight := ctx.BlockHeight()
		err := m.mgr.TxOutStore().UnSafeAddTxOutItem(ctx, m.mgr, out, outboundHeight)
		if err != nil {
			ctx.Logger().Error("failed to add manual outbound", "error", err, "outbound", out)
		} else {
			ctx.Logger().Info("successfully added manual outbound", "outbound", out)
		}
	}

	return nil
}

// Migrate9to10 migrates from version 9 to 10.
func (m Migrator) Migrate9to10(ctx sdk.Context) error {
	// loads the manager for this migration
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// move excess thor.nami to treasury
	coins := common.Coins{{
		Asset:  common.NAMI,
		Amount: cosmos.NewUint(524245),
	}}
	err := m.mgr.Keeper().SendFromModuleToModule(
		ctx, AsgardName, TreasuryName, coins,
	)
	if err != nil {
		ctx.Logger().Error("failed to move excess nami to treasury", "error", err)
	} else {
		ctx.Logger().Info("successfully moved excess nami to treasury", "coins", coins)
	}

	// re-attempt recoveries
	outbounds, err := mainnetManualOutbounds9to10(ctx, m.mgr)
	if err != nil {
		ctx.Logger().Error("failed to create manual outbounds for migration", "error", err)
	}
	for _, out := range outbounds {
		outboundHeight := ctx.BlockHeight()
		err := m.mgr.TxOutStore().UnSafeAddTxOutItem(ctx, m.mgr, out, outboundHeight)
		if err != nil {
			ctx.Logger().Error("failed to add manual outbound", "error", err, "outbound", out)
		} else {
			ctx.Logger().Info("successfully added manual outbound", "outbound", out)
		}
	}

	return nil
}

// Migrate10to11 migrates from version 10 to 11.
func (m Migrator) Migrate10to11(ctx sdk.Context) error {
	return nil
}

// Migrate11to12 migrates from version 11 to 12.
func (m Migrator) Migrate11to12(ctx sdk.Context) error {
	// loads the manager for this migration
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// avax.usdc asset
	avaxUSDC, err := common.NewAsset("AVAX.USDC-0XB97EF9EF8734C71904D8002F8B6BC66DD9C48A6E")
	if err != nil {
		return fmt.Errorf("failed to parse AVAX.USDC asset: %w", err)
	}

	// records to be removed
	removeLPs := []LiquidityProvider{
		// https://gateway.liquify.com/chain/thorchain_api/cosmos/tx/v1beta1/txs/5BF8911A9DD947EC1EE7E990085ECB21DD7E395F152B5137F7CD73B76E46870A
		{
			RuneAddress:  "thor1pe0pspu4ep85gxr5h9l6k49g024vemtr80hg4c",
			AssetAddress: "0x03c42ab083bd46202ee430AfC4D3dd8eD8c76c07",
		},
		// https://gateway.liquify.com/chain/thorchain_api/cosmos/tx/v1beta1/txs/248CE4B07E6A63E8CA3E111754849DD560FA9F4BF83BF2FD0B0B40947633B1B9
		{
			RuneAddress:  "",
			AssetAddress: "0x467a0ec2d2f23f0bb165eb7e44a9b16b12f5a7b4",
		},
		// affiliate position for the prior add liquidity transaction
		{
			RuneAddress:  "thor122h9hlrugzdny9ct95z6g7afvpzu34s73uklju",
			AssetAddress: "",
		},
	}

	// get the pool
	pool, err := m.mgr.Keeper().GetPool(ctx, avaxUSDC)
	if err != nil {
		return fmt.Errorf("failed to get AVAX.USDC pool: %w", err)
	}

	for _, lp := range removeLPs {
		// fetch the record
		lp, err = m.mgr.Keeper().GetLiquidityProvider(ctx, avaxUSDC, lp.GetAddress())
		if err != nil {
			ctx.Logger().Error("failed to get liquidity provider ",
				"error", err,
				"rune_address", lp.RuneAddress,
				"asset_address", lp.AssetAddress)
			continue
		}

		// remove the units from the pool
		pool.LPUnits = common.SafeSub(pool.LPUnits, lp.Units)
		ctx.Logger().Info("adjusted pool LP units",
			"asset", avaxUSDC.String(),
			"removed_units", lp.Units.String(),
			"new_pool_lp_units", pool.LPUnits.String())

		// remove the record
		m.mgr.Keeper().RemoveLiquidityProvider(ctx, lp)
		ctx.Logger().Info(
			"removed bad liquidity provider record",
			"asset", avaxUSDC.String(),
			"rune_address", lp.RuneAddress,
			"asset_address", lp.AssetAddress,
		)
	}

	// save the updated pool
	if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
		return fmt.Errorf("failed to save updated pool: %w", err)
	}

	// remove obsolete/typo mimir keys
	obsoleteMimirs := []string{
		"HALTRADING",
		"ADR012",
		"ADR020",
		"ADR18",
		"DEPRECATEILP",
		"FULLIMPLOSSPROTECTIONBLOCKS",
		"ILPCUTOFF",
		"KILLSWITCHSTART",
		"PROPOSAL6",
		"VOTEDOFM",
		"VOTELENDING",
		"VOTEMAXSYNTHSFORSAVERSYIELD",
		"ENABLEAVAXCHAIN",
		"ENABLEBSC",
		"ENABLESAVINGSVAULTS",
		"ADD-CHAIN-BASE",
		"ADD-CHAIN-XRP",
	}

	for _, key := range obsoleteMimirs {
		if err := m.mgr.Keeper().DeleteMimir(ctx, key); err != nil {
			ctx.Logger().Error("failed to delete obsolete mimir", "key", key, "error", err)
			continue
		}
		ctx.Logger().Info("deleted obsolete mimir", "key", key)
	}

	// refund 43F310A416A4ED8CF8B645B1EBBB5E25FB89F9777A4350F7023DEB62B90EA3AD
	refundRune := common.NewCoin(common.RuneNative, cosmos.NewUint(40000000000))
	userAddr, err := common.NewAddress("thor1dvvr4kdeurs8fdwgrql6je7l2v9ma73dp50n7m")
	if err != nil {
		return err
	}
	userAccAddr, err := userAddr.AccAddress()
	if err != nil {
		return err
	}
	err = m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, userAccAddr, common.NewCoins(refundRune))
	if err != nil {
		return err
	}

	return nil
}

// Migrate12to13 migrates from version 12 to 13.
func (m Migrator) Migrate12to13(ctx sdk.Context) error {
	// Loads the manager for this migration (we are in the x/upgrade's preblock)
	// Note, we do not require the manager loaded for this migration, but it is okay
	// to load it earlier and this is the pattern for migrations to follow.
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// ------------------------------ Bond Slash Refund ------------------------------

	// Validate Reserve module has sufficient funds before starting refunds
	totalRefundAmount := cosmos.NewUint(mainnetSlashRefunds12to13Total) // Total amount to be refunded
	reserveBalance := m.mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)
	if reserveBalance.LT(totalRefundAmount) {
		return fmt.Errorf("insufficient reserve balance for migration: have %s, need %s",
			reserveBalance.String(), totalRefundAmount.String())
	}
	ctx.Logger().Info("Reserve balance validation passed",
		"reserve_balance", reserveBalance.String(),
		"required_amount", totalRefundAmount.String())

	for _, slashRefund := range mainnetSlashRefunds12to13 {
		recipient, err := cosmos.AccAddressFromBech32(slashRefund.address)
		if err != nil {
			ctx.Logger().Error("error parsing address in store migration",
				"error", err,
				"address", slashRefund.address)
			continue
		}
		amount := cosmos.NewUint(slashRefund.amount)
		refundCoins := common.NewCoins(common.NewCoin(common.RuneAsset(), amount))
		if err := m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, recipient, refundCoins); err != nil {
			ctx.Logger().Error("fail to store migration transfer RUNE from Reserve to recipient",
				"error", err,
				"recipient", recipient.String(),
				"address", slashRefund.address,
				"amount", amount.String())
		} else {
			ctx.Logger().Debug("successfully transferred bond slash refund",
				"recipient", recipient.String(),
				"amount", amount.String())
		}
	}

	return nil
}

// Migrate13to14 migrates from version 13 to 14.
func (m Migrator) Migrate13to14(ctx sdk.Context) error {
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// ADR-023: Burn ~87% of Reserve and reduce MaxRuneSupply to 360M.
	return m.BurnReserveAndReduceMaxSupply(ctx)
}

// Migrate14to15 migrates from version 14 to 15.
//
// Recovers state from the DOGE asgard migration double-broadcast at block
// 26,001,464 (slash tx 1DFE09ABE8D8DD11BA55BD6B499AC1EF91FA0B5CA83D492AB12C08AC89C2B54D)
// where the retiring vault transmitted the same migration outbound twice,
// draining 122,221,658,117,429 sats and triggering a 200% bond slash on the
// 19 vault members. The slash was later absorbed by each member's bond
// providers proportionally via bp.Adjust() during subsequent reward
// distributions.
//
// Four corrections, in order:
//
//  1. Reverse the pool slash event: pool.BalanceAsset += 122,221,658,117,429,
//     pool.BalanceRune -= 26,008,730,208,467. A strict-subtract guards against
//     pool drift between the slash block and the migration block — if the
//     pool's RUNE depth dropped below the credited amount (e.g. via LP
//     activity), the migration aborts cleanly rather than silently clamping.
//  2. Move 26,008,730,208,467 RUNE from Asgard back to Reserve, undoing the
//     bond->asgard transfer that backed the (now-reversed) pool credit.
//  3. Refund the slash directly from Reserve to each provider's wallet, using
//     the pre-computed bond deltas in mainnetSlashRefunds14to15. Per-row sends
//     log-and-continue on failure (matches Migrate4to5 / 5to6 / 12to13). na.Bond
//     is intentionally NOT touched: some slashed nodes will have churned out
//     and unbonded by the time this runs, and others will have a different
//     provider distribution than at slash time. Crediting na.Bond would also
//     trigger a NodeOperatorFee skim on the next bp.Adjust() since providers
//     have already absorbed the slash.
//  4. Reconcile phantom credit on two retiring asgard ETH vaults caused by
//     SafeSub clamping in vault.SubFunds during outbound observation when the
//     vault was already drained. See fixVaultClamps_v3_18_0 and
//     mainnetVaultClampFixes14to15 for details. Pre-flight check skips any
//     entry that would clamp again (loud error logged) — better to leave the
//     phantom credit in place than halt the upgrade.
//
// Net coin movement: Asgard -26B, Reserve 0 (in +26B from Asgard, out -52B to
// providers, was +26B from original slash), providers' wallets +52B total.
// ETH vault Coins reduced by 270.427671 ETH across two retiring vaults (no
// module/account movement — purely a vault.Coins ledger correction).
func (m Migrator) Migrate14to15(ctx sdk.Context) error {
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	const (
		stolenDogeSats   = uint64(122_221_658_117_429)
		runeCreditedSats = uint64(26_008_730_208_467)
		asgardToReserve  = uint64(26_008_730_208_467)
	)

	// Step 1: reverse pool slash event.
	pool, err := m.mgr.Keeper().GetPool(ctx, common.DOGEAsset)
	if err != nil {
		return fmt.Errorf("fail to get DOGE pool: %w", err)
	}
	if pool.IsEmpty() {
		return fmt.Errorf("DOGE pool is empty, cannot reverse slash")
	}
	runeCredit := cosmos.NewUint(runeCreditedSats)
	if pool.BalanceRune.LT(runeCredit) {
		return fmt.Errorf(
			"DOGE pool BalanceRune (%s) below expected slash credit (%d); "+
				"pool drifted between slash and migration — investigate before retrying",
			pool.BalanceRune.String(), runeCreditedSats,
		)
	}
	pool.BalanceAsset = pool.BalanceAsset.Add(cosmos.NewUint(stolenDogeSats))
	pool.BalanceRune = pool.BalanceRune.Sub(runeCredit)
	if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
		return fmt.Errorf("fail to save DOGE pool: %w", err)
	}
	poolSlashAmt := []PoolAmt{
		{Asset: common.DOGEAsset, Amount: int64(stolenDogeSats)},
		{Asset: common.RuneAsset(), Amount: -int64(runeCreditedSats)},
	}
	if err := m.mgr.EventMgr().EmitEvent(ctx, NewEventSlash(common.DOGEAsset, poolSlashAmt)); err != nil {
		ctx.Logger().Error("fail to emit pool reversal event", "error", err)
	}

	// Step 2: Asgard -> Reserve.
	asgardCoin := common.NewCoin(common.RuneAsset(), cosmos.NewUint(asgardToReserve))
	if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ReserveName, common.NewCoins(asgardCoin)); err != nil {
		return fmt.Errorf("fail to move asgard->reserve: %w", err)
	}

	// Step 3: Reserve -> per-provider direct refund.
	// Pre-flight Reserve balance check (matches Migrate5to6 / 12to13).
	totalRefundAmount := cosmos.NewUint(mainnetSlashRefunds14to15Total)
	reserveBalance := m.mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName)
	if reserveBalance.LT(totalRefundAmount) {
		return fmt.Errorf("insufficient reserve balance for migration: have %s, need %s",
			reserveBalance.String(), totalRefundAmount.String())
	}
	ctx.Logger().Info("Reserve balance validation passed",
		"reserve_balance", reserveBalance.String(),
		"required_amount", totalRefundAmount.String())

	for _, refund := range mainnetSlashRefunds14to15 {
		recipient, err := cosmos.AccAddressFromBech32(refund.address)
		if err != nil {
			ctx.Logger().Error("error parsing address in store migration",
				"error", err,
				"address", refund.address)
			continue
		}
		amount := cosmos.NewUint(refund.amount)
		refundCoins := common.NewCoins(common.NewCoin(common.RuneAsset(), amount))
		if err := m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, recipient, refundCoins); err != nil {
			ctx.Logger().Error("fail to store migration transfer RUNE from Reserve to recipient",
				"error", err,
				"recipient", recipient.String(),
				"address", refund.address,
				"amount", amount.String())
		} else {
			ctx.Logger().Debug("successfully transferred bond slash refund",
				"recipient", recipient.String(),
				"amount", amount.String())
		}
	}

	// Step 4: reconcile phantom credit on retiring asgard ETH vaults from
	// SafeSub clamping in vault.SubFunds during outbound observation.
	// Failures inside this routine are logged and skipped per-row; we do
	// not return an error from this step so the migration as a whole still
	// completes the prior 3 steps.
	fixVaultClamps_v3_18_0(ctx, m.mgr, mainnetVaultClampFixes14to15)

	return nil
}

// adr028PoolHaircut describes the pool-level depth adjustments applied by the
// ADR-028 conciliation waterfall (steps 1–4).  All amounts are in 1e8 units.
// Fields are populated from on-chain state snapshotted at upgrade height;
// zero values mean the step does not apply to that pool.
type adr028PoolHaircut struct {
	asset common.Asset
	// step1RuneHaircut: ΔR — reduce pool.BalanceRune by this amount and burn it
	// from Asgard. pool.BalanceAsset is reduced proportionally:
	// ΔA = ΔR × BalanceAsset / BalanceRune (symmetric AMM identity).
	// LP units at the reserve address are NOT mutated; polPoolValue() reads
	// pool.BalanceRune live so the depth reduction propagates automatically.
	// After all step-1 pool writes, RUNEPool.ReserveUnits is re-solved to keep
	// providers whole (see Migrate15to16 for the formula).
	step1RuneHaircut uint64
	// step2ReserveSynthBurn: synth amount (in the pool's synth denom) to burn
	// directly from the reserve module bank. No pool balance change — the freed
	// backing shifts from synth-claim to LP-claim on the next CalcUnits recompute.
	step2ReserveSynthBurn uint64
	// step3SaversHaircutBps: savers haircut in basis points applied to the synth
	// vault pool (asset = synthAsset). Reduces every saver LP record pro-rata,
	// reduces the synth pool LPUnits and BalanceAsset, and burns the corresponding
	// synth supply from Asgard.
	step3SaversHaircutBps uint64
	// step4TreasuryDraw: ΔA — asset amount to draw from the treasury LP position.
	// Reduces treasury LP units, pool.LPUnits, pool.BalanceAsset by ΔA, and
	// pool.BalanceRune / Asgard RUNE by ΔR = ΔA × BalanceRune / BalanceAsset.
	// Non-zero only for zero-saver assets where treasury holds LP coverage.
	step4TreasuryDraw uint64
}

// adr028SolveOverflowBps computes the strict-waterfall savers-overflow haircut
// (in bps on the savers-pool BalanceAsset) needed to retire `residualUnits` of
// total-pool units that the reserve-capped layer-1 POL removal could not absorb.
//
// It mirrors the dry-run computeCalibration regime split:
//
//	overhang (2·postA ≤ S): CalcUnits clamps, SU = postL·S is LINEAR in supply, so
//	  burning supply by burn_perc = ΔR/preR retires exactly burn_perc of the units
//	  → overflow_bps = ΔR·10000/preR (this is the BTC=1907 known-good shortcut).
//	non-overhang (2·postA > S): SU = postL·S/(2·postA−S). Solve the post-removal
//	  synth units target (SU_now − residualUnits), invert CalcUnits at (postL, postA)
//	  for the supply S_target = 2·postA·SU_target/(postL+SU_target), and burn
//	  b = S − S_target → overflow_bps = b·10000/saverPool.BalanceAsset.
//
// postL = L − x (LP units after layer-1 removal); postA = A after the ΔA depth cut.
// Returns 0 if there is no savers pool (TRON.TRX, XRP.XRP) — the residual then
// unavoidably falls on DLPs; the dry-run flags it (G9).
func (m Migrator) adr028SolveOverflowBps(
	ctx sdk.Context,
	asset common.Asset,
	postL, postA, synthSupply, targetT cosmos.Uint,
) uint64 {
	synthAsset := asset.GetSyntheticAsset()
	saverPool, err := m.mgr.Keeper().GetPool(ctx, synthAsset)
	if err != nil || saverPool.BalanceAsset.IsZero() {
		// No savers pool / zero depth → no overflow lever (TRX/XRP).
		return 0
	}
	// Pure arithmetic extracted to adr028OverflowBps (migration_math_15to16.go) so
	// its regime switch and clamp are CI-unit-testable; see that file for the
	// CalcUnits-inverse derivation.
	return adr028OverflowBps(postL, postA, synthSupply, targetT, saverPool.BalanceAsset)
}

// splitFreedRune disposes of RUNE freed by a pool-depth reduction. Instead of
// burning the whole amount, it burns HALF and retains the other HALF in the
// Reserve module. Either way the full `freed` amount leaves Asgard (so pool/vault
// RUNE accounting is consistent); only its destination differs — half is destroyed
// (Asgard→thorchain→burn, thorchain being the only Burner-permissioned module) and
// half is credited to the Reserve (Asgard→Reserve). The odd 1e-8 unit, if any, goes
// to the burn. Emits a mint/burn event for the burned half.
func (m Migrator) splitFreedRune(ctx sdk.Context, freed cosmos.Uint, reason string) error {
	if freed.IsZero() {
		return nil
	}
	toBurn, toReserve := adr028SplitFreed(freed) // pure split; odd 1e-8 unit → burn
	if !toBurn.IsZero() {
		bc := common.NewCoin(common.RuneNative, toBurn)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ModuleName, common.NewCoins(bc)); err != nil {
			return fmt.Errorf("move burn-half Asgard→thorchain: %w", err)
		}
		if err := m.mgr.Keeper().BurnFromModule(ctx, ModuleName, bc); err != nil {
			return fmt.Errorf("burn freed RUNE: %w", err)
		}
		burnEvt := NewEventMintBurn(BurnSupplyType, bc.Asset.Native(), bc.Amount, reason)
		if err := m.mgr.EventMgr().EmitEvent(ctx, burnEvt); err != nil {
			ctx.Logger().Error("conciliation: freed-rune: emit burn event", "error", err, "reason", reason)
		}
	}
	if !toReserve.IsZero() {
		rc := common.NewCoin(common.RuneNative, toReserve)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ReserveName, common.NewCoins(rc)); err != nil {
			return fmt.Errorf("move reserve-half Asgard→Reserve: %w", err)
		}
	}
	ctx.Logger().Info("conciliation: freed RUNE split (burn half, reserve half)",
		"reason", reason, "freed", freed, "burned", toBurn, "to_reserve", toReserve)
	return nil
}

// vault2v28PubKey, adr028TreasuryAddr, stuckStreamingTxStr and the hex-qualified
// adr028Asset* constants are defined in migration_data_15to16.go (build-tag-agnostic
// so the mocknet data-validation test can reach them).

// Migrate15to16 implements ADR-028 conciliation.
// Waterfall order (ADR-028 §3, re-run amounts at upgrade height):
//
//	— zero vault 2v28 keeper balances (log only, no event).
//	— POL haircut: reduce pool.BalanceRune/Asset, burn RUNE from Asgard.
//	— reserve synth burn: burn reserve module's synth bank balances.
//	— savers haircut: reduce saver LP units + synth pool depth pro-rata,
//	  burn synths from Asgard.
//	— treasury LP draw: cover residual for zero-saver pools with treasury LP.
//	— POL provider-whole solve: after all of the above pool writes, solve
//	  RUNEPool.ReserveUnits so the reserve bucket absorbs the full POL loss.
//	— stuck streaming swap: stop the 2v28 ETH.DAI→ETH.USDT streaming swap, pay
//	  the already-produced ETH.USDT (StreamingSwap.Out) as a best-effort outbound,
//	  and log the remainder of the guaranteed minimum the treasury refunds
//	  off-chain (no on-chain fund movement for it).
//
// Snapshot date: 2026-05-26 (re-run before release at upgrade height).
// POL drain = ΔR (RUNE to burn from Asgard; ΔA = ΔR×BalanceAsset/BalanceRune).
// Only the reserve-bucket fraction (55.76%) of LP value is consumed;
// providers are held whole via RUNEPool.ReserveUnits solve after all pool writes.
func (m Migrator) Migrate15to16(ctx sdk.Context) error {
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	// ── Step 0: zero vault 2v28 keeper record ───────────────────────────────
	vault, err := m.mgr.Keeper().GetVault(ctx, vault2v28PubKey)
	if err != nil {
		return fmt.Errorf("conciliation: vault 2v28: fail to get vault 2v28: %w", err)
	}
	originalCoins := vault.Coins
	vault.Coins = common.Coins{}
	if err := m.mgr.Keeper().SetVault(ctx, vault); err != nil {
		return fmt.Errorf("conciliation: vault 2v28: fail to zero vault 2v28: %w", err)
	}
	ctx.Logger().Info("conciliation: vault 2v28: zeroed vault 2v28 keeper record",
		"pubkey", vault2v28PubKey,
		"cleared_coins", originalCoins.String())

	// ── Build per-pool haircut table (assets requiring NewAsset parsing) ─────
	ethUSDT, err := common.NewAsset(adr028AssetETHUSDT)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse ETH.USDT: %w", err)
	}
	avaxUSDC, err := common.NewAsset(adr028AssetAVAXUSDC)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse AVAX.USDC: %w", err)
	}
	baseUSDC, err := common.NewAsset(adr028AssetBASEUSDC)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse BASE.USDC: %w", err)
	}
	bscBUSD, err := common.NewAsset(adr028AssetBSCBUSD)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse BSC.BUSD: %w", err)
	}
	bscETH, err := common.NewAsset(adr028AssetBSCETH)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse BSC.ETH: %w", err)
	}
	bscBTCB, err := common.NewAsset(adr028AssetBSCBTCB)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse BSC.BTCB: %w", err)
	}
	ethUSDC, err := common.NewAsset(adr028AssetETHUSDC)
	if err != nil {
		return fmt.Errorf("adr028: failed to parse ETH.USDC: %w", err)
	}

	// Per-pool waterfall table — 2026-05-26 live-chain snapshot; re-run at upgrade height.
	// Step1 = ΔR (raw 1e8 RUNE to burn from Asgard); ΔA derived at runtime.
	// Step2 = synth units in 1e8 to burn from reserve bank (no pool mutation).
	// Step3 = savers haircut bps applied to the synth vault pool.
	// Step4 = ΔA (raw 1e8 asset) drawn from treasury LP; only assets w/ treasury coverage.
	// Zero values mean the step does not apply to that pool.
	haircuts := []adr028PoolHaircut{
		// DOGE: fully covered by POL haircut (72,302 DOGE) + reserve synth burn (3,392,456 DOGE synths).
		{asset: common.DOGEAsset, step1RuneHaircut: 1_419_918_684_958, step2ReserveSynthBurn: 339_245_565_058_092},
		// BCH: fully covered by POL haircut (174 BCH).
		{asset: common.BCHAsset, step1RuneHaircut: 13_013_460_708_269},
		// BSC.BNB: fully covered by reserve synth burn (45.74 native BNB synths; reserve holds ~143 BNB).
		{asset: common.BNBBEP20Asset, step2ReserveSynthBurn: 4_574_000_000},
		// ETH.USDT: POL haircut (198,410 USDT) + reserve synth burn (27,022 USDT).
		// Savers overflow is RUNTIME-DERIVED by the strict waterfall (step1OverflowBps),
		// no longer a tabled bps — it equals the residual the reserve-capped POL-unit
		// removal could not absorb.
		{asset: ethUSDT, step1RuneHaircut: 33_880_349_267_266, step2ReserveSynthBurn: 2_702_221_779_137},
		// AVAX.USDC: synth units inflated, near-zero LP share → full 2713 bps savers haircut.
		{asset: avaxUSDC, step3SaversHaircutBps: 2713},
		// ETH.ETH: POL haircut (1,086 ETH = ~$3.26M) closes the bifrost-observed
		// absorbed loss from the vault 2v28 compromise (37.5% pool haircut, the
		// largest single per-pool fraction in this migration; POL reserve
		// absorbs the corresponding LP value loss via step-5 inflation).
		// Savers overflow is RUNTIME-DERIVED by the strict waterfall — for ETH's
		// synth-overhang regime it lands at ≈ ΔR/R·10000 bps (clamp linearity),
		// generalizing the BTC known-good result; no tabled bps.
		{asset: common.ETHAsset, step1RuneHaircut: 420_098_359_472_574},
		// GAIA.ATOM: no POL or synth coverage → 3121 bps savers haircut.
		{asset: common.ATOMAsset, step3SaversHaircutBps: 3121},
		// Zero-saver pools with confirmed treasury LP coverage:
		{asset: baseUSDC, step4TreasuryDraw: 4_904_100_000_000},
		{asset: bscBUSD, step4TreasuryDraw: 3_214_300_000_000},
		{asset: bscETH, step4TreasuryDraw: 1_600_000_000},
		{asset: bscBTCB, step4TreasuryDraw: 51_000_000},
		// ─── Pool-haircut entries closing additional vault 2v28 L1 backing
		// drops surfaced by the snapshot dry-run. Each ΔR is sub-1% of pool
		// RUNE; absorbed by POL via step-5 RUNEPool.ReserveUnits re-solve.
		// ETH.USDC: closes 27,179 USDC L1 gap (0.8% pool haircut).
		{asset: ethUSDC, step1RuneHaircut: 4_640_667_267_256},
		// TRON.TRX: closes 2,874 TRX L1 gap via TREASURY draw (not POL). The TRON
		// pool is ~99.7% treasury1-owned and has 0 POL units + 0 savers, so a step-1
		// POL haircut landed the whole loss on ordinary LPs (-88.41 bps DLP v/u
		// drift, flagged by the dry-run). Drawing ΔA from treasury1's LP position
		// (~1,228 of its 138,508 units) makes the treasury bear it and keeps the
		// ~0.3% ordinary LPs whole. ΔA = 287_392_444_998 (2,873.92444998 TRX).
		{asset: common.TRXAsset, step4TreasuryDraw: 287_392_444_998},
		// XRP.XRP: closes 19 XRP L1 gap (<0.01% pool haircut).
		{asset: common.XRPAsset, step1RuneHaircut: 4_831_551_272},
		// ─── Full attack-recapitalization entries for bifrost-OBSERVED native
		// gas-asset drains from vault 2v28. These losses were already absorbed
		// economically at attack time (bifrost decremented vault.Coins as the
		// attacker-initiated L1 outbounds were observed), but pool.BalanceAsset
		// did NOT follow — leaving a pool/vault aggregate gap. Step-1 here
		// closes that gap by reducing pool depth to match actual vault holdings;
		// POL absorbs the LP value loss via step-5 ReserveUnits inflation.
		// ΔR sized to match stolen_amount × poolRune/poolAsset at snapshot height
		// 26,355,121; §3e re-verify these against fresh state at upgrade height.
		// BTC.BTC: closes 40.73 BTC absorbed loss (~$4.07M), 19.0% pool haircut.
		// Savers overflow is RUNTIME-DERIVED by the strict waterfall. In BTC's
		// synth-overhang regime CalcUnits is linear (SynthUnits = LPUnits·S), so
		// the reserve_pool_capacity is ≈0 and the overflow lands at the SAME
		// fraction as the L1 depth cut: ΔR/R·10000 ≈ 1907 bps (the prior tabled
		// value). Burning the synth pool by ΔR/R drives LP value-per-unit drift
		// to ~0: savers bear their proportional share via fewer (whole) synths,
		// DLP & POL kept whole.
		{asset: common.BTCAsset, step1RuneHaircut: 568_945_622_679_283},
		// LTC.LTC: closes 6,866.75 LTC absorbed loss (~$686k), 41.5% pool haircut.
		// Tied for largest per-pool fraction with ETH.ETH; significant L1 pool
		// depth reduction expected.
		{asset: common.LTCAsset, step1RuneHaircut: 69_426_603_965_037},
		// AVAX.AVAX: closes 861 AVAX absorbed loss (~$26k), 1.1% pool haircut.
		{asset: common.AVAXAsset, step1RuneHaircut: 1_444_896_137_532},
	}

	// ── Step 1: POL haircut (per pool) ──────────────────────────────────────
	// Snapshot provider value BEFORE any pool write.
	runePool, err := m.mgr.Keeper().GetRUNEPool(ctx)
	if err != nil {
		return fmt.Errorf("conciliation: pol haircut: fail to get RUNEPool: %w", err)
	}
	vPre, err := runePoolValue(ctx, m.mgr)
	if err != nil {
		return fmt.Errorf("conciliation: pol haircut: fail to compute runePoolValue pre: %w", err)
	}
	providerValuePre := common.GetSafeShare(runePool.PoolUnits, runePool.TotalUnits(), vPre)

	// POL holds LP units in pools via the Reserve module's bech32 address.
	// We need polAddress to compute POL's fractional share of each step-1
	// haircut for accurate pol.RuneWithdrawn accounting (see below).
	polAddress, err := m.mgr.Keeper().GetModuleAddress(ReserveName)
	if err != nil {
		return fmt.Errorf("conciliation: pol haircut: fail to get reserve module address: %w", err)
	}

	totalStep1Rune := cosmos.ZeroUint()        // sum of ΔR burned from Asgard (for logging/conservation)
	totalPOLRuneWithdrawn := cosmos.ZeroUint() // sum of POL's fractional share of ΔR per pool (for pol.RuneWithdrawn)
	totalPOLUnitsRemoved := cosmos.ZeroUint()  // sum of POL pool LP units removed (layer 1, for logging)
	// dlpTargetR/dlpTargetT: per step-1 pool, the PRE-migration BalanceRune and total
	// pool units. The savers overflow is solved LAST (step 3) against the realized
	// post-everything pool state to land total units at the DLP-neutral target
	// Tfinal = R_current · T_orig / R_orig — absorbing the exact residual regardless
	// of how step 1 (depth cut), step 2 (reserve burn + self-neutral paired cut) and
	// the layer-1 POL removal composed. Reserve-first ordering: layer 1 (POL) absorbs
	// up to its reserve cap, the step-2 reserve synth burn absorbs its share, and
	// savers cover only what remains.
	dlpTargetR := map[string]cosmos.Uint{}
	dlpTargetT := map[string]cosmos.Uint{}
	for _, h := range haircuts {
		if h.step1RuneHaircut == 0 {
			continue
		}
		pool, err := m.mgr.Keeper().GetPool(ctx, h.asset)
		if err != nil {
			return fmt.Errorf("conciliation: pol haircut: fail to get pool %s: %w", h.asset, err)
		}
		deltaR := cosmos.NewUint(h.step1RuneHaircut)
		if pool.BalanceRune.LT(deltaR) {
			return fmt.Errorf("conciliation: pol haircut: pool %s BalanceRune %s < haircut %d — abort",
				h.asset, pool.BalanceRune, h.step1RuneHaircut)
		}

		// Snapshot POL's fractional share BEFORE applying the haircut so
		// pol.RuneWithdrawn below records only POL's actual loss (not the full
		// ΔR burn). Critical for pools where POL is not the sole/majority LP
		// — for those, crediting the full ΔR would over-count POL's withdrawal
		// and could push pol.CurrentDeposit() negative, breaking the
		// maxPOLDeposit safety gate in manager_network_current.go.
		//
		// Compute CalcUnits in-memory on a local copy so the share denominator
		// (LPUnits + SynthUnits) reflects the current synth supply rather than
		// any stored-stale SynthUnits — critical for pools that are in the
		// degenerate synth-overhang regime (supply > 2×BalanceAsset), where
		// stored SynthUnits is correctly-but-astronomically inflated and POL's
		// share-of-pool-value is effectively zero.
		//
		// preR/preA/T/L are the PRE-haircut depth, total units and LP units used
		// by the strict-waterfall layer-1 math below.
		poolPreHaircut := pool
		synthSupply := m.mgr.Keeper().GetTotalSupply(ctx, h.asset.GetSyntheticAsset())
		poolPreHaircut.CalcUnits(synthSupply)
		preR := poolPreHaircut.BalanceRune
		T := poolPreHaircut.GetPoolUnits() // L + SU (pre-state)
		L := poolPreHaircut.LPUnits
		// Record pre-migration (R, T) so step 3 can solve the savers overflow against
		// the realized post-everything state to land DLP value-per-unit at neutral.
		dlpTargetR[h.asset.String()] = preR
		dlpTargetT[h.asset.String()] = T
		polLP, lpErr := m.mgr.Keeper().GetLiquidityProvider(ctx, h.asset, common.Address(polAddress.String()))

		// ── STRICT WATERFALL — Layer 1: remove POL's pool LP units ───────────
		// To keep DLP value-per-unit neutral after a burn_perc = ΔR/R depth cut,
		// total pool units must shrink by burn_perc. The IDEAL POL-only removal
		// is x* = burn_perc·L (= ΔR·L/preR): removing the same FRACTION of LP
		// units as the depth cut keeps (L−x)/L = 1−burn_perc, and since SU
		// scales with LPUnits in BOTH CalcUnits regimes (non-clamped: SU∝L;
		// clamped overhang: SU = L·S ∝ L), the post total units land at
		// (1−burn_perc)·T — DLP v/u neutral.
		//
		// CEILING: we may remove at most the POL units economically backed by
		// the RESERVE (vs RUNEPool providers): reserve_pool_capacity =
		// polL · ReserveUnits / TotalUnits. Removing more drives vPost below
		// providerValuePre, hitting step-5's wipe branch and forcing loss onto
		// providers — forbidden. x = min(x*, reserveCap, polL).
		xStar := cosmos.ZeroUint()
		if !preR.IsZero() {
			xStar = common.GetUncappedShare(deltaR, preR, L) // ΔR·L/preR
		}
		reserveCap := cosmos.ZeroUint()
		if lpErr == nil && !runePool.TotalUnits().IsZero() {
			reserveCap = common.GetSafeShare(runePool.ReserveUnits, runePool.TotalUnits(), polLP.Units)
		}
		x := xStar
		if x.GT(reserveCap) {
			x = reserveCap
		}
		if lpErr == nil && x.GT(polLP.Units) {
			x = polLP.Units
		}
		if lpErr != nil {
			x = cosmos.ZeroUint() // no POL LP record to remove from
		}
		if !x.IsZero() && lpErr == nil {
			polLP.Units = common.SafeSub(polLP.Units, x)
			m.mgr.Keeper().SetLiquidityProvider(ctx, polLP)
			pool.LPUnits = common.SafeSub(pool.LPUnits, x)
			totalPOLUnitsRemoved = totalPOLUnitsRemoved.Add(x)
		}

		// pol.RuneWithdrawn accounting: credit POL's RUNE-value of the units it
		// ACTUALLY lost — the layer-1 removal (x units) plus the depth-cut value
		// on POL's remaining units. We use the value of the removed units,
		// 2·GetSafeShare(x, T, preR), which is POL's realized loss from layer 1.
		// This keeps pol.CurrentDeposit() accurate without over-counting (the
		// G4b invariant — RuneDeposited ≥ RuneWithdrawn — is dry-run asserted).
		if lpErr == nil && !T.IsZero() && !x.IsZero() {
			polDeltaR := common.GetSafeShare(x, T, preR).MulUint64(2)
			totalPOLRuneWithdrawn = totalPOLRuneWithdrawn.Add(polDeltaR)
		} else if lpErr == nil && !poolPreHaircut.GetPoolUnits().IsZero() && !polLP.Units.IsZero() {
			// Fallback (no layer-1 removal happened, e.g. reserveCap=0): record
			// POL's fractional share of the depth-cut ΔR as before.
			polDeltaR := common.GetSafeShare(polLP.Units, poolPreHaircut.GetPoolUnits(), deltaR)
			totalPOLRuneWithdrawn = totalPOLRuneWithdrawn.Add(polDeltaR)
		}
		// If POL has no LP in this pool (lpErr) or pool is degenerate (synth
		// dominates → POL share ≈ 0), totalPOLRuneWithdrawn gets no contribution
		// for this pool. The full ΔR is still burned from Asgard below — only
		// POL's accounting is fractional.

		// Savers overflow is NOT computed here — it is solved LAST in step 3 against
		// the realized pool state (dlpTargetR/T), so it absorbs the exact residual
		// after this depth cut, the step-2 reserve burn+pairing, and layer 1. Layer 1
		// (reserve-first POL removal) is applied above; savers cover only the rest.
		ctx.Logger().Info("conciliation: pol haircut: strict-waterfall layer 1",
			"asset", h.asset, "x_star", xStar, "reserve_cap", reserveCap, "pol_units_removed", x)

		// ΔA = ΔR × BalanceAsset / BalanceRune (price-ratio preservation).
		deltaA := common.GetSafeShare(deltaR, pool.BalanceRune, pool.BalanceAsset)
		pool.BalanceRune = common.SafeSub(pool.BalanceRune, deltaR)
		pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, deltaA)
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("conciliation: pol haircut: fail to save pool %s: %w", h.asset, err)
		}
		// Freed RUNE: burn HALF, retain HALF in the Reserve (was: burn all).
		if err := m.splitFreedRune(ctx, deltaR, "conciliation_pol_haircut"); err != nil {
			return fmt.Errorf("conciliation: pol haircut: fail to dispose freed RUNE for %s: %w", h.asset, err)
		}
		mod := NewPoolMod(h.asset, deltaR, false, deltaA, false)
		emitPoolBalanceChangedEvent(ctx, mod, "conciliation_pol_haircut", m.mgr)
		totalStep1Rune = totalStep1Rune.Add(deltaR)
		ctx.Logger().Info("conciliation: pol haircut: POL haircut applied",
			"asset", h.asset, "delta_rune", deltaR, "delta_asset", deltaA)
	}

	// Track POL withdrawal in the POL record so CurrentDeposit() stays accurate.
	// IMPORTANT: only POL's fractional share of each pool's ΔR is recorded as a
	// POL withdrawal — NOT the full ΔR sum. Recording the full ΔR would over-
	// state POL's loss (treating it as if POL owned 100% of every haircut pool)
	// and could push pol.RuneWithdrawn past pol.RuneDeposited → negative
	// CurrentDeposit → broken maxPOLDeposit safety gate.
	if !totalPOLRuneWithdrawn.IsZero() {
		pol, err := m.mgr.Keeper().GetPOL(ctx)
		if err != nil {
			return fmt.Errorf("conciliation: pol haircut: fail to get POL: %w", err)
		}
		pol.RuneWithdrawn = pol.RuneWithdrawn.Add(totalPOLRuneWithdrawn)
		if err := m.mgr.Keeper().SetPOL(ctx, pol); err != nil {
			return fmt.Errorf("conciliation: pol haircut: fail to save POL: %w", err)
		}
		ctx.Logger().Info("conciliation: pol haircut: POL.RuneWithdrawn adjusted",
			"total_step1_rune_burned", totalStep1Rune,
			"pol_fractional_withdrawn", totalPOLRuneWithdrawn,
			"non_pol_share", common.SafeSub(totalStep1Rune, totalPOLRuneWithdrawn),
			"total_pol_units_removed", totalPOLUnitsRemoved)
	}

	// ── Step 2: reserve synth burn ───────────────────────────────────────────
	// Pure bank burn — no pool balance mutation.  FreedSynthUnits shifts from
	// synth-claim to LP-claim on the next CalcUnits recompute.
	//
	// The Reserve module account has no Burner permission (see app.maccPerms),
	// so the synth is first sent to thorchain.ModuleName (the only module with
	// Minter+Burner) and burned from there — the standard cosmos-sdk pattern.
	//
	// Cap burnAmt to what Reserve actually holds rather than aborting, mirroring
	// the step-3 idiom: snapshot drift may leave the Reserve synth balance below
	// the planned amount, but a partial burn is still strictly better than
	// halting the chain mid-upgrade. The shortfall is logged for off-chain
	// reconciliation.
	for _, h := range haircuts {
		if h.step2ReserveSynthBurn == 0 {
			continue
		}
		synthAsset := h.asset.GetSyntheticAsset()
		burnAmt := cosmos.NewUint(h.step2ReserveSynthBurn)
		reserveSynthBal := m.mgr.Keeper().GetBalanceOfModule(ctx, ReserveName, synthAsset.Native())
		if reserveSynthBal.LT(burnAmt) {
			ctx.Logger().Error("conciliation: reserve synth burn: Reserve synth balance below burnAmt, capping — shortfall left",
				"asset", synthAsset,
				"reserve_balance", reserveSynthBal,
				"burn_amt", burnAmt,
				"shortfall", common.SafeSub(burnAmt, reserveSynthBal))
			burnAmt = reserveSynthBal
		}
		if burnAmt.IsZero() {
			ctx.Logger().Info("conciliation: reserve synth burn: nothing to burn, skipping",
				"asset", synthAsset)
			continue
		}
		synthCoin := common.NewCoin(synthAsset, burnAmt)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, ReserveName, ModuleName, common.NewCoins(synthCoin)); err != nil {
			return fmt.Errorf("conciliation: reserve synth burn: fail to send %s reserve→thorchain: %w", synthAsset, err)
		}
		if err := m.mgr.Keeper().BurnFromModule(ctx, ModuleName, synthCoin); err != nil {
			return fmt.Errorf("conciliation: reserve synth burn: fail to burn %s from thorchain: %w", synthAsset, err)
		}
		burnEvt := NewEventMintBurn(BurnSupplyType, synthCoin.Asset.Native(), synthCoin.Amount, "conciliation_reserve_synth_burn")
		if err := m.mgr.EventMgr().EmitEvent(ctx, burnEvt); err != nil {
			ctx.Logger().Error("conciliation: reserve synth burn: fail to emit burn event", "error", err, "asset", synthAsset)
		}
		ctx.Logger().Info("conciliation: reserve synth burn: burned reserve synths",
			"asset", synthAsset, "amount", burnAmt)

		// ── Self-neutral pairing (Option 2): retire the pool VALUE the burned reserve
		// synths claimed, so DLP/saver value-per-unit is UNCHANGED. A bare burn drops
		// SynthUnits and sprays that freed v/u onto everyone else; pairing it with a
		// depth cut keeps the reserve's absorption position-neutral. Closed-form
		// depth-keep fraction r = R'/R solving L(S−X)/(2A·r−(S−X)) = rT − L exactly
		// (this accounts for A's own re-inflation of SU, which the naive R·ΔSU/T missed):
		//   non-overhang (2A > S): r = (T(S−X) + 2AL) / (2AT)
		//   overhang     (2A ≤ S): r = (S−X) / S      (CalcUnits clamp → SU = L·S)
		// Cut R→rR, A→rA and burn the freed (1−r)·R RUNE; the asset-claim drop also
		// tightens L1 solvency.
		pool, perr := m.mgr.Keeper().GetPool(ctx, h.asset)
		if perr != nil {
			return fmt.Errorf("conciliation: reserve synth burn: fail to get pool %s: %w", h.asset, perr)
		}
		sPre := m.mgr.Keeper().GetTotalSupply(ctx, synthAsset).Add(burnAmt) // supply BEFORE the burn
		if !pool.BalanceRune.IsZero() && !pool.BalanceAsset.IsZero() && !sPre.IsZero() {
			pCopy := pool
			pCopy.CalcUnits(sPre)
			T := pCopy.GetPoolUnits() // L + SU(pre-burn)
			// Pure regime-branching arithmetic extracted to adr028PairedCut
			// (migration_math_15to16.go) so the overhang/non-overhang boundary is
			// CI-unit-testable.
			dRpair, dApair := adr028PairedCut(pool.BalanceRune, pool.BalanceAsset, pool.LPUnits, T, sPre, burnAmt)
			if !dRpair.IsZero() {
				pool.BalanceRune = common.SafeSub(pool.BalanceRune, dRpair)
				pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, dApair)
				if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
					return fmt.Errorf("conciliation: reserve synth burn: fail to save paired cut %s: %w", h.asset, err)
				}
				// Freed RUNE from the paired cut: burn HALF, retain HALF in the Reserve.
				if err := m.splitFreedRune(ctx, dRpair, "conciliation_reserve_paired_cut"); err != nil {
					return fmt.Errorf("conciliation: reserve synth burn: fail to dispose paired RUNE %s: %w", h.asset, err)
				}
				ctx.Logger().Info("conciliation: reserve synth burn: self-neutral paired depth cut",
					"asset", h.asset, "rune_cut", dRpair, "asset_cut", dApair)
			}
		}
	}

	// ── Step 3: savers haircut ───────────────────────────────────────────────
	// Operates on the synth vault pool (asset = synthAsset, one-sided LP with
	// BalanceAsset only).  Reduces every saver LP unit pro-rata, reduces pool
	// LPUnits and BalanceAsset, then burns the freed synth supply from Asgard.
	for _, h := range haircuts {
		synthAsset := h.asset.GetSyntheticAsset()
		// Strict-waterfall OVERFLOW-LAST: for a step-1 pool, solve the savers haircut
		// NOW (against the realized post-step-1/step-2/layer-1 pool state) so total
		// pool units land at the DLP-neutral target Tfinal = R_current·T_orig/R_orig.
		// This OVERRIDES any tabled step3SaversHaircutBps and absorbs the exact
		// residual the reserve-first layers (POL + reserve burn) could not. Pure-savers
		// pools (no step-1, e.g. AVAX.USDC, GAIA.ATOM) keep their tabled bps.
		bps := h.step3SaversHaircutBps
		if rOrig, ok := dlpTargetR[h.asset.String()]; ok && !rOrig.IsZero() {
			// A GetPool failure here must abort, not silently fall through: for every
			// step-1 pool the tabled bps is 0, so swallowing the error would skip the
			// savers haircut entirely and under-allocate that pool's loss — an
			// undetectable error in a one-shot consensus migration. (Matches the
			// hard-error handling on the saver-pool GetPool just below.)
			cur, cerr := m.mgr.Keeper().GetPool(ctx, h.asset)
			if cerr != nil {
				return fmt.Errorf("conciliation: savers haircut: fail to re-read pool %s for overflow solve: %w", h.asset, cerr)
			}
			sCur := m.mgr.Keeper().GetTotalSupply(ctx, synthAsset)
			tFinal := common.GetUncappedShare(cur.BalanceRune, rOrig, dlpTargetT[h.asset.String()])
			bps = m.adr028SolveOverflowBps(ctx, h.asset, cur.LPUnits, cur.BalanceAsset, sCur, tFinal)
		}
		if bps == 0 {
			continue
		}
		saverPool, err := m.mgr.Keeper().GetPool(ctx, synthAsset)
		if err != nil {
			return fmt.Errorf("conciliation: savers haircut: fail to get saver pool %s: %w", synthAsset, err)
		}
		// burnAmt = pool.BalanceAsset × bps / 10000
		burnAmt := saverPool.BalanceAsset.MulUint64(bps).QuoUint64(10000)
		if burnAmt.IsZero() {
			ctx.Logger().Error("conciliation: savers haircut: zero burnAmt, skipping", "asset", synthAsset)
			continue
		}
		// Cap burnAmt to what Asgard actually holds rather than aborting; log any shortfall.
		asgardSynthBal := m.mgr.Keeper().GetBalanceOfModule(ctx, AsgardName, synthAsset.Native())
		if asgardSynthBal.LT(burnAmt) {
			ctx.Logger().Error("conciliation: savers haircut: Asgard synth balance below burnAmt, capping — shortfall left",
				"asset", synthAsset,
				"asgard_balance", asgardSynthBal,
				"burn_amt", burnAmt,
				"shortfall", common.SafeSub(burnAmt, asgardSynthBal))
			burnAmt = asgardSynthBal
		}
		// Collect all saver LP records before modifying (safe iteration pattern).
		// Drain inside a closure so the iterator is deferred-closed per pool — and
		// on a MustUnmarshal panic — without leaking a defer across the haircuts loop.
		saverLPs := func() []LiquidityProvider {
			iter := m.mgr.Keeper().GetLiquidityProviderIterator(ctx, synthAsset)
			defer iter.Close()
			var lps []LiquidityProvider
			for ; iter.Valid(); iter.Next() {
				var lp LiquidityProvider
				m.mgr.Keeper().Cdc().MustUnmarshal(iter.Value(), &lp)
				lps = append(lps, lp)
			}
			return lps
		}()
		// Reduce each saver's LP units in proportion to the synth actually burned
		// (deduct = lp.Units × burnAmt / BalanceAsset, on the pre-haircut depth),
		// accumulating the exact per-LP deductions to use as the pool-level
		// deduction. Deriving the unit reduction from burnAmt — rather than from
		// bps directly — keeps it coupled to the asset reduction even when burnAmt
		// was capped to the Asgard synth balance above; otherwise LPUnits would
		// shrink by the full bps while BalanceAsset shrank by less, inflating the
		// savers' value-per-unit. In the uncapped path burnAmt = ⌊BalanceAsset ×
		// bps/10000⌋, so this matches the bps haircut. Accumulating per-LP
		// truncations (not a pool-level ⌊⌋) keeps saverPool.LPUnits == Σ lp.Units
		// exact.
		poolAssetPre := saverPool.BalanceAsset
		sumLPDeductions := cosmos.ZeroUint()
		for _, lp := range saverLPs {
			deduct := lp.Units.Mul(burnAmt).Quo(poolAssetPre)
			lp.Units = common.SafeSub(lp.Units, deduct)
			m.mgr.Keeper().SetLiquidityProvider(ctx, lp)
			sumLPDeductions = sumLPDeductions.Add(deduct)
		}
		// Reduce synth pool depth using the accumulated LP deduction so that
		// saverPool.LPUnits == Σ lp.Units is maintained exactly.
		saverPool.LPUnits = common.SafeSub(saverPool.LPUnits, sumLPDeductions)
		saverPool.BalanceAsset = common.SafeSub(saverPool.BalanceAsset, burnAmt)
		if err := m.mgr.Keeper().SetPool(ctx, saverPool); err != nil {
			return fmt.Errorf("conciliation: savers haircut: fail to save saver pool %s: %w", synthAsset, err)
		}
		// Burn the freed synth supply from Asgard (bank pairing:
		// Asgard→thorchain→burn — same idiom as step 1's RUNE burn, since
		// AsgardName has no Burner permission in app.maccPerms).
		synthCoin := common.NewCoin(synthAsset, burnAmt)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ModuleName, common.NewCoins(synthCoin)); err != nil {
			return fmt.Errorf("conciliation: savers haircut: fail to move synths from Asgard for %s: %w", synthAsset, err)
		}
		if err := m.mgr.Keeper().BurnFromModule(ctx, ModuleName, synthCoin); err != nil {
			return fmt.Errorf("conciliation: savers haircut: fail to burn synths for %s: %w", synthAsset, err)
		}
		mod := NewPoolMod(synthAsset, cosmos.ZeroUint(), false, burnAmt, false)
		emitPoolBalanceChangedEvent(ctx, mod, "conciliation_saver_haircut", m.mgr)
		burnEvt := NewEventMintBurn(BurnSupplyType, synthCoin.Asset.Native(), synthCoin.Amount, "conciliation_saver_haircut")
		if err := m.mgr.EventMgr().EmitEvent(ctx, burnEvt); err != nil {
			ctx.Logger().Error("conciliation: savers haircut: fail to emit burn event", "error", err, "asset", synthAsset)
		}
		ctx.Logger().Info("conciliation: savers haircut: savers haircut applied",
			"asset", synthAsset, "bps", bps, "burn_amt", burnAmt,
			"lp_count", len(saverLPs))
	}

	// ── Step 4: treasury LP draw ─────────────────────────────────────────────
	// Covers residual shortfall for zero-saver assets where treasury holds LP.
	// ΔA = step4TreasuryDraw; ΔR = ΔA × BalanceRune / BalanceAsset.
	// Treasury LP units are reduced by ΔA / BalanceAsset (proportional share).
	// Accounting convention: totalStep4PlannedRune / totalStep4DrawnRune track
	// ONLY pools where a draw could be PRICED and attempted — i.e. pools with live
	// depth. uncovered_rune = planned − drawn is then the RUNE for healthy pools we
	// priced but could not fully draw (treasury-LP record missing → full plannedR
	// uncovered; treasury units < needed → the capped-off slice). Pools skipped for
	// zero depth cannot be priced (a draw on an empty pool is 0 RUNE), so forcing
	// them into the netting would add a meaningless 0; they are reported separately
	// in skipped_pools instead so off-chain reconciliation still sees them.
	totalStep4PlannedRune := cosmos.ZeroUint()
	totalStep4DrawnRune := cosmos.ZeroUint()
	var step4SkippedPools []string
	treasuryAddr, err := common.NewAddress(adr028TreasuryAddr)
	if err != nil {
		return fmt.Errorf("conciliation: treasury draw: fail to parse treasury address: %w", err)
	}
	for _, h := range haircuts {
		if h.step4TreasuryDraw == 0 {
			continue
		}
		pool, err := m.mgr.Keeper().GetPool(ctx, h.asset)
		if err != nil {
			return fmt.Errorf("conciliation: treasury draw: fail to get pool %s: %w", h.asset, err)
		}
		if pool.BalanceAsset.IsZero() || pool.BalanceRune.IsZero() {
			ctx.Logger().Error("conciliation: treasury draw: pool has zero balance, skipping",
				"asset", h.asset, "balance_asset", pool.BalanceAsset, "balance_rune", pool.BalanceRune)
			// Zero depth → no priceable draw; report separately (see convention above).
			step4SkippedPools = append(step4SkippedPools, h.asset.String())
			continue
		}
		deltaA := cosmos.NewUint(h.step4TreasuryDraw)
		// Explicit pre-condition: deltaA must not exceed pool depth. GetSafeShare
		// silently caps deltaR at BalanceRune when deltaA > BalanceAsset, so the
		// "LT(deltaR)" guard below would never fire. A bad snapshot amount must be
		// a hard error, not a silent full-pool drain.
		if deltaA.GT(pool.BalanceAsset) {
			return fmt.Errorf("conciliation: treasury draw: pool %s step4TreasuryDraw %s exceeds BalanceAsset %s — snapshot out of date",
				h.asset, deltaA, pool.BalanceAsset)
		}
		deltaR := common.GetSafeShare(deltaA, pool.BalanceAsset, pool.BalanceRune)
		plannedR := deltaR
		// LP units corresponding to ΔA of the pool (prop to total).
		deltaLP := common.GetSafeShare(deltaA, pool.BalanceAsset, pool.LPUnits)
		// Reduce treasury LP record.
		treasuryLP, err := m.mgr.Keeper().GetLiquidityProvider(ctx, h.asset, treasuryAddr)
		if err != nil {
			ctx.Logger().Error("conciliation: treasury draw: fail to get treasury LP, skipping",
				"asset", h.asset, "error", err)
			totalStep4PlannedRune = totalStep4PlannedRune.Add(plannedR)
			continue
		}
		if treasuryLP.Units.LT(deltaLP) {
			ctx.Logger().Error("conciliation: treasury draw: treasury LP units < deltaLP, capping",
				"asset", h.asset, "treasury_units", treasuryLP.Units, "delta_lp", deltaLP)
			deltaLP = treasuryLP.Units
			// Recompute deltaA, deltaR from the capped deltaLP.
			deltaA = common.GetSafeShare(deltaLP, pool.LPUnits, pool.BalanceAsset)
			deltaR = common.GetSafeShare(deltaA, pool.BalanceAsset, pool.BalanceRune)
		}
		treasuryLP.Units = common.SafeSub(treasuryLP.Units, deltaLP)
		m.mgr.Keeper().SetLiquidityProvider(ctx, treasuryLP)
		pool.LPUnits = common.SafeSub(pool.LPUnits, deltaLP)
		pool.BalanceAsset = common.SafeSub(pool.BalanceAsset, deltaA)
		pool.BalanceRune = common.SafeSub(pool.BalanceRune, deltaR)
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("conciliation: treasury draw: fail to save pool %s: %w", h.asset, err)
		}
		// Freed RUNE: burn HALF, retain HALF in the Reserve (was: burn all).
		if err := m.splitFreedRune(ctx, deltaR, "conciliation_treasury_draw"); err != nil {
			return fmt.Errorf("conciliation: treasury draw: fail to dispose freed RUNE for %s: %w", h.asset, err)
		}
		mod := NewPoolMod(h.asset, deltaR, false, deltaA, false)
		emitPoolBalanceChangedEvent(ctx, mod, "conciliation_treasury_draw", m.mgr)
		totalStep4PlannedRune = totalStep4PlannedRune.Add(plannedR)
		totalStep4DrawnRune = totalStep4DrawnRune.Add(deltaR)
		ctx.Logger().Info("conciliation: treasury draw: treasury LP draw applied",
			"asset", h.asset, "delta_asset", deltaA, "delta_rune", deltaR, "delta_lp", deltaLP)
	}
	ctx.Logger().Info("conciliation: treasury draw: coverage summary",
		"total_planned_rune", totalStep4PlannedRune,
		"total_drawn_rune", totalStep4DrawnRune,
		"uncovered_rune", common.SafeSub(totalStep4PlannedRune, totalStep4DrawnRune),
		"skipped_pools", step4SkippedPools,
		"skipped_pool_count", len(step4SkippedPools))

	// ── Step 4.5: recompute SynthUnits on every L1 pool the waterfall touched ─
	//
	// Steps 2 and 3 burn synth supply via direct bank ops (Reserve→ModuleName→burn
	// for step 2; Asgard→ModuleName→burn for step 3); these do NOT update the L1
	// pool's SynthUnits in the keeper — that recompute is normally deferred to
	// the next swap/withdraw/add-liquidity on that pool via `pool.CalcUnits(...)`.
	//
	// Step 1 reduces BalanceAsset without touching synth supply; the formula
	// SU = (LPUnits × supply) / (2×BalanceAsset − supply) has BalanceAsset in
	// the denominator, so a depth haircut also inflates SynthUnits on next
	// CalcUnits — meaning LPs lose MORE share than just the depth haircut.
	//
	// Without an explicit recompute here, LPs withdrawing in the window between
	// this upgrade block and the first swap/withdraw/add on the pool would read
	// the stale keeper-persisted SynthUnits → wrong (under-)payout. Closing that
	// window is the reason this step exists and must be kept.
	//
	// NOTE: step 5's runePoolValue()/polPoolValue() does NOT depend on this
	// recompute. polPoolValue (helpers.go) calls pool.CalcUnits(GetTotalSupply)
	// on a fresh in-memory pool copy *before* reading GetPoolUnits(), so it never
	// consumes the keeper-persisted SynthUnits field — the POL solve is correct
	// whether or not step 4.5 has run. (An earlier comment here claimed a stale
	// SynthUnits would inflate the POL denominator and make POL over-absorb; that
	// is false for the reason just stated.) The persisted recompute matters only
	// for the LP-withdraw window above.
	//
	// Fix: iterate every haircut-table entry whose step 1/2/3 touched the
	// underlying L1 pool or its synth supply, call pool.CalcUnits(synthSupply)
	// with the post-migration supply, persist via SetPool. Step 4 already keeps
	// LPUnits and BalanceAsset proportional, so its pools also benefit (CalcUnits
	// re-anchors them too without changing the ratio).
	for _, h := range haircuts {
		if h.step1RuneHaircut == 0 && h.step2ReserveSynthBurn == 0 &&
			h.step3SaversHaircutBps == 0 && h.step4TreasuryDraw == 0 {
			continue
		}
		pool, err := m.mgr.Keeper().GetPool(ctx, h.asset)
		if err != nil {
			return fmt.Errorf("conciliation: synthunits recompute: fail to get pool %s: %w", h.asset, err)
		}
		synthSupply := m.mgr.Keeper().GetTotalSupply(ctx, h.asset.GetSyntheticAsset())
		oldSU := pool.SynthUnits
		pool.CalcUnits(synthSupply)
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("conciliation: synthunits recompute: fail to save pool %s: %w", h.asset, err)
		}
		ctx.Logger().Info("conciliation: synthunits recompute: pool.SynthUnits recomputed",
			"asset", h.asset, "synth_supply", synthSupply, "old_synth_units", oldSU, "new_synth_units", pool.SynthUnits)
	}

	// ── POL provider-whole solve (after all pool writes) ─────────────────────
	// Now that Steps 1–4 have applied every pool mutation (POL haircut, reserve
	// + savers synth burns, treasury draw), re-snapshot POL value and solve
	// RUNEPool.ReserveUnits so provider value is preserved (the reserve bucket
	// absorbs the entire loss). Steps 2–4 burn synths and draw treasury LP from
	// pools that also hold POL, each of which raises POL value, so vPost must be
	// taken here rather than right after Step 1. providerValuePre was captured
	// before any pool write.
	vPost, err := runePoolValue(ctx, m.mgr)
	if err != nil {
		return fmt.Errorf("conciliation: pol solve: fail to compute runePoolValue post: %w", err)
	}
	switch {
	case runePool.PoolUnits.IsZero():
		// No provider (POL) units to hold whole — leave ReserveUnits untouched.
		// Without this guard providerValuePre would be 0 and the solve below would
		// collapse ReserveUnits to 0, wiping a reserve-only RUNEPool position.
		ctx.Logger().Info("conciliation: pol solve: PoolUnits is zero; leaving ReserveUnits unchanged",
			"reserve_units", runePool.ReserveUnits)
	case providerValuePre.GTE(vPost) || runePool.TotalUnits().IsZero():
		// Edge case: reserve is fully wiped or no units — set ReserveUnits to 0.
		ctx.Logger().Error("conciliation: pol solve: providerValuePre >= vPost or TotalUnits=0; zeroing ReserveUnits",
			"v_pre", vPre, "v_post", vPost, "provider_value_pre", providerValuePre)
		runePool.ReserveUnits = cosmos.ZeroUint()
	default:
		// ReserveUnits_post = PoolUnits × vPost / providerValuePre − PoolUnits
		newTotalUnits := common.GetUncappedShare(runePool.PoolUnits, providerValuePre, vPost)
		runePool.ReserveUnits = common.SafeSub(newTotalUnits, runePool.PoolUnits)
	}
	m.mgr.Keeper().SetRUNEPool(ctx, runePool)
	ctx.Logger().Info("conciliation: pol solve: RUNEPool.ReserveUnits adjusted",
		"v_pre", vPre, "v_post", vPost,
		"provider_value_pre", providerValuePre,
		"new_reserve_units", runePool.ReserveUnits)
	// STRICT-WATERFALL safety: the layer-1 removal is capped by reserve_pool_capacity
	// precisely so that vPost never falls below providerValuePre, i.e. step 5's wipe
	// branch is NEVER taken when any POL units were removed. If ReserveUnits is zero
	// here while layer 1 removed POL units, the reserve ceiling was mis-derived and the
	// loss spilled onto RUNEPool providers — log an ERROR for off-chain reconciliation.
	if runePool.ReserveUnits.IsZero() && !totalPOLUnitsRemoved.IsZero() {
		ctx.Logger().Error("conciliation: pol solve: ReserveUnits ZEROED despite layer-1 POL removal — reserve ceiling mis-derived; loss may have hit RUNEPool providers",
			"total_pol_units_removed", totalPOLUnitsRemoved,
			"v_pre", vPre, "v_post", vPost, "provider_value_pre", providerValuePre)
	}

	// ── Step 5: settle the stuck streaming swap ──────────────────────────────
	// Tx 5071A7 was an ETH.DAI→ETH.USDT streaming swap, 484/1182 sub-swaps done.
	// Two amounts are owed to the user (the swap's destination):
	//   1. StreamingSwap.Out — ETH.USDT already produced by the completed
	//      sub-swaps. The ETH.USDT pool was already debited for it; it never paid
	//      out because the swap never completed. We schedule it as a normal
	//      ETH.USDT outbound from a live Asgard vault — unlike a queued swap, an
	//      outbound persists in the queue until ETH signing resumes. If no active
	//      vault holds enough ETH.USDT to schedule it, we log and skip rather than
	//      abort the migration; the treasury then covers this portion off-chain.
	//   2. The remainder of the user's guaranteed minimum not paid on-chain
	//      (TradeTarget − amount actually scheduled) — its input DAI was stuck in
	//      the drained 2v28 vault. The treasury refunds it to the user off-chain
	//      from its own funds and follows up for reimbursement separately, so the
	//      migration moves no funds for it; we only log the amount owed. (We also
	//      do not queue an on-chain swap: it would run later in EndBlock under
	//      full validation and be aborted while trading is halted.)
	//
	// Trading is halted at upgrade height, so no further sub-swaps execute and
	// StreamingSwap.Out is final — we read it live and pay exactly that.
	// (stuckStreamingTxStr is defined in migration_data_15to16.go.)
	stuckTxID, err := common.NewTxID(stuckStreamingTxStr)
	if err != nil {
		return fmt.Errorf("conciliation: streaming swap: failed to parse txid: %w", err)
	}

	// Idempotent: if the swap already completed or was removed between the
	// snapshot and the upgrade height there is nothing to settle. GetStreamingSwap
	// returns a zero-valued record with a nil error when the key is absent, so
	// check existence explicitly.
	if !m.mgr.Keeper().StreamingSwapExists(ctx, stuckTxID) {
		ctx.Logger().Info("conciliation: streaming swap: stuck swap already absent; nothing to settle",
			"tx", stuckStreamingTxStr)
		return nil
	}

	stuckSwap, err := m.mgr.Keeper().GetStreamingSwap(ctx, stuckTxID)
	if err != nil {
		return fmt.Errorf("conciliation: streaming swap: failed to get streaming swap: %w", err)
	}
	// The destination lives on the queue item (MsgSwap); it is needed only to pay
	// the on-chain outbound. Tolerate its absence (best-effort) so a drifted or
	// consumed queue item does not abort the upgrade — the treasury then refunds
	// the full amount off-chain.
	var destination common.Address
	if stuckQueueMsg, qErr := m.mgr.Keeper().GetSwapQueueItem(ctx, stuckTxID, 0); qErr == nil {
		destination = stuckQueueMsg.Destination
	}

	// 1. Pay the already-produced ETH.USDT (StreamingSwap.Out) as a best-effort
	//    outbound from a live Asgard vault (the outbound fee is deducted by
	//    TxOutStore). ethUSDT was parsed above for the haircut table. Everything
	//    here is best-effort: if the destination is unknown or no active vault
	//    holds enough ETH.USDT to schedule it (e.g. the L1 USDT distribution at
	//    upgrade height does not satisfy it), log and fall through rather than
	//    abort the whole migration — the treasury then refunds this portion
	//    off-chain too.
	usdtOut := stuckSwap.Out
	usdtPaidOnChain := cosmos.ZeroUint()
	switch {
	case usdtOut.IsZero():
		// No output produced yet; the whole guaranteed minimum is refunded off-chain.
	case destination.IsEmpty():
		ctx.Logger().Error("conciliation: streaming swap: unknown destination; skipping ETH.USDT outbound, treasury to refund off-chain",
			"usdt_out", usdtOut)
	default:
		usdtCoin := common.NewCoin(ethUSDT, usdtOut)
		if err := unsafeAddRefundOutbound(ctx, m.mgr, stuckStreamingTxStr, destination.String(), usdtCoin, ctx.BlockHeight()); err != nil {
			ctx.Logger().Error("conciliation: streaming swap: could not schedule ETH.USDT outbound; treasury to refund it off-chain",
				"error", err, "usdt_out", usdtOut, "beneficiary", destination)
		} else {
			usdtPaidOnChain = usdtOut
		}
	}

	// 2. Whatever is not paid on-chain (TradeTarget − usdtPaidOnChain — the
	//    unfulfilled minimum plus any USDT that could not be scheduled above) is
	//    refunded to the user off-chain by the treasury from its own funds; the
	//    treasury follows up for reimbursement separately. The migration moves no
	//    funds for it — record the amount owed.
	//
	// POLICY / FINALITY CAVEAT: usdtPaidOnChain is the amount we *queued* as an
	// outbound, and this netting assumes that queued outbound ultimately settles
	// on-chain. A TxOutItem is durable — it persists in the outbound queue and is
	// retried until ETH signing resumes — so under normal operation "queued" does
	// reach the user. But if that outbound is *permanently* unsignable (vault
	// churn, asset no longer held, manual queue surgery), the user would be short
	// both on-chain (never delivered) and off-chain (already netted out here).
	// The treasury MUST monitor finality of the usdt_paid_onchain outbound and
	// top up the user off-chain if it never lands. We log this explicitly below.
	usdtOffchainRefund := common.SafeSub(stuckSwap.TradeTarget, usdtPaidOnChain)
	if !usdtPaidOnChain.IsZero() {
		ctx.Logger().Warn("conciliation: streaming swap: off-chain refund nets out a QUEUED (not yet finalized) outbound; treasury must verify on-chain delivery and top up off-chain if it fails to settle",
			"usdt_paid_onchain", usdtPaidOnChain,
			"usdt_offchain_refund", usdtOffchainRefund,
			"beneficiary", destination)
	}

	// 3. Stop the stuck swap: remove the streaming-swap record and queue item so
	//    the remaining sub-swaps never execute.
	m.mgr.Keeper().RemoveStreamingSwap(ctx, stuckTxID)
	m.mgr.Keeper().RemoveSwapQueueItem(ctx, stuckTxID, 0)

	ctx.Logger().Info("conciliation: streaming swap: settled stuck swap",
		"tx", stuckStreamingTxStr,
		"usdt_produced", usdtOut,
		"usdt_paid_onchain", usdtPaidOnChain,
		"usdt_offchain_refund", usdtOffchainRefund,
		"beneficiary", destination)

	return nil
}
