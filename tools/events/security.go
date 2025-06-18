package main

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/tools/events/pkg/config"
	"gitlab.com/thorchain/thornode/v3/tools/events/pkg/notify"
	"gitlab.com/thorchain/thornode/v3/tools/events/pkg/util"
	"gitlab.com/thorchain/thornode/v3/tools/thorscan"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Scan Security
////////////////////////////////////////////////////////////////////////////////////////

func ScanSecurity(block *thorscan.BlockResponse) {
	SecurityEvents(block)
	ErrataTransactions(block)
	LastRoundFailures(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// Security Events
////////////////////////////////////////////////////////////////////////////////////////

func SecurityEvents(block *thorscan.BlockResponse) {
	// transaction security events
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] != types.SecurityEventType {
				continue
			}

			// notify security event
			title := "Security Event"
			data, err := json.MarshalIndent(event, "", "  ")
			if err != nil {
				log.Error().Err(err).Msg("unable to marshal security event")
			}
			lines := []string{"```" + string(data) + "```"}
			fields := util.NewOrderedMap()
			fields.Set("Hash", tx.Hash)
			fields.Set(
				"Links",
				fmt.Sprintf("[Explorer](%s/tx/%s)", config.Get().Links.Explorer, tx.BlockTx.Hash),
			)
			notify.Notify(config.Get().Notifications.Security, title, block.Header.Height, lines, notify.Warning, fields)
		}
	}

	// block security events
	for _, event := range append(block.EndBlockEvents, block.FinalizeBlockEvents...) {
		if event["type"] != types.SecurityEventType {
			continue
		}

		// notify security event
		title := "Security Event"
		data, err := json.MarshalIndent(event, "", "  ")
		if err != nil {
			log.Error().Err(err).Msg("unable to marshal security event")
		}
		lines := []string{"```" + string(data) + "```"}
		notify.Notify(config.Get().Notifications.Security, title, block.Header.Height, lines, notify.Warning, nil)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Errata Transactions
////////////////////////////////////////////////////////////////////////////////////////

func ErrataTransactions(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] != types.ErrataEventType {
				continue
			}

			// build the notification
			title := "Errata Tx"
			fields := util.NewOrderedMap()
			fields.Set(
				"Links",
				fmt.Sprintf("[Details](%s/thorchain/tx/details/%s)", config.Get().Links.Thornode, event["tx_id"]),
			)

			// notify errata transaction
			notify.Notify(config.Get().Notifications.Security, title, block.Header.Height, nil, notify.Warning, fields)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Last Round Failures
////////////////////////////////////////////////////////////////////////////////////////

func LastRoundFailures(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		if tx.Tx == nil { // transaction failed decode
			continue
		}
		for _, msg := range tx.Tx.GetMsgs() {
			if msgKeysignFail, ok := msg.(*thorchain.MsgTssKeysignFail); ok {
				// skip migrate transactions
				if reMemoMigration.MatchString(msgKeysignFail.Memo) {
					continue
				}

				// skip failures except for round 7
				if msgKeysignFail.Blame.Round != "SignRound7Message" && msgKeysignFail.Blame.Round != "EDDSASignRound3Message" {
					continue
				}

				// skip seen round 7 failures
				seen := map[string]bool{}
				err := util.Load("lastRound", &seen)
				if err != nil {
					log.Error().Err(err).Msg("unable to load round 7 failures")
				}
				if seen[msgKeysignFail.Memo] {
					continue
				}

				// build the notification
				title := "Last Round Failure"
				fields := util.NewOrderedMap()
				fields.Set("Amount", fmt.Sprintf(
					"%f %s (%s)",
					float64(msgKeysignFail.Coins[0].Amount.Uint64())/common.One,
					msgKeysignFail.Coins[0].Asset,
					util.USDValueString(block.Header.Height, msgKeysignFail.Coins[0]),
				))
				fields.Set("Memo", msgKeysignFail.Memo)
				fields.Set("Transaction", fmt.Sprintf("%s/cosmos/tx/v1beta1/txs/%s", config.Get().Links.Thornode, tx.Hash))
				notify.Notify(config.Get().Notifications.Security, title, block.Header.Height, nil, notify.Warning, fields)

				// save seen round 7 failures
				seen[msgKeysignFail.Memo] = true
				err = util.Store("lastRound", seen)
				if err != nil {
					log.Error().Err(err).Msg("unable to save last round failures")
				}
			}
		}
	}
}
