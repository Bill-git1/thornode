package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/tools/thorscan"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Scan Loans
////////////////////////////////////////////////////////////////////////////////////////

func ScanLoans(block *thorscan.BlockResponse) {
	LoanOpen(block)
	LoanRepayment(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// Loan Open
////////////////////////////////////////////////////////////////////////////////////////

func LoanOpen(block *thorscan.BlockResponse) {
	for _, event := range block.EndBlockEvents {
		if event["type"] != types.LoanOpenEventType {
			continue
		}

		title := fmt.Sprintf("`[%d]` Loan Open", block.Header.Height)
		fields := NewOrderedMap()
		lines := []string{}

		// extract event data
		collateralAmount := cosmos.NewUintFromString(event["collateral_deposited"])
		collateralAsset, err := common.NewAsset(event["collateral_asset"])
		if err != nil {
			log.Panic().Err(err).Msg("failed to parse collateral asset")
		}
		debtInTOR := cosmos.NewUintFromString(event["debt_issued"])

		// collateral
		collateralCoin := common.NewCoin(collateralAsset, collateralAmount)
		collateralUSDValue := USDValue(block.Header.Height, collateralCoin)
		collateralExternalUSDValue := ExternalUSDValue(collateralCoin)
		fields.Set("Collateral", fmt.Sprintf(
			"%s (%s) _(External: %s)_",
			collateralCoin, FormatUSD(collateralUSDValue), FormatUSD(collateralExternalUSDValue),
		))
		if uint64(collateralUSDValue) > config.Styles.USDPerMoneyBag {
			lines = append(lines, Moneybags(uint64(collateralUSDValue)))
		}

		// collateralization ratio
		collateralizationRatio := cosmos.NewUintFromString(event["collateralization_ratio"])
		crStr := fmt.Sprintf(
			"%.2fx (%.2fx computed)",
			float64(collateralizationRatio.Uint64())/10000,
			float64(collateralUSDValue*1e8)/float64(debtInTOR.Uint64()),
		)
		fields.Set("CR", crStr)

		// debt
		debtStr := FormatUSD(float64(debtInTOR.Uint64()) / common.One)
		if debtInTOR.GT(cosmos.NewUint(uint64(collateralUSDValue * common.One))) {
			debtStr = fmt.Sprintf(":rotating_light: %s :rotating_light:", debtStr)
		}
		fields.Set("Debt", debtStr)

		// miscellaneous
		fields.Set("Target Asset", event["target_asset"])
		fields.Set("Owner", event["owner"])

		// collect swap fees from midgard
		setMidgardLoanFees(block.Header.Height, fields, event["tx_id"])

		// links
		links := []string{
			fmt.Sprintf("[Owner](%s/address/%s?tab=loans)", config.Links.Explorer, event["owner"]),
			fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, event["tx_id"]),
			"[Lending Dashboard](https://dashboards.ninerealms.com/#lending)",
		}
		fields.Set("Links", strings.Join(links, " | "))

		// notify
		Notify(config.Notifications.Lending, title, lines, false, fields)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Loan Repayment
////////////////////////////////////////////////////////////////////////////////////////

func LoanRepayment(block *thorscan.BlockResponse) {
	for _, event := range block.EndBlockEvents {
		if event["type"] != types.LoanRepaymentEventType {
			continue
		}

		title := fmt.Sprintf("`[%d]` Loan Repayment", block.Header.Height)
		fields := NewOrderedMap()
		lines := []string{}

		// extract event data
		collateralWithdrawn := cosmos.NewUintFromString(event["collateral_withdrawn"])
		collateralAsset, err := common.NewAsset(event["collateral_asset"])
		if err != nil {
			log.Panic().Err(err).Msg("failed to parse collateral asset")
		}
		debtRepaid := cosmos.NewUintFromString(event["debt_repaid"])
		fields.Set("Owner", event["owner"])
		fields.Set("Debt Repaid", FormatUSD(float64(debtRepaid.Uint64())/common.One))

		// withdrawn
		withdrawnCoin := common.NewCoin(collateralAsset, collateralWithdrawn)
		withdrawnStr := fmt.Sprintf("%f %s", float64(collateralWithdrawn.Uint64())/common.One, collateralAsset)
		if !collateralWithdrawn.IsZero() {
			usdValue := USDValue(block.Header.Height, withdrawnCoin)
			withdrawnStr += fmt.Sprintf(" (%s)", FormatUSD(usdValue))
			withdrawnStr += fmt.Sprintf(" _(External: %s)_", FormatUSD(ExternalUSDValue(withdrawnCoin)))
			if uint64(usdValue) > config.Styles.USDPerMoneyBag {
				lines = append(lines, Moneybags(uint64(usdValue)))
			}
			title += fmt.Sprintf(" %s", EmojiMoneyWithWings)
		}
		fields.Set("Collateral Withdrawn", withdrawnStr)

		// loan status
		borrower := openapi.Borrower{}
		url := fmt.Sprintf("%s/thorchain/pool/%s/borrower/%s", config.Endpoints.Thornode, collateralAsset.String(), event["owner"])
		err = RetryGet(url, &borrower)
		if err != nil {
			log.Panic().Str("borrower", event["owner"]).Err(err).Msg("failed to get borrower")
		}
		borrowerCollateralDeposited := cosmos.NewUintFromString(borrower.CollateralDeposited)
		borrowerCollateralWithdrawn := cosmos.NewUintFromString(borrower.CollateralWithdrawn)
		borrowerCollateralCurrent := cosmos.NewUintFromString(borrower.CollateralCurrent)
		borrowerDebtIssued := cosmos.NewUintFromString(borrower.DebtIssued)
		borrowerDebtRepaid := cosmos.NewUintFromString(borrower.DebtRepaid)
		borrowerDebtCurrent := cosmos.NewUintFromString(borrower.DebtCurrent)
		blockAge := borrower.LastRepayHeight - borrower.LastOpenHeight

		// borrower collateral summary
		collateralDepositedStr := fmt.Sprintf(
			"%f %s (%s)",
			float64(borrowerCollateralDeposited.Uint64())/common.One,
			collateralAsset,
			USDValueString(block.Header.Height, common.NewCoin(collateralAsset, borrowerCollateralDeposited)),
		)
		collateralWithdrawnStr := fmt.Sprintf(
			"%f %s (%s)",
			float64(borrowerCollateralWithdrawn.Uint64())/common.One,
			collateralAsset,
			USDValueString(block.Header.Height, common.NewCoin(collateralAsset, borrowerCollateralWithdrawn)),
		)
		collateralRemainingStr := fmt.Sprintf(
			"%f %s (%s)",
			float64(borrowerCollateralCurrent.Uint64())/common.One,
			collateralAsset,
			USDValueString(block.Header.Height, common.NewCoin(collateralAsset, borrowerCollateralCurrent)),
		)
		fields.Set("Collateral", fmt.Sprintf(
			"%s deposited | %s withdrawn | %s remaining",
			collateralDepositedStr, collateralWithdrawnStr, collateralRemainingStr,
		))
		fields.Set("Total Debt", fmt.Sprintf(
			"%s issued | %s repaid | %s remaining",
			FormatUSD(float64(borrowerDebtIssued.Uint64())/common.One),
			FormatUSD(float64(borrowerDebtRepaid.Uint64())/common.One),
			FormatUSD(float64(borrowerDebtCurrent.Uint64())/common.One),
		))
		ageDuration := time.Duration(blockAge*common.THORChain.ApproximateBlockMilliseconds()) * time.Millisecond
		fields.Set("Age", fmt.Sprintf("%d blocks (%s)", blockAge, FormatDuration(ageDuration)))

		// collect swap fees from midgard
		setMidgardLoanFees(block.Header.Height, fields, event["tx_id"])

		// links
		links := []string{
			fmt.Sprintf("[Owner](%s/address/%s?tab=loans)", config.Links.Explorer, event["owner"]),
			fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, event["tx_id"]),
			"[Lending Dashboard](https://dashboards.ninerealms.com/#lending)",
		}
		fields.Set("Links", strings.Join(links, " | "))

		// notify
		Notify(config.Notifications.Lending, title, lines, false, fields)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

func setMidgardLoanFees(height int64, fields *OrderedMap, txid string) {
	// sleep to reduce race before collecting fees from midgard
	time.Sleep(time.Duration(common.THORChain.ApproximateBlockMilliseconds()) * time.Millisecond)

	// get actions
	actions := MidgardActionsResponse{}
	url := fmt.Sprintf("%s/v2/actions?txid=%s", config.Endpoints.Midgard, txid)
	err := RetryGet(url, &actions)
	if err != nil {
		log.Panic().
			Err(err).
			Str("txid", txid).
			Msg("failed to get midgard actions")
	}

	// extract fees
	outboundFees := []string{}
	liquidityFee := cosmos.ZeroUint()
	for _, action := range actions.Actions {
		if action.Type != "swap" {
			continue
		}
		liquidityFee = liquidityFee.Add(cosmos.NewUintFromString(action.Metadata.Swap.LiquidityFee))
		for _, fee := range action.Metadata.Swap.NetworkFees {
			outboundFees = append(outboundFees, fmt.Sprintf(
				"%f %s (%s)",
				float64(fee.Amount.Uint64())/common.One,
				fee.Asset,
				USDValueString(height, common.NewCoin(fee.Asset, fee.Amount)),
			))
		}
	}

	// set fields
	fields.Set("Liquidity Fee", fmt.Sprintf(
		"%f RUNE (%s)",
		float64(liquidityFee.Uint64())/common.One,
		FormatUSD(float64(liquidityFee.Uint64())/common.One),
	))
	fields.Set("Outbound Fees", strings.Join(outboundFees, " | "))
}
