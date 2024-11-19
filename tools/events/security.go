package main

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/v3/common"
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
	Round7Failures(block)
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
			title := fmt.Sprintf("`[%d]` Security Event", block.Header.Height)
			data, err := json.MarshalIndent(event, "", "  ")
			if err != nil {
				log.Error().Err(err).Msg("unable to marshal security event")
			}
			lines := []string{"```" + string(data) + "```"}
			fields := NewOrderedMap()
			fields.Set("Hash", tx.Hash)
			fields.Set(
				"Links",
				fmt.Sprintf("[Explorer](%s/tx/%s)", config.Links.Explorer, tx.BlockTx.Hash),
			)
			Notify(config.Notifications.Security, title, lines, false, fields)
		}
	}

	// block security events
	for _, event := range block.EndBlockEvents {
		if event["type"] != types.SecurityEventType {
			continue
		}

		// notify security event
		title := fmt.Sprintf("`[%d]` Security Event", block.Header.Height)
		data, err := json.MarshalIndent(event, "", "  ")
		if err != nil {
			log.Error().Err(err).Msg("unable to marshal security event")
		}
		lines := []string{"```" + string(data) + "```"}
		Notify(config.Notifications.Security, title, lines, false, nil)
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
			title := fmt.Sprintf("`[%d]` Errata", block.Header.Height)
			fields := NewOrderedMap()
			fields.Set(
				"Links",
				fmt.Sprintf("[Details](%s/thorchain/tx/details/%s)", config.Links.Thornode, event["tx_id"]),
			)

			// notify errata transaction
			Notify(config.Notifications.Security, title, nil, false, fields)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Round 7 Failures
////////////////////////////////////////////////////////////////////////////////////////

func Round7Failures(block *thorscan.BlockResponse) {
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
				if msgKeysignFail.Blame.Round != "SignRound7Message" {
					continue
				}

				// skip seen round 7 failures
				seen := map[string]bool{}
				err := Load("round7", &seen)
				if err != nil {
					log.Error().Err(err).Msg("unable to load round 7 failures")
				}
				if seen[msgKeysignFail.Memo] {
					continue
				}

				// build the notification
				title := fmt.Sprintf("`[%d]` Round 7 Failure", block.Header.Height)
				fields := NewOrderedMap()
				fields.Set("Amount", fmt.Sprintf(
					"%f %s (%s)",
					float64(msgKeysignFail.Coins[0].Amount.Uint64())/common.One,
					msgKeysignFail.Coins[0].Asset,
					USDValueString(block.Header.Height, msgKeysignFail.Coins[0]),
				))
				fields.Set("Memo", msgKeysignFail.Memo)
				fields.Set("Transaction", fmt.Sprintf("%s/cosmos/tx/v1beta1/txs/%s", config.Links.Thornode, tx.Hash))
				Notify(config.Notifications.Security, title, nil, false, fields)

				// save seen round 7 failures
				seen[msgKeysignFail.Memo] = true
				err = Store("round7", seen)
				if err != nil {
					log.Error().Err(err).Msg("unable to save round 7 failures")
				}
			}
		}
	}
}
