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
//     SafeSub clamping in vault.SubFundsWithClamping during outbound observation when the
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
	// SafeSub clamping in vault.SubFundsWithClamping during outbound observation.
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

// Migrate16to17 requeues dangling ETH, GAIA, BTC, and LTC user settlement outbounds
// whose planned voter actions are no longer present in the live outbound queue.
func (m Migrator) Migrate16to17(ctx sdk.Context) error {
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	specs, err := mainnetDanglingSettlementSpecs16to17()
	if err != nil {
		return err
	}

	requeueDanglingSettlementOutbounds(ctx, m.mgr, specs, ctx.BlockHeight()+300)

	return nil
}

// requeueDanglingSettlementOutbounds applies best-effort requeues for the fixed
// mainnet migration batch. One bad or already-settled row is logged and skipped
// so drifted live-chain state does not abort the whole store migration.
func requeueDanglingSettlementOutbounds(ctx cosmos.Context, mgr *Mgrs, specs []danglingSettlementSpec, outboundHeight int64) {
	for _, spec := range specs {
		if err := requeueDanglingSettlementOutbound(ctx, mgr, spec, outboundHeight); err != nil {
			ctx.Logger().Error("failed to requeue dangling settlement outbound",
				"tx_id", spec.TxID.String(),
				"chain", spec.Chain.String(),
				"asset", spec.Asset.String(),
				"amount", spec.Amount.String(),
				"error", err)
			continue
		}
		ctx.Logger().Info("requeued dangling settlement outbound",
			"tx_id", spec.TxID.String(),
			"chain", spec.Chain.String(),
			"asset", spec.Asset.String(),
			"amount", spec.Amount.String())
	}
}

func mainnetDanglingSettlementSpecs16to17() ([]danglingSettlementSpec, error) {
	ethUSDT, err := common.NewAsset("ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7")
	if err != nil {
		return nil, fmt.Errorf("failed to parse ETH.USDT: %w", err)
	}
	ethUSDC, err := common.NewAsset("ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48")
	if err != nil {
		return nil, fmt.Errorf("failed to parse ETH.USDC: %w", err)
	}
	ethWBTC, err := common.NewAsset("ETH.WBTC-0X2260FAC5E5542A773AA44FBCFEDF7C193BC2C599")
	if err != nil {
		return nil, fmt.Errorf("failed to parse ETH.WBTC: %w", err)
	}

	rows := []struct {
		txID   string
		chain  common.Chain
		asset  common.Asset
		amount uint64
	}{
		{
			txID:   "62566F78A66B8C1D6D756A93E28CA0AD07532FF5949A1393B5828429A1AD0A7B",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 236_382_280_700,
		},
		{
			txID:   "15D63325B96BD0FE409EB1FDE21D8A6145894D364D14F98C17068C704CFBE749",
			chain:  common.ETHChain,
			asset:  ethUSDT,
			amount: 499_997_734_700,
		},
		{
			txID:   "74D5D2F1BE68CB15DF9656BD2A11733FC64FE17CF0DC030A9F5948D93AC5A561",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 499_997_734_500,
		},
		{
			txID:   "4FE29CBBB8A82728E7A1A8E5E4527830B53E6132E74BCB51B02EBBB94CC26597",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 742_745_645_600,
		},
		{
			txID:   "D8A75A8427814347C36AFB1E972785C90D1AF2BCDA8195DAD152AE5C15E08D62",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 97_734_500,
		},
		{
			txID:   "EBE39D82C348CCE1F004D61870C458BD86D1BE6AD202C7C6A1B33989B6774D14",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 97_734_500,
		},
		{
			txID:   "84AF6BC1ABEE78AD1B2EBCD5F1A6FF4773B53E734F57EC840B55BE55039A2163",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 2_981_767_801_900,
		},
		{
			txID:   "D537A15D820D9A800ECCEB5735C1A01ACB89F7EE9CB9D5F0F041DD60403EADDB",
			chain:  common.ETHChain,
			asset:  ethUSDT,
			amount: 799_997_967_100,
		},
		{
			txID:   "DC1F405EA046C804B857321504EF1A8EDBEFED942D26F40EF80311DAA943BF11",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 627_955_000,
		},
		{
			txID:   "93FD5B47E74B7C20881464641FF0A753B65A9BB64E70B75C176B353B31B23779",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 997_734_500,
		},
		{
			txID:   "C53DEFFCF8A62C80F0EB3DD0C97E35C6AAE67BD725C8B070A2EAF619EBDCA367",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 124_436_600,
		},
		{
			txID:   "3C1F5BE0E6AD5296A95B539F137EED2BAC649FFF869F7CCD6245A70CA12569BE",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 4_997_734_500,
		},
		{
			txID:   "28D17EC1B66DEABCC9325C532D471D61B9E2276FF8AD6314908783EF50BEBDD0",
			chain:  common.ETHChain,
			asset:  ethWBTC,
			amount: 972,
		},
		{
			txID:   "33BFFE8CABAD8BF30ECDCE39EC6A2FCFB081F85E707B8BCAD8657997C8535FE4",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 203_911_502_000,
		},
		{
			txID:   "46994110DB09621EC5D793EF57C17A00580E4E25F72252B8F6617955CA9C8671",
			chain:  common.ETHChain,
			asset:  ethUSDC,
			amount: 51_517_759_500,
		},
		{
			txID:   "DC8EEB91473665D60E8CE09A7BB65AB1D66026CD90159A5794FCD1FE7DC832C8",
			chain:  common.GAIAChain,
			asset:  common.ATOMAsset,
			amount: 12_599_998_000,
		},
		{
			txID:   "71633631A00B7085E202351DBF3B7AE52A573BAB15FE6F2D8165878A8A7D8883",
			chain:  common.GAIAChain,
			asset:  common.ATOMAsset,
			amount: 249_997_000,
		},
		{
			txID:   "14A6024EC7688F3243937F43E302FB1D934D7B55AA9CDB28D352E118D96969E3",
			chain:  common.GAIAChain,
			asset:  common.ATOMAsset,
			amount: 9_996_000,
		},
		{
			txID:   "4C0FE03225B88FC460118285D43BE60AB903DFDDE95EE0AB12443CE17C35DB75",
			chain:  common.GAIAChain,
			asset:  common.ATOMAsset,
			amount: 19_998_000,
		},
		{
			txID:   "202B832EA7EB06C5BDFAF9459E1ED7C143A67B557FB6DFF43C8956FB030BFAD5",
			chain:  common.GAIAChain,
			asset:  common.ATOMAsset,
			amount: 8_869_997_000,
		},
		{
			txID:   "A509E8F11121122BD4A8DC87103D2BA3141885A4EDD2413A2CBF3FDF6D2CF495",
			chain:  common.LTCChain,
			asset:  common.LTCAsset,
			amount: 6_814_283,
		},
		{
			txID:   "07612B63ACFFDDCBD3B005324BC20F316E527959F74155823DE3B85E5878D1AD",
			chain:  common.LTCChain,
			asset:  common.LTCAsset,
			amount: 6_482_864_307,
		},
		{
			txID:   "B06FFD1F8DC8B3101760F7EDB52C861EDA1877956A6FACDCBCF7910A522BFB11",
			chain:  common.BTCChain,
			asset:  common.BTCAsset,
			amount: 387_417_726,
		},
		// These BTC rows are safe as-is: miner fees reduce the vault change,
		// so the recipient output still delivers the full amount.
		{
			txID:   "FA0FE33222A00020D6B1A4091828518E41CC792AA47F8C2CAE31B8DA4A7249BC",
			chain:  common.BTCChain,
			asset:  common.BTCAsset,
			amount: 2_370,
		},
		{
			txID:   "C36C8291FC97B421B2498DCFD88AAD0979FDBD4941DB43802F90BFDC5E11A77C",
			chain:  common.BTCChain,
			asset:  common.BTCAsset,
			amount: 4_572,
		},
		{
			txID:   "E3097EA59345B8908A91FCA42E567E025515E8143890481BC75F7E85BE8C6315",
			chain:  common.BTCChain,
			asset:  common.BTCAsset,
			amount: 10_169,
		},
	}

	specs := make([]danglingSettlementSpec, 0, len(rows))
	for _, row := range rows {
		txID, err := common.NewTxID(row.txID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse settlement tx id %s: %w", row.txID, err)
		}
		specs = append(specs, danglingSettlementSpec{
			TxID:   txID,
			Chain:  row.chain,
			Asset:  row.asset,
			Amount: cosmos.NewUint(row.amount),
		})
	}

	return specs, nil
}

