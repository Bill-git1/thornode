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
		ctx.Logger().Info("removed bad liquidity provider record",
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
