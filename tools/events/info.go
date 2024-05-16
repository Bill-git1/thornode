package main

import (
	"fmt"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/tools/thorscan"
	"gitlab.com/thorchain/thornode/x/thorchain"
)

////////////////////////////////////////////////////////////////////////////////////////
// ScanInfo
////////////////////////////////////////////////////////////////////////////////////////

func ScanInfo(block *thorscan.BlockResponse) {
	SetMimir(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// SetMimir
////////////////////////////////////////////////////////////////////////////////////////

func SetMimir(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] == "set_mimir" {
				// if the transaction contains a mimir message it is admin
				source := "auto"
				for _, msg := range tx.Tx.GetMsgs() {
					if _, ok := msg.(*thorchain.MsgMimir); ok {
						source = "admin"
					}
				}
				msg := formatMimirMessage(block.Header.Height, source, event["key"], event["value"])
				err := Notify(config.Notifications.Info, "", []string{msg}, false, nil)
				if err != nil {
					log.Fatal().Err(err).Msg("failed to send notification")
				}
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

func formatMimirMessage(height int64, source, key, value string) string {
	// get value at previous height
	mimirs := make(map[string]int64)
	err := ThornodeCachedGet("thorchain/mimir", int(height)-1, &mimirs)
	if err != nil {
		log.Fatal().Int64("height", height-1).Err(err).Msg("failed to get mimirs")
	}

	if previous, ok := mimirs[key]; ok {
		return fmt.Sprintf("`[%d]` **%s**: %d -> %s (%s)", height, key, previous, value, source)
	}
	return fmt.Sprintf("`[%d]` **%s**: %s (%s)", height, key, value, source)
}