// adr028WritedownSpec identifies one pool whose BalanceAsset must be written down
// by `deltaAsset` (1e8) to remove the ADR-028 residual "phantom depth" — pool
// depth in excess of the live-backed Asgard vault holdings.
type adr028WritedownSpec struct {
	asset common.Asset
	// deltaAsset is the phantom slice to remove: pool.BalanceAsset − live backing.
	// It is stored as a DELTA rather than an absolute target because the delta is
	// height-invariant (a swap credits the pool and the vault equally, so the gap
	// is a frozen historical amount) while an absolute target goes stale the
	// moment the pool trades. The write target is derived from live depth at
	// execution: target = pool.BalanceAsset − deltaAsset.
	deltaAsset cosmos.Uint
}

// Migrate17to18 completes the ADR-028 reconciliation begun in Migrate15to16 by
// writing down the residual pool "phantom depth". Migrate15to16 cut the
// record/obligation side but left two halves unfinished: pool depth that still
// exceeds live-backed Asgard holdings (the ~-$3.5M DEFICIT, corrected here), and
// the freed L1 asset left physically in Asgard (the ~+$3.1M orphaned-ΔA SURPLUS,
// left in place — excess L1 backing, net-solvent, swept later if ever).
//
// For each deficit pool, write pool.BalanceAsset down to
//
//	target = max(0, asgard − trade.depth − secured.depth − in-flight-stream)
//
// so pool + trade + secured == asgard. trade.depth and secured.depth are 1:1
// redemption claims honored at par and are NOT touched. BalanceRune is reduced by
// the symmetric ratio ΔR = ΔA × BR/BA to hold pool price constant, and the freed
// RUNE is moved Asgard→Reserve in FULL — none burned (all cut RUNE to Reserve),
// matching the seed pools. After all pool writes,
// RUNEPool.ReserveUnits is re-solved so POL providers are held whole (the reserve
// bucket absorbs the depth loss), same as Migrate15to16.
//
// Snapshot basis: THOR height 26,967,482. The per-pool targets are POINT-IN-TIME
// and MUST be re-derived against fresh chain state at the actual upgrade height
// and hardcoded before release, exactly as Migrate15to16's 2026-05-26 snapshot.
func (m Migrator) Migrate17to18(ctx sdk.Context) error {
	if err := m.mgr.LoadManagerIfNecessary(ctx); err != nil {
		return err
	}

	specs, err := mainnetADR028WritedownSpecs17to18()
	if err != nil {
		return err
	}

	// Snapshot POL provider value BEFORE any pool write; held whole by the
	// ReserveUnits re-solve after all writes (reserve absorbs the depth loss).
	runePool, err := m.mgr.Keeper().GetRUNEPool(ctx)
	if err != nil {
		return fmt.Errorf("adr028 writedown: fail to get RUNEPool: %w", err)
	}
	vPre, err := runePoolValue(ctx, m.mgr)
	if err != nil {
		return fmt.Errorf("adr028 writedown: fail to compute runePoolValue pre: %w", err)
	}
	providerValuePre := common.GetSafeShare(runePool.PoolUnits, runePool.TotalUnits(), vPre)

	// ── Pre-flight balance checks — before any mutation ──────────────────────
	// A mid-loop send failure is NOT recoverable partial state: RunMigrations
	// returns the error, the upgrade handler panics, and the block never commits
	// — deterministically on every node, i.e. a chain halt at the upgrade height
	// needing an emergency binary. Verify up front that Asgard can fund the
	// writedown ΔR moves (ΣΔR, summed exactly as the loop below computes/skips it)
	// plus the seed pools' freed RUNE, and that the Reserve can fund the seed draws
	// plus the Secured-Ops coverage, so a shortfall aborts cleanly instead.
	totalDeltaR := cosmos.ZeroUint()
	for _, spec := range specs {
		pool, err := m.mgr.Keeper().GetPool(ctx, spec.asset)
		if err != nil || pool.IsEmpty() || pool.BalanceAsset.LTE(spec.deltaAsset) {
			continue
		}
		dR := common.GetUncappedShare(spec.deltaAsset, pool.BalanceAsset, pool.BalanceRune)
		if dR.IsZero() || dR.GTE(pool.BalanceRune) {
			continue // mirrors the writedown loop's skip below
		}
		totalDeltaR = totalDeltaR.Add(dR)
	}
	// The seed step (seedADR028Pools) also moves RUNE both ways: a seed pool with
	// more RUNE than its $5k target frees the excess Asgard→Reserve; one with less
	// draws the shortfall Reserve→Asgard. Mirror both, so the Asgard guard covers
	// ΣΔR AND the (often larger) seed-freed drains, and the Reserve guard covers
	// the seed draws AND the Secured-Ops coverage.
	seedSpecs, err := mainnetADR028SeedSpecs17to18()
	if err != nil {
		return err
	}
	seedDraw, seedFreed := cosmos.ZeroUint(), cosmos.ZeroUint()
	for _, spec := range seedSpecs {
		pool, err := m.mgr.Keeper().GetPool(ctx, spec.asset)
		if err != nil || pool.IsEmpty() {
			continue
		}
		switch {
		case spec.seedRune.GT(pool.BalanceRune):
			seedDraw = seedDraw.Add(common.SafeSub(spec.seedRune, pool.BalanceRune))
		case pool.BalanceRune.GT(spec.seedRune):
			seedFreed = seedFreed.Add(common.SafeSub(pool.BalanceRune, spec.seedRune))
		}
	}
	asgardNeed := totalDeltaR.Add(seedFreed)
	if bal := m.mgr.Keeper().GetRuneBalanceOfModule(ctx, AsgardName); bal.LT(asgardNeed) {
		return fmt.Errorf("adr028 writedown: Asgard RUNE %s < ΣΔR+seedFreed %s; aborting before any mutation", bal, asgardNeed)
	}
	reserveNeed := cosmos.NewUint(adr028SecuredOpsCoverageRune).Add(seedDraw)
	if bal := m.mgr.Keeper().GetRuneBalanceOfModule(ctx, ReserveName); bal.LT(reserveNeed) {
		return fmt.Errorf("adr028 writedown: Reserve RUNE %s < coverage+seedDraw %s; aborting before any mutation", bal, reserveNeed)
	}

	// Snapshot pre-mutation CalcUnits safety for every touched pool. After all
	// mutations, the recompute loop checks whether any pool TRANSITIONED from
	// safe (2×BA > synthSupply) to unsafe (2×BA ≤ synthSupply) — a migration-
	// caused CalcUnits blow-up that must abort. Pre-existing unsafe pools are
	// logged and skipped (the migration didn't create or worsen them).
	preMigrationSafe := make(map[string]bool, len(specs)+len(seedSpecs))
	for _, s := range specs {
		pool, err := m.mgr.Keeper().GetPool(ctx, s.asset)
		if err != nil || pool.IsEmpty() {
			continue
		}
		ss := m.mgr.Keeper().GetTotalSupply(ctx, s.asset.GetSyntheticAsset())
		preMigrationSafe[s.asset.String()] = pool.BalanceAsset.MulUint64(2).GT(ss)
	}
	for _, s := range seedSpecs {
		if _, already := preMigrationSafe[s.asset.String()]; already {
			continue // pool appears in both specs — writedown snapshot is canonical
		}
		pool, err := m.mgr.Keeper().GetPool(ctx, s.asset)
		if err != nil || pool.IsEmpty() {
			continue
		}
		ss := m.mgr.Keeper().GetTotalSupply(ctx, s.asset.GetSyntheticAsset())
		preMigrationSafe[s.asset.String()] = pool.BalanceAsset.MulUint64(2).GT(ss)
	}

	for _, spec := range specs {
		pool, err := m.mgr.Keeper().GetPool(ctx, spec.asset)
		if err != nil {
			return fmt.Errorf("adr028 writedown: fail to get pool %s: %w", spec.asset, err)
		}
		if pool.IsEmpty() {
			ctx.Logger().Error("adr028 writedown: empty pool; skipping", "asset", spec.asset)
			continue
		}
		// Skip if the pool already sits at/below target (drifted since snapshot) —
		// a writedown would only apply if depth still exceeds backing.
		// The write target is derived from LIVE depth, not hardcoded: the phantom
		// slice is height-invariant, so removing it from whatever the pool holds at
		// execution keeps the migration correct however long after the snapshot the
		// upgrade fires. A pool that has since shrunk below its own phantom slice
		// contradicts that invariant — skip it loudly rather than zero the pool.
		if pool.BalanceAsset.LTE(spec.deltaAsset) {
			ctx.Logger().Error("adr028 writedown: BalanceAsset <= phantom delta; skipping",
				"asset", spec.asset, "balance_asset", pool.BalanceAsset, "delta", spec.deltaAsset)
			continue
		}
		deltaA := spec.deltaAsset
		target := common.SafeSub(pool.BalanceAsset, deltaA)
		// ΔR = ΔA × BR/BA — symmetric AMM identity, holds pool price constant.
		deltaR := common.GetUncappedShare(deltaA, pool.BalanceAsset, pool.BalanceRune)
		if deltaR.IsZero() {
			continue // dust pool: ΔR rounds to 0, nothing to move
		}
		if deltaR.GTE(pool.BalanceRune) {
			ctx.Logger().Error("adr028 writedown: ΔR >= BalanceRune (would zero or underflow the RUNE side); skipping",
				"asset", spec.asset, "balance_rune", pool.BalanceRune, "delta_r", deltaR)
			continue
		}

		pool.BalanceAsset = target
		pool.BalanceRune = common.SafeSub(pool.BalanceRune, deltaR)
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("adr028 writedown: fail to set pool %s: %w", spec.asset, err)
		}

		// Resolve CalcUnits clamp (2×target ≤ synthSupply) by reducing synth
		// supply. Two paths:
		//   UNDERBACKED (named pools with live savers + vault free backing):
		//     step-3 savers haircut — settle savers at real free backing.
		//   All others: BurnEntireSynthSupply.
		// The pool set is explicit because the generic "vault − pool.BA"
		// gate doesn't account for trade/secured claims that reduce free
		// backing; hardcoding avoids burning savers that should be settled.
		synthAsset := spec.asset.GetSyntheticAsset()
		synthSupply := m.mgr.Keeper().GetTotalSupply(ctx, synthAsset)
		if !synthSupply.IsZero() && target.MulUint64(2).LTE(synthSupply) {
			if adr028SaversHaircutPools[spec.asset.String()] {
				freeBacking, err := m.adr028FreeBacking(ctx, spec.asset, target)
				if err != nil {
					return fmt.Errorf("adr028 writedown: %w", err)
				}
				if !freeBacking.IsZero() {
					if err := m.adr028SaversHaircut(ctx, spec.asset, synthAsset, synthSupply, freeBacking); err != nil {
						return fmt.Errorf("adr028 writedown: fail savers haircut for %s: %w", spec.asset, err)
					}
				} else {
					ctx.Logger().Error("adr028 writedown: haircut pool has zero free backing, degrading to full burn",
						"asset", spec.asset)
					burned, err := m.mgr.Keeper().BurnEntireSynthSupply(ctx, synthAsset)
					if err != nil {
						return fmt.Errorf("adr028 writedown: fail to burn synth supply for %s: %w", spec.asset, err)
					}
					ctx.Logger().Info("adr028 writedown: burned entire synth supply (degraded)",
						"asset", spec.asset, "synth_burned", burned)
				}
			} else {
				wasSafe, hasPre := preMigrationSafe[spec.asset.String()]
				if hasPre && !wasSafe {
					ctx.Logger().Info("adr028 writedown: pre-existing CalcUnits clamp, skipping burn",
						"asset", spec.asset, "target", target, "synth_supply", synthSupply)
				} else {
					burned, err := m.mgr.Keeper().BurnEntireSynthSupply(ctx, synthAsset)
					if err != nil {
						return fmt.Errorf("adr028 writedown: fail to burn synth supply for %s: %w", spec.asset, err)
					}
					if adr028ZeroSaversPools[spec.asset.String()] {
						if err := m.adr028ZeroSavers(ctx, synthAsset); err != nil {
							return fmt.Errorf("adr028 writedown: fail to zero savers for %s: %w", spec.asset, err)
						}
					}
					ctx.Logger().Info("adr028 writedown: burned entire synth supply",
						"asset", spec.asset, "synth_burned", burned)
				}
			}
		}

		emitPoolBalanceChangedEvent(ctx, NewPoolMod(spec.asset, deltaR, false, deltaA, false), "adr028_writedown", m.mgr)

		// Dispose the freed RUNE by moving it Asgard→Reserve in FULL — none burned
		// (all cut RUNE goes to Reserve), matching the seed pools. Deliberately NOT
		// splitFreedRune (that half-burns; kept only for the shipped Migrate15to16).
		freedRune := common.NewCoin(common.RuneAsset(), deltaR)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ReserveName, common.NewCoins(freedRune)); err != nil {
			return fmt.Errorf("adr028 writedown: fail to move freed RUNE Asgard→Reserve for %s: %w", spec.asset, err)
		}

		ctx.Logger().Info("adr028 writedown: pool depth written down",
			"asset", spec.asset, "delta_asset", deltaA, "delta_rune", deltaR,
			"new_balance_asset", pool.BalanceAsset, "new_balance_rune", pool.BalanceRune)
	}

	// ── Re-seed the four empty-vault pools to $5k per side ───────────────────
	// These are NOT written down to backing. Both sides are cut to a $5k seed so
	// the pool quotes at market price; the displaced RUNE is moved Asgard→Reserve
	// in full (not burned). Real backing / claim settlement is a separate manual
	// step after the upgrade.
	if err := m.seedADR028Pools(ctx); err != nil {
		return err
	}

	// ── Recompute SynthUnits for ALL pools ──────────────────────────────────
	// CalcUnits derives SU = L·S/(2A−S) from the pool's current BalanceAsset.
	// The writedown and seed paths mutated BalanceAsset without recomputing, so
	// persisted SynthUnits are stale. Recompute before the POL re-solve, which
	// reads GetPoolUnits() (= LPUnits + SynthUnits) via polPoolValue.
	//
	// The CalcUnits-clamp guard aborts only on a SAFE→UNSAFE transition that the
	// migration caused: a pool that was 2×BA > S before mutation but 2×BA ≤ S
	// after. Pre-existing clamp pools (already
	// 2×BA ≤ S before the migration) are logged and skipped; their CalcUnits
	// recompute would just re-persist the clamped value they already had.
	allPools, err := m.mgr.Keeper().GetPools(ctx)
	if err != nil {
		return fmt.Errorf("adr028 writedown: fail to get pools for SynthUnits recompute: %w", err)
	}
	for _, pool := range allPools {
		if pool.Asset.IsNative() || pool.BalanceAsset.IsZero() {
			continue
		}
		synthSupply := m.mgr.Keeper().GetTotalSupply(ctx, pool.Asset.GetSyntheticAsset())
		if pool.BalanceAsset.MulUint64(2).LTE(synthSupply) {
			wasSafe, hasSnapshot := preMigrationSafe[pool.Asset.String()]
			if hasSnapshot && wasSafe {
				// Skip rather than abort: user-held synths surviving the haircut
				// can keep the pool clamped even after the burn. An abort here
				// would deterministically halt the chain at the upgrade height.
				ctx.Logger().Error("adr028 writedown: pool crossed safe→clamp (migration-caused); skipping recompute",
					"asset", pool.Asset, "two_ba", pool.BalanceAsset.MulUint64(2), "synth_supply", synthSupply)
			} else {
				ctx.Logger().Error("adr028 writedown: pre-existing CalcUnits clamp (not migration-caused); skipping recompute",
					"asset", pool.Asset, "balance_asset", pool.BalanceAsset, "synth_supply", synthSupply)
			}
			continue
		}
		pool.CalcUnits(synthSupply)
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("adr028 writedown: fail to save recomputed SynthUnits for %s: %w", pool.Asset, err)
		}
	}

	// ── POL provider-whole solve (after all pool writes) ─────────────────────
	// Re-solve RUNEPool.ReserveUnits so provider value is preserved (the reserve
	// bucket absorbs the depth loss). Identical structure to Migrate15to16; a
	// no-op when POL holds no units in the written-down pools.
	vPost, err := runePoolValue(ctx, m.mgr)
	if err != nil {
		return fmt.Errorf("adr028 writedown: fail to compute runePoolValue post: %w", err)
	}
	switch {
	case runePool.PoolUnits.IsZero():
		ctx.Logger().Info("adr028 writedown: PoolUnits is zero; leaving ReserveUnits unchanged",
			"reserve_units", runePool.ReserveUnits)
	case providerValuePre.GT(vPost) || runePool.TotalUnits().IsZero():
		// Skip the re-solve rather than abort: the writedown should still proceed.
		// The accounting drift is safe-direction (CurrentDeposit overstated → POL
		// under-deposits, not over-deposits). An abort here would deterministically
		// halt the chain at the upgrade height.
		ctx.Logger().Error("adr028 writedown: POL re-solve skipped — providerValuePre > vPost (or TotalUnits=0); leaving ReserveUnits unchanged",
			"v_pre", vPre, "v_post", vPost, "provider_value_pre", providerValuePre,
			"reserve_units", runePool.ReserveUnits)
	default:
		// ReserveUnits_post = PoolUnits × vPost / providerValuePre − PoolUnits
		newTotalUnits := common.GetUncappedShare(runePool.PoolUnits, providerValuePre, vPost)
		runePool.ReserveUnits = common.SafeSub(newTotalUnits, runePool.PoolUnits)
	}
	m.mgr.Keeper().SetRUNEPool(ctx, runePool)
	ctx.Logger().Info("adr028 writedown: RUNEPool.ReserveUnits adjusted",
		"v_pre", vPre, "v_post", vPost,
		"provider_value_pre", providerValuePre,
		"new_reserve_units", runePool.ReserveUnits)

	// ── Secured-Ops coverage settlement ──────────────────────────────────────
	// The three empty-vault pools carry trade+secured claims their drained vaults
	// cannot honour. Send the RUNE-equivalent of that uncovered claim value from
	// the Reserve to the TC Secured-Ops account, which settles the claim holders
	// off-chain in kind.
	if err := m.sendADR028SecuredOpsCoverage(ctx); err != nil {
		return err
	}

	return nil
}

