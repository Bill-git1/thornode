package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	ctypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/openapi/gen"
	"gitlab.com/thorchain/thornode/tools/thorscan"
	"gitlab.com/thorchain/thornode/x/thorchain"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Config
////////////////////////////////////////////////////////////////////////////////////////

var reExternalMigration = regexp.MustCompile(`MIGRATE:(\d+)`)

////////////////////////////////////////////////////////////////////////////////////////
// ScanActivity
////////////////////////////////////////////////////////////////////////////////////////

func ScanActivity(block *thorscan.BlockResponse) {
	LargeUnconfirmedInbounds(block)
	LargeStreamingSwaps(block)
	LargeScheduledOutbounds(block)
	LargeTransfers(block)
	InactiveVaultInbounds(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// LargeUnconfirmedInbounds
////////////////////////////////////////////////////////////////////////////////////////

func LargeUnconfirmedInbounds(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		// skip failed transactions
		if *tx.Result.Code != 0 {
			continue
		}

		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}

		for _, msg := range tx.Tx.GetMsgs() {
			// skip anything other than observed transactions
			msgObservedTxIn, ok := msg.(*thorchain.MsgObservedTxIn)
			if !ok {
				continue
			}

			// the observed tx in can have multiple transactions
			for _, tx := range msgObservedTxIn.Txs {
				// since this is checked often, only update cached price every 10 blocks
				priceHeight := block.Header.Height / 10 * 10

				// skip if below usd threshold
				usdValue := USDValue(priceHeight, tx.Tx.Coins[0])
				if uint64(usdValue) < config.Thresholds.USDValue {
					continue
				}

				// skip if under 2 minutes until confirmation
				confirmBlocks := tx.FinaliseHeight - tx.BlockHeight
				blockMs := tx.Tx.Chain.GetGasAsset().Chain.ApproximateBlockMilliseconds()
				confirmDuration := time.Duration(confirmBlocks*blockMs) * time.Millisecond
				if confirmDuration < time.Minute*2 {
					continue
				}

				// skip if previously seen
				seen := false
				seenKey := fmt.Sprintf("seen-large-unconfirmed-inbound/%s", tx.Tx.ID.String())
				err := Load(seenKey, &seen)
				if err != nil {
					log.Debug().Err(err).Msg("unable to load seen large unconfirmed inbound")
				}
				if seen {
					continue
				}

				// mark this inbound as seen
				err = Store(seenKey, true)
				if err != nil {
					log.Fatal().Err(err).Msg("unable to store seen large unconfirmed inbound")
				}

				// build notification
				title := fmt.Sprintf("`[%d]` Large Unconfirmed Inbound", block.Header.Height)
				fields := NewOrderedMap()
				fields.Set("Chain", tx.Tx.Chain.String())
				fields.Set("Hash", tx.Tx.ID.String())
				fields.Set("Memo", fmt.Sprintf("`%s`", tx.Tx.Memo))
				fields.Set("Confirmation Time", FormatDuration(confirmDuration))
				fields.Set("Amount", fmt.Sprintf(
					"%f %s (%s)",
					float64(tx.Tx.Coins[0].Amount.Uint64())/common.One,
					tx.Tx.Coins[0].Asset,
					USDValueString(priceHeight, tx.Tx.Coins[0]),
				),
				)

				// notify
				Notify(config.Notifications.Activity, title, nil, false, fields)

				// notify security if over security threshold
				if usdValue > float64(config.Thresholds.Security.USDValue) {
					Notify(config.Notifications.Security, title, nil, false, fields)
				}
			}
		}

	}
}

////////////////////////////////////////////////////////////////////////////////////////
// LargeStreamingSwap
////////////////////////////////////////////////////////////////////////////////////////

