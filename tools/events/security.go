package main

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/tools/thorscan"
)

////////////////////////////////////////////////////////////////////////////////////////
// ScanSecurity
////////////////////////////////////////////////////////////////////////////////////////

func ScanSecurity(block *thorscan.BlockResponse) {
	SecurityEvents(block)
	FailedTransactions(block)
	ErrataTransactions(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// SecurityEvents
////////////////////////////////////////////////////////////////////////////////////////

func SecurityEvents(block *thorscan.BlockResponse) {
	// transaction security events
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] != "security" {
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
		if event["type"] != "security" {
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
// FailedTransactions
////////////////////////////////////////////////////////////////////////////////////////

func FailedTransactions(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		// skip successful transactions and failed gas or sequence
		switch *tx.Result.Code {
		case 0: // success
			continue
		case 5: // insufficient funds
			continue
		case 32: // bad sequence
			continue
		}

		// alert fields
		fields := NewOrderedMap()
		fields.Set("Code", fmt.Sprintf("%d", *tx.Result.Code))
		fields.Set(
			"Transaction",
			fmt.Sprintf("%s/cosmos/tx/v1beta1/txs/%s", config.Links.Thornode, tx.BlockTx.Hash),
		)

		// determine if the transaction failed to decode
		if tx.Tx == nil {
			fields.Set("Failed Decode", "true")
		}
		if tx.Result.Codespace != nil {
			fields.Set("Codespace", fmt.Sprintf("`%s`", *tx.Result.Codespace))
		}
		if tx.Result.Log != nil {
			fields.Set("Log", fmt.Sprintf("`%s`", *tx.Result.Log))
		}

		// notify failed transaction
		title := fmt.Sprintf("`[%d]` Failed Transaction", block.Header.Height)
		Notify(config.Notifications.Security, title, nil, false, fields)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// ErrataTransactions
////////////////////////////////////////////////////////////////////////////////////////

func ErrataTransactions(block *thorscan.BlockResponse) {}