// adr028SecuredOpsAddr is the TC Secured-Ops THORChain account that receives the
// RUNE-equivalent coverage for the empty-vault pools' unbacked trade+secured
// claims (ECDSA pubkey 03af714f…3236fbcd). It is deliberately NOT the similarly
// named Secured-Ops vault (03de3ba3…).
const adr028SecuredOpsAddr = "thor1gkvvzfnkyqv8dfhk0593tfhgr03a3ndgjqdalf"

// adr028SecuredOpsCoverageRune is the RUNE-equivalent of the uncovered
// trade+secured claim value across the three empty-vault seed pools, valued at
// the h=26,967,482 snapshot (RUNE $0.405553, ETH $1,802.40):
//
//	BSC.USDC 23,751.67 + BSC.USDT 23,388.57 + BASE.ETH 9.2217 ETH ≈ $63,761
//	=> 157,221.00 RUNE
//
// Like the seed constants this is $-denominated and MUST be re-derived at the
// upgrade height (RUNE and ETH prices drift) and hardcoded before release.
const adr028SecuredOpsCoverageRune = 15722100323851

// sendADR028SecuredOpsCoverage moves the Secured-Ops coverage RUNE from the
// Reserve to the TC Secured-Ops account. Failure aborts the migration — the
// amount and recipient are fixed, pre-verified constants.
func (m Migrator) sendADR028SecuredOpsCoverage(ctx sdk.Context) error {
	to, err := cosmos.AccAddressFromBech32(adr028SecuredOpsAddr)
	if err != nil {
		return fmt.Errorf("adr028 secured-ops: parse address %s: %w", adr028SecuredOpsAddr, err)
	}
	coin := common.NewCoin(common.RuneNative, cosmos.NewUint(adr028SecuredOpsCoverageRune))
	if err := m.mgr.Keeper().SendFromModuleToAccount(ctx, ReserveName, to, common.NewCoins(coin)); err != nil {
		return fmt.Errorf("adr028 secured-ops: send Reserve→%s: %w", adr028SecuredOpsAddr, err)
	}
	ctx.Logger().Info("adr028 secured-ops: coverage sent from Reserve",
		"to", adr028SecuredOpsAddr, "rune", coin.Amount)
	return nil
}