func LargeStreamingSwaps(block *thorscan.BlockResponse) {
	for _, event := range block.EndBlockEvents {
		if event["type"] != "swap" {
			continue
		}

		// only alert on the first sub swap
		if event["streaming_swap_count"] != "1" {
			continue
		}

		// only alert when there are multiple swaps
		if event["streaming_swap_quantity"] == "1" {
			continue
		}

		// parse the quantity
		quantity, err := strconv.Atoi(event["streaming_swap_quantity"])
		if err != nil {
			log.Fatal().Err(err).Msg("unable to parse streaming swap quantity")
		}

		// check first the approximate USD value before fetching the inbound
		coin, err := common.ParseCoin(event["coin"])
		if err != nil {
			log.Fatal().Str("coin", event["coin"]).Err(err).Msg("unable to parse streaming swap coin")
		}
		usdValue := USDValue(block.Header.Height, coin)
		if uint64(usdValue*float64(quantity)) < config.Thresholds.USDValue {
			continue
		}

		// skip previously seen streaming swaps
		seen := false
		seenKey := fmt.Sprintf("seen-large-streaming-swap/%s", event["id"])
		err = Load(seenKey, &seen)
		if err != nil {
			log.Debug().Err(err).Msg("unable to load seen large streaming swap")
		}
		if seen {
			continue
		}

		// get the tx for the precise value
		tx := struct {
			ObservedTx openapi.ObservedTx `json:"observed_tx"`
		}{}
		url := fmt.Sprintf("thorchain/tx/%s", event["id"])
		err = ThornodeCachedRetryGet(url, block.Header.Height, &tx)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get tx")
		}

		// verify precise amount
		coinStr := fmt.Sprintf("%s %s", tx.ObservedTx.Tx.Coins[0].Amount, tx.ObservedTx.Tx.Coins[0].Asset)
		coin, err = common.ParseCoin(coinStr)
		if err != nil {
			log.Fatal().Str("coin", coinStr).Err(err).Msg("unable to parse coin")
		}
		usdValue = USDValue(block.Header.Height, coin)
		if uint64(usdValue) < config.Thresholds.USDValue {
			continue
		}

		// mark this swap as seen
		err = Store(seenKey, true)
		if err != nil {
			log.Fatal().Err(err).Msg("unable to store seen large streaming swap")
		}

		// build notification
		title := fmt.Sprintf("`[%d]` Streaming Swap", block.Header.Height)
		lines := []string{}
		if usdValue > float64(config.Styles.USDPerMoneyBag) {
			lines = append(lines, Moneybags(int(usdValue)))
		}
		fields := NewOrderedMap()
		fields.Set("Chain", event["chain"])
		fields.Set("Hash", event["id"])
		fields.Set("Amount", fmt.Sprintf(
			"%f %s (%s)",
			float64(coin.Amount.Uint64())/common.One,
			coin.Asset,
			USDValueString(block.Header.Height, coin),
		))
		fields.Set("Memo", fmt.Sprintf("`%s`", event["memo"]))
		fields.Set("Quantity", fmt.Sprintf("%s swaps", event["streaming_swap_quantity"]))

		// attempt adding interval and expected time
		args := strings.Split(event["memo"], ":")
		if len(args) > 3 {
			limitParams := strings.Split(args[3], "/")
			var interval int
			if len(limitParams) > 1 {
				interval, err = strconv.Atoi(limitParams[1])
				if err != nil {
					log.Error().Err(err).Msg("unable to parse streaming swap interval")
				}
			}
			if quantity > 0 && interval > 0 {
				ms := quantity * interval * int(common.THORChain.ApproximateBlockMilliseconds())
				swapDuration := time.Duration(ms) * time.Millisecond
				fields.Set("Interval", fmt.Sprintf("%d blocks", interval))
				fields.Set("Expected Swap Time", FormatDuration(swapDuration))
			}
		}

		links := []string{
			fmt.Sprintf("[Tracker](%s/%s)", config.Links.Track, event["id"]),
			fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, event["id"]),
		}
		fields.Set("Links", strings.Join(links, " | "))

		// notify
		Notify(config.Notifications.Activity, title, lines, false, fields)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// LargeScheduledOutbounds
////////////////////////////////////////////////////////////////////////////////////////

func LargeScheduledOutbounds(block *thorscan.BlockResponse) {}

////////////////////////////////////////////////////////////////////////////////////////
// LargeTransfers
////////////////////////////////////////////////////////////////////////////////////////