// mainnetADR028WritedownSpecs17to18 returns the per-pool phantom-depth slice to
// remove for the ADR-028 reconciliation.
//
// SNAPSHOT @ THOR height 26,967,482 (reconciliation inspector's full CSV):
//
//	backing = asgard − trade.depth − secured.depth − stream_inflight
//	delta   = pool.BalanceAsset − backing        (both 1e8)
//
// These are DELTAS, not absolute targets, and that distinction is load-bearing.
// The phantom slice is a frozen historical hole: a swap credits the pool and the
// vault equally, so pool.BalanceAsset and backing move in lockstep and their gap
// does not change with trading. An absolute target, by contrast, goes stale the
// moment the pool trades — measured against live chain state 148,076 blocks
// (~10 days) after this snapshot, shipping absolute targets would have
// over-written-down by ~$302k, concentrated in ETH.USDC/USDT/WBTC/DOGE.
//
// Storing the delta and deriving target = pool.BalanceAsset − delta at execution
// makes the writedown independent of how long after the snapshot the upgrade
// fires. Invariance was verified empirically across all 26 pools over those
// 148,076 blocks: 24 bit-identical, AVAX.USDT −41 native, ETH.USDC −2 native.
// Re-derivation at the upgrade height is therefore a sanity check rather than a
// correctness dependency (unlike the seed constants, which remain $-denominated
// at snapshot prices — see mainnetADR028SeedSpecs17to18).
//
// This set is the non-THOR positive-deficit rows ONLY. Deliberately EXCLUDED:
//   - THOR.TCY / THOR.RUJI: native THORChain tokens whose pool.BalanceAsset is
//     backed by bank/module supply, not asgard vault custody (asgard_record=0 is
//     by design). Writing them down would destroy two solvent pools.
//   - Surplus rows (asgard > obligation): AVAX.AVAX, BCH.BCH, BSC.BTCB, BSC.ETH,
//     BTC.BTC, ETH.ETH, LTC.LTC, SOL.SOL, TRON.TRX, TRON.USDT, XRP.XRP — the
//     orphaned-ΔA/float side, left in place (excess L1 backing, net-solvent).
//   - The four SEEDED pools (BSC.USDC, BSC.USDT, BASE.ETH, ETH.THOR) — they are
//     NOT written down to backing; they are re-seeded and RUNE-funded instead.
//     See mainnetADR028SeedSpecs17to18.
//
// 26 rows total, all positive-target. BSC.BUSD (~$1.17) and ETH.XRUNE are real
// but dead/sunset tokens — retained for completeness; drop both if the
// reconciliation excludes dead tokens.
// adr028SaversHaircutPools identifies writedown pools that carry live saver
// positions with positive vault free backing. These pools get the step-3
// savers haircut (settle savers proportionally at free backing) instead of
// BurnEntireSynthSupply. All other clamped pools are burned entirely.
var adr028SaversHaircutPools = map[string]bool{
	"ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48": true,
}

// adr028ZeroSaversPools identifies UNBACKED writedown pools whose savers
// records must be zeroed alongside BurnEntireSynthSupply. The synth burn
// removes the coins but leaves the saver pool and LP records intact —
// without zeroing, savers would hold phantom units against an empty pool.
var adr028ZeroSaversPools = map[string]bool{
	"ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7":  true,
	"AVAX.USDC-0XB97EF9EF8734C71904D8002F8B6BC66DD9C48A6E": true,
	"BSC.BNB": true,
}

func mainnetADR028WritedownSpecs17to18() ([]adr028WritedownSpec, error) {
	// {asset identifier, writedown delta BalanceAsset (1e8)}. Generated from the
	// inspector CSV (pool_BA(snapshot) − live backing), adjusted upward for the
	// ETH.USDC/ETH.USDT/GAIA.ATOM/ETH.WBTC outbounds that Migrate16to17 requeues
	// at the same upgrade height: those committed coins still sit in vault.coins
	// when the CSV was snapshotted, inflating the apparent backing. Without this
	// adjustment the pools are under-backed once the outbounds settle.
	rows := []struct {
		asset string
		delta uint64
	}{
		{"AVAX.SOL-0XFE6B19286885A4F7F55ADAD09C3CD1F906D2478F", 38893875330},
		{"AVAX.USDC-0XB97EF9EF8734C71904D8002F8B6BC66DD9C48A6E", 18438759809862},
		{"AVAX.USDT-0X9702230A8EA53601F5CD2DC00FDBC13D4DF4A8C7", 4256523275720},
		{"BASE.USDC-0X833589FCD6EDB6E08F4C7C32D4F71B54BDA02913", 685491225982},
		{"BASE.VVV-0XACFE6019ED1A7DC6F7B508C02D1B04EC88CC21BF", 2957939847},
		{"BSC.BNB", 4856114802},
		{"BSC.BUSD-0XE9E7CEA3DEDCA5984780BAFC599BD69ADD087D56", 123066821},
		{"BSC.TWT-0X4B0F1812E5DF2A09796481FF14017E6005508003", 3298025883526},
		{"DOGE.DOGE", 188322561538022},
		{"ETH.AAVE-0X7FC66500C84A76AD7E9C93437BFC5AC33E2DDAE9", 2795812734},
		{"ETH.DAI-0X6B175474E89094C44DA98B954EEDEAC495271D0F", 245264736890},
		{"ETH.DPI-0X1494CA1F11D487C2BBE4543E90080AEBA4BA3C2B", 1143217176},
		{"ETH.FOX-0XC770EEFAD204B5180DF6A14EE197D99D808EE52D", 47623739145492},
		{"ETH.GUSD-0X056FD409E1D7A124BD7017459DFEA2F387B6D5CD", 6107452410472},
		{"ETH.LINK-0X514910771AF9CA656AF840DFF83E8264ECF986CA", 104374231717},
		{"ETH.LUSD-0X5F98805A4E8BE255A32880FDEC7F6728C6568BA0", 6413841177179},
		{"ETH.SNX-0XC011A73EE8576FB46F5E1C5751CA3B9FE0AF2A6F", 99792896784},
		{"ETH.TGT-0X108A850856DB3F85D0269A2693D896B394C80325", 77950001213139},
		{"ETH.USDC-0XA0B86991C6218B36C1D19D4A2E9EB0CE3606EB48", 106612841513705}, // 101889575459905 + 4723266053800 (16to17 requeue)
		{"ETH.USDP-0X8E870D67F660D95D5BE530380D0EC0BD388289E1", 3876245256026},
		{"ETH.USDT-0XDAC17F958D2EE523A2206206994597C13D831EC7", 98310077343961}, // 97010081642161 + 1299995701800 (16to17 requeue)
		{"ETH.VTHOR-0X815C23ECA83261B6EC689B60CC4A58B54BC24D8D", 3438326325955},
		{"ETH.WBTC-0X2260FAC5E5542A773AA44FBCFEDF7C193BC2C599", 520601044}, // 520600072 + 972 (16to17 requeue)
		{"ETH.XRUNE-0X69FA0FEE221AD11012BAB0FDB45D444D3D2CE71C", 7326188839277660},
		{"ETH.YFI-0X0BC529C00C6401AEF6D220BE8C6EA1667F6AD93E", 105337362},
		{"GAIA.ATOM", 1679285461912}, // 1657535475912 + 21749986000 (16to17 requeue)
	}

	specs := make([]adr028WritedownSpec, 0, len(rows))
	for _, r := range rows {
		asset, err := common.NewAsset(r.asset)
		if err != nil {
			return nil, fmt.Errorf("adr028 writedown: parse asset %s: %w", r.asset, err)
		}
		specs = append(specs, adr028WritedownSpec{
			asset:      asset,
			deltaAsset: cosmos.NewUint(r.delta),
		})
	}

	return specs, nil
}