func LargeTransfers(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		// skip failed transactions
		if *tx.Result.Code != 0 {
			continue
		}

		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}

		for _, msg := range tx.Tx.GetMsgs() {
			// skip anything other than send
			msgSend, ok := msg.(*thorchain.MsgSend)
			if !ok {
				continue
			}

			// find rune value
			amount := uint64(0)
			for _, coin := range msgSend.Amount {
				if coin.Denom == "rune" {
					amount = coin.Amount.Uint64()
				}
			}

			// skip small transfers
			if amount < config.Thresholds.RuneValue*common.One {
				continue
			}

			fields := NewOrderedMap()

			// determine if this is an external migration
			txWithMemo, ok := tx.Tx.(ctypes.TxWithMemo)
			if !ok {
				log.Fatal().Msg("failed to cast tx to TxWithMemo")
			}
			matches := reExternalMigration.FindStringSubmatch(txWithMemo.GetMemo())
			if len(matches) > 0 {
				title := fmt.Sprintf(
					"`[%d]` External Migration `%s` (%sᚱ)",
					block.Header.Height, txWithMemo.GetMemo(), FormatLocale(amount/common.One),
				)
				fields.Set(
					"Links",
					fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, tx.Hash),
				)
				Notify(config.Notifications.Info, title, nil, false, fields)
				continue
			}

			// otherwise this is just a large transfer
			title := fmt.Sprintf(
				"`[%d]` Large Transfer >> %sᚱ (%s)",
				block.Header.Height,
				FormatLocale(amount/common.One),
				USDValueString(block.Header.Height, common.NewCoin(common.RuneAsset(), cosmos.NewUint(amount))),
			)
			fromAddr := config.LabeledAddresses[msgSend.FromAddress.String()]
			if fromAddr == "" {
				fromAddr = msgSend.FromAddress.String()
			}
			toAddr := config.LabeledAddresses[msgSend.ToAddress.String()]
			if toAddr == "" {
				toAddr = msgSend.ToAddress.String()
			}
			links := []string{
				fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, tx.BlockTx.Hash),
				fmt.Sprintf("[%s](%s/address/%s)", fromAddr, config.Links.Explorer, msgSend.FromAddress.String()),
				fmt.Sprintf("[%s](%s/address/%s)", toAddr, config.Links.Explorer, msgSend.ToAddress.String()),
			}
			fields.Set("Hash", tx.BlockTx.Hash)
			fields.Set("From", fromAddr)
			fields.Set("To", toAddr)
			fields.Set("Links", strings.Join(links, " | "))
			Notify(config.Notifications.Info, title, nil, false, fields)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// InactiveVaultInbounds
////////////////////////////////////////////////////////////////////////////////////////

var (
	activeVaults   map[string]bool
	retiringVaults map[string]bool
	retiringHeight int64
)

func InactiveVaultInbounds(block *thorscan.BlockResponse) {
	// update our active vault set any time there is an active vault event
	update := false
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] == types.VaultStatus_ActiveVault.String() {
				update = true
				break
			}
		}
	}
	if activeVaults == nil || update {
		activeVaults = make(map[string]bool)
		retiringVaults = make(map[string]bool)
		vaults := []openapi.Vault{}
		err := ThornodeCachedRetryGet("thorchain/vaults/asgard", block.Header.Height, &vaults)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get vaults")
		}
		for _, vault := range vaults {
			if vault.Status == types.VaultStatus_ActiveVault.String() {
				activeVaults[*vault.PubKey] = true
			}
			if vault.Status == types.VaultStatus_RetiringVault.String() {
				retiringVaults[*vault.PubKey] = true
			}
		}
		retiringHeight = block.Header.Height
	}

	// check for inactive vault inbounds
	for _, tx := range block.Txs {
		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}

		for _, msg := range tx.Tx.GetMsgs() {
			// skip anything other than observed transactions
			msgObservedTxIn, ok := msg.(*thorchain.MsgObservedTxIn)
			if !ok {
				continue
			}

			// the observed tx in can have multiple transactions
			for _, tx := range msgObservedTxIn.Txs {
				// skip inbounds to active vaults
				if activeVaults[tx.ObservedPubKey.String()] {
					continue
				}

				// skip inbounds to retiring vaults within 2 hours
				if retiringVaults[tx.ObservedPubKey.String()] && block.Header.Height-retiringHeight < 1200 {
					continue
				}

				// skip previously seen inactive inbounds
				seen := false
				seenKey := fmt.Sprintf("seen-inactive-inbound/%s", tx.Tx.ID.String())
				err := Load(seenKey, &seen)
				if err != nil {
					log.Debug().Err(err).Msg("unable to load seen inactive inbound")
				}
				if seen {
					continue
				}

				// mark this inbound as seen
				err = Store(seenKey, true)
				if err != nil {
					log.Fatal().Err(err).Msg("unable to store seen inactive inbound")
				}

				// gather links
				links := []string{
					fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, tx.Tx.ID),
					fmt.Sprintf("[Track](%s/%s)", config.Links.Track, tx.Tx.ID),
				}

				// build notification
				title := fmt.Sprintf("`[%d]` Inbound to Non-Active Vault", block.Header.Height)
				fields := NewOrderedMap()
				fields.Set("Chain", tx.Tx.Chain.String())
				fields.Set("Vault", tx.ObservedPubKey.String())
				fields.Set("Vault Address", tx.Tx.ToAddress.String())
				fields.Set("Memo", fmt.Sprintf("`%s`", tx.Tx.Memo))
				fields.Set("Links", strings.Join(links, " | "))

				// notify
				Notify(config.Notifications.Activity, title, nil, false, fields)
			}
		}
	}
}