// seedADR028Pools cuts the four re-seeded pools to a $5k-per-side seed. Per pool:
//
//	pool.BalanceAsset = seedAsset   ($5k of asset)
//	pool.BalanceRune  = seedRune    ($5k of RUNE)
//
// This is a symmetric double-sided haircut: both sides are set to $5k so the
// pool quotes at market price (a balanced seed is un-skewed — no arbitrage
// window). The RUNE displaced by collapsing the old depth to the seed is moved
// Asgard→Reserve in FULL — none of it is burned (unlike the writedown path's
// splitFreedRune), so the freed RUNE is retained by the protocol. If a pool's
// RUNE side is somehow already below the seed the shortfall is drawn
// Reserve→Asgard, but for the frozen empty-vault pools that branch is not
// expected to fire. Failures on one pool abort the migration — these are fixed,
// pre-verified rows, so a mismatch means the snapshot is stale.
func (m Migrator) seedADR028Pools(ctx sdk.Context) error {
	specs, err := mainnetADR028SeedSpecs17to18()
	if err != nil {
		return err
	}

	for _, spec := range specs {
		pool, err := m.mgr.Keeper().GetPool(ctx, spec.asset)
		if err != nil {
			return fmt.Errorf("adr028 seed: fail to get pool %s: %w", spec.asset, err)
		}
		if pool.IsEmpty() {
			ctx.Logger().Error("adr028 seed: empty pool; skipping", "asset", spec.asset)
			continue
		}

		oldAsset, oldRune := pool.BalanceAsset, pool.BalanceRune

		// Reconcile the pool's RUNE side with Asgard: move the displaced RUNE to
		// the Reserve in full (no burn), or draw the shortfall from the Reserve.
		switch {
		case oldRune.GT(spec.seedRune):
			freed := common.NewCoin(common.RuneNative, common.SafeSub(oldRune, spec.seedRune))
			if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ReserveName, common.NewCoins(freed)); err != nil {
				return fmt.Errorf("adr028 seed: fail to move freed RUNE Asgard→Reserve for %s: %w", spec.asset, err)
			}
		case spec.seedRune.GT(oldRune):
			fund := common.NewCoin(common.RuneNative, common.SafeSub(spec.seedRune, oldRune))
			if err := m.mgr.Keeper().SendFromModuleToModule(ctx, ReserveName, AsgardName, common.NewCoins(fund)); err != nil {
				return fmt.Errorf("adr028 seed: fail to fund %s Reserve→Asgard: %w", spec.asset, err)
			}
		}

		// Burn the pool's entire synth supply if it would trip the CalcUnits
		// blow-up (2×seedBA ≤ synthSupply). These are frozen ThorFi-era residuals
		// that treasury already settled; zeroing writes off a settled liability.
		synthAsset := spec.asset.GetSyntheticAsset()
		synthSupply := m.mgr.Keeper().GetTotalSupply(ctx, synthAsset)
		if !synthSupply.IsZero() && spec.seedAsset.MulUint64(2).LTE(synthSupply) {
			burned, err := m.mgr.Keeper().BurnEntireSynthSupply(ctx, synthAsset)
			if err != nil {
				return fmt.Errorf("adr028 seed: fail to burn synth supply for %s: %w", spec.asset, err)
			}
			ctx.Logger().Info("adr028 seed: burned residual synth supply",
				"asset", spec.asset, "synth_burned", burned)
		}

		pool.BalanceAsset = spec.seedAsset
		pool.BalanceRune = spec.seedRune
		if err := m.mgr.Keeper().SetPool(ctx, pool); err != nil {
			return fmt.Errorf("adr028 seed: fail to set pool %s: %w", spec.asset, err)
		}

		runeAmt := common.SafeSub(oldRune, spec.seedRune)
		runeAdd := spec.seedRune.GT(oldRune)
		if runeAdd {
			runeAmt = common.SafeSub(spec.seedRune, oldRune)
		}
		assetAmt := common.SafeSub(oldAsset, spec.seedAsset)
		assetAdd := spec.seedAsset.GT(oldAsset)
		if assetAdd {
			assetAmt = common.SafeSub(spec.seedAsset, oldAsset)
		}
		emitPoolBalanceChangedEvent(ctx, NewPoolMod(spec.asset, runeAmt, runeAdd, assetAmt, assetAdd), "adr028_seed", m.mgr)

		ctx.Logger().Info("adr028 seed: pool re-seeded to $5k per side",
			"asset", spec.asset,
			"old_balance_asset", oldAsset, "new_balance_asset", pool.BalanceAsset,
			"old_balance_rune", oldRune, "new_balance_rune", pool.BalanceRune)
	}

	return nil
}

// adr028SeedSpec re-seeds one pool to a fixed $5k-per-side size instead of
// writing it down to live backing.
type adr028SeedSpec struct {
	asset     common.Asset
	seedAsset cosmos.Uint // pool.BalanceAsset is set to exactly this ($5k of asset)
	seedRune  cosmos.Uint // pool.BalanceRune is set to exactly this ($5k of RUNE)
}

// mainnetADR028SeedSpecs17to18 returns the $5k-per-side seed table for the four
// pools that are re-seeded rather than written down to live backing.
//
// SET SELECTION — three of the four are the complete empty-vault set, found by
// scanning all 30 deficit pools at the snapshot height for
//
//	backing = asgard − trade.depth − secured.depth − stream_inflight < 0
//
// i.e. the asgard vault cannot cover even the 1:1 trade+secured redemption
// claims. That returns exactly BSC.USDC, BSC.USDT and BASE.ETH — no other pool
// qualifies (surplus pools cannot by definition; THOR.TCY/RUJI are bank-backed).
// ETH.THOR does NOT meet this criterion — its vault holds 4,168,967 THOR, 748%
// of its claims, and its backing is positive — it is included by explicit
// decision as the exploit-origin token, not by the scan.
//
// SNAPSHOT @ THOR height 26,967,482, priced at that height: RUNE $0.405553,
// ETH $1,802.40 (ETH.ETH pool spot), THOR $0.033671 (ETH.THOR pool spot),
// USDC/USDT $1.00. Unlike the writedown deltas, these seed amounts are absolute
// $-denominated values, so they MUST be re-derived at the actual upgrade height
// (RUNE and asset prices drift) and hardcoded before release. They are hardcoded
// rather than computed from live pool state on purpose — a price lookup inside a
// migration is non-deterministic across nodes and would fork the chain.
//
// NOTE: the seeded pools quote against vaults that are physically drained
// (BSC.USDC holds 7.61 USDC, BSC.USDT 1.51, BASE.ETH 0.04). The seed is balanced
// $5k/$5k so it quotes at market price; real backing and any claim settlement
// are handled by a separate manual step after the upgrade.
func mainnetADR028SeedSpecs17to18() ([]adr028SeedSpec, error) {
	// $5k RUNE at the snapshot price, identical for all four pools.
	const seedRune1e8 = 1232884481190

	// {asset, $5k of asset (1e8)}.
	rows := []struct {
		asset     string
		seedAsset uint64
	}{
		{"BASE.ETH", 277408024},
		{"BSC.USDC-0X8AC76A51CC950D9822D68B83FE1AD97B32CD580D", 500000000000},
		{"BSC.USDT-0X55D398326F99059FF775485246999027B3197955", 500000000000},
		{"ETH.THOR-0XA5F2211B9B8170F694421F2046281775E8468044", 14849664450329},
	}

	specs := make([]adr028SeedSpec, 0, len(rows))
	for _, r := range rows {
		asset, err := common.NewAsset(r.asset)
		if err != nil {
			return nil, fmt.Errorf("adr028 seed: parse asset %s: %w", r.asset, err)
		}
		specs = append(specs, adr028SeedSpec{
			asset:     asset,
			seedAsset: cosmos.NewUint(r.seedAsset),
			seedRune:  cosmos.NewUint(seedRune1e8),
		})
	}

	return specs, nil
}

// adr028FreeBacking returns the genuinely unencumbered vault float for asset
// after the writedown: vault coins − post-writedown pool.BA − trade depth −
// secured depth. Trade and secured balances are backed by the same Asgard
// vaults and must be excluded so savers are not settled against collateral
// already claimed by other account types.
func (m *Migrator) adr028FreeBacking(ctx cosmos.Context, asset common.Asset, target cosmos.Uint) (cosmos.Uint, error) {
	vaultCoins := cosmos.ZeroUint()
	for _, status := range []VaultStatus{ActiveVault, RetiringVault} {
		vaults, err := m.mgr.Keeper().GetAsgardVaultsByStatus(ctx, status)
		if err != nil {
			return cosmos.ZeroUint(), fmt.Errorf("fail to get %s vaults for %s: %w", status, asset, err)
		}
		for _, v := range vaults {
			vaultCoins = vaultCoins.Add(v.GetCoin(asset).Amount)
		}
	}

	claims := target
	tu, err := m.mgr.Keeper().GetTradeUnit(ctx, asset.GetTradeAsset())
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("fail to get trade unit for %s: %w", asset, err)
	}
	claims = claims.Add(tu.Depth)
	sa, err := m.mgr.Keeper().GetSecuredAsset(ctx, asset.GetSecuredAsset())
	if err != nil {
		return cosmos.ZeroUint(), fmt.Errorf("fail to get secured asset for %s: %w", asset, err)
	}
	claims = claims.Add(sa.Depth)

	return common.SafeSub(vaultCoins, claims), nil
}

// adr028SaversHaircut settles saver positions at the pool's real free backing
// instead of burning their entire synth supply. Mirrors the Migrate15to16
// step-3 savers haircut pattern: iterates saver LPs, reduces units
// proportionally, reduces the synth pool depth, then burns the excess synth
// from Asgard.
func (m *Migrator) adr028SaversHaircut(
	ctx cosmos.Context,
	asset, synthAsset common.Asset,
	synthSupply, freeBacking cosmos.Uint,
) error {
	saverPool, err := m.mgr.Keeper().GetPool(ctx, synthAsset)
	if err != nil {
		return fmt.Errorf("fail to get saver pool %s: %w", synthAsset, err)
	}

	// Determine how much of the savers' depth to keep (capped at freeBacking).
	saverKeep := freeBacking
	if saverPool.BalanceAsset.LT(freeBacking) {
		saverKeep = saverPool.BalanceAsset
	}
	saverBurnAmt := common.SafeSub(saverPool.BalanceAsset, saverKeep)

	var saverCount int
	if !saverBurnAmt.IsZero() {
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
		saverCount = len(saverLPs)

		poolAssetPre := saverPool.BalanceAsset
		sumLPDeductions := cosmos.ZeroUint()
		for _, lp := range saverLPs {
			deduct := lp.Units.Mul(saverBurnAmt).Quo(poolAssetPre)
			lp.Units = common.SafeSub(lp.Units, deduct)
			m.mgr.Keeper().SetLiquidityProvider(ctx, lp)
			sumLPDeductions = sumLPDeductions.Add(deduct)
		}
		saverPool.LPUnits = common.SafeSub(saverPool.LPUnits, sumLPDeductions)
		saverPool.BalanceAsset = common.SafeSub(saverPool.BalanceAsset, saverBurnAmt)
		if err := m.mgr.Keeper().SetPool(ctx, saverPool); err != nil {
			return fmt.Errorf("fail to save saver pool %s: %w", synthAsset, err)
		}
	}

	// Burn synths in two passes so Asgard retains exactly saverKeep coins
	// (matching the post-haircut saverPool.BalanceAsset). Reserve-held synths
	// (ThorFi-era residuals) are burned separately. User-held synths survive
	// — they're individual bank balances we can't burn.
	totalBurned := cosmos.ZeroUint()

	// Pass 1: Asgard — burn everything above saverKeep.
	asgardSynthBal := m.mgr.Keeper().GetBalanceOfModule(ctx, AsgardName, synthAsset.Native())
	asgardBurn := common.SafeSub(asgardSynthBal, saverKeep)
	if !asgardBurn.IsZero() {
		coin := common.NewCoin(synthAsset, asgardBurn)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, AsgardName, ModuleName, common.NewCoins(coin)); err != nil {
			return fmt.Errorf("fail to move synths from Asgard for %s: %w", synthAsset, err)
		}
		if err := m.mgr.Keeper().BurnFromModule(ctx, ModuleName, coin); err != nil {
			return fmt.Errorf("fail to burn synths from Asgard for %s: %w", synthAsset, err)
		}
		totalBurned = totalBurned.Add(asgardBurn)
	}

	// Pass 2: Reserve — burn any synths held by the Reserve module.
	reserveSynthBal := m.mgr.Keeper().GetBalanceOfModule(ctx, ReserveName, synthAsset.Native())
	if !reserveSynthBal.IsZero() {
		coin := common.NewCoin(synthAsset, reserveSynthBal)
		if err := m.mgr.Keeper().SendFromModuleToModule(ctx, ReserveName, ModuleName, common.NewCoins(coin)); err != nil {
			return fmt.Errorf("fail to move synths from Reserve for %s: %w", synthAsset, err)
		}
		if err := m.mgr.Keeper().BurnFromModule(ctx, ModuleName, coin); err != nil {
			return fmt.Errorf("fail to burn synths from Reserve for %s: %w", synthAsset, err)
		}
		totalBurned = totalBurned.Add(reserveSynthBal)
	}

	if !totalBurned.IsZero() {
		mod := NewPoolMod(synthAsset, cosmos.ZeroUint(), false, saverBurnAmt, false)
		emitPoolBalanceChangedEvent(ctx, mod, "adr028_savers_haircut", m.mgr)
		burnEvt := NewEventMintBurn(BurnSupplyType, synthAsset.Native(), totalBurned, "adr028_savers_haircut")
		if err := m.mgr.EventMgr().EmitEvent(ctx, burnEvt); err != nil {
			ctx.Logger().Error("adr028 savers haircut: fail to emit burn event", "error", err, "asset", synthAsset)
		}
	}

	userHeld := common.SafeSub(synthSupply, asgardSynthBal.Add(reserveSynthBal))
	ctx.Logger().Info("adr028 writedown: UNDERBACKED — savers settled at free backing",
		"asset", asset, "saver_count", saverCount, "saver_burn_amt", saverBurnAmt,
		"asgard_burn", asgardBurn, "reserve_burn", reserveSynthBal,
		"user_held_surviving", userHeld, "free_backing", freeBacking)
	return nil
}

// adr028ZeroSavers zeros all saver LP records and the saver pool for an
// UNBACKED pool whose synth supply was just burned. Without this, the saver
// pool and LP units would reference a synth that no longer exists.
func (m *Migrator) adr028ZeroSavers(ctx cosmos.Context, synthAsset common.Asset) error {
	saverPool, err := m.mgr.Keeper().GetPool(ctx, synthAsset)
	if err != nil {
		return fmt.Errorf("fail to get saver pool %s: %w", synthAsset, err)
	}
	if saverPool.BalanceAsset.IsZero() && saverPool.LPUnits.IsZero() {
		return nil
	}

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

	oldBA := saverPool.BalanceAsset
	oldLPUnits := saverPool.LPUnits

	for _, lp := range saverLPs {
		lp.Units = cosmos.ZeroUint()
		m.mgr.Keeper().SetLiquidityProvider(ctx, lp)
	}

	saverPool.LPUnits = cosmos.ZeroUint()
	saverPool.BalanceAsset = cosmos.ZeroUint()
	if err := m.mgr.Keeper().SetPool(ctx, saverPool); err != nil {
		return fmt.Errorf("fail to save zeroed saver pool %s: %w", synthAsset, err)
	}

	ctx.Logger().Info("adr028 writedown: zeroed saver records for UNBACKED pool",
		"asset", synthAsset, "saver_count", len(saverLPs),
		"old_balance_asset", oldBA, "old_lp_units", oldLPUnits)
	return nil
}
