package main

import (
	"fmt"
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
	memo "gitlab.com/thorchain/thornode/x/thorchain/memo"
	"gitlab.com/thorchain/thornode/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Scan Activity
////////////////////////////////////////////////////////////////////////////////////////

func ScanActivity(block *thorscan.BlockResponse) {
	LargeUnconfirmedInbounds(block)
	LargeStreamingSwaps(block)
	ScheduledOutbounds(block)
	LargeTransfers(block)
	InactiveVaultInbounds(block)
	NewNode(block)
	Bond(block)
	FailedTransactions(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// Large Unconfirmed Inbounds
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
				// skip migrate inbounds
				if reMemoMigration.MatchString(tx.Tx.Memo) {
					continue
				}

				// skip consolidate inbounds
				if tx.Tx.Memo == "consolidate" {
					continue
				}

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
					log.Panic().Err(err).Msg("unable to store seen large unconfirmed inbound")
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
// Large Streaming Swap
////////////////////////////////////////////////////////////////////////////////////////

func LargeStreamingSwaps(block *thorscan.BlockResponse) {
	for _, event := range block.EndBlockEvents {
		if event["type"] != types.SwapEventType {
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
			log.Panic().Err(err).Msg("unable to parse streaming swap quantity")
		}

		// check first the approximate USD value before fetching the inbound
		coin, err := common.ParseCoin(event["coin"])
		if err != nil {
			log.Panic().Str("coin", event["coin"]).Err(err).Msg("unable to parse streaming swap coin")
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
			log.Panic().Err(err).Msg("failed to get tx")
		}

		// verify precise amount
		coinStr := fmt.Sprintf("%s %s", tx.ObservedTx.Tx.Coins[0].Amount, tx.ObservedTx.Tx.Coins[0].Asset)
		coin, err = common.ParseCoin(coinStr)
		if err != nil {
			log.Panic().Str("coin", coinStr).Err(err).Msg("unable to parse coin")
		}
		usdValue = USDValue(block.Header.Height, coin)
		if uint64(usdValue) < config.Thresholds.USDValue {
			continue
		}

		// mark this swap as seen
		err = Store(seenKey, true)
		if err != nil {
			log.Panic().Err(err).Msg("unable to store seen large streaming swap")
		}

		// build notification
		title := fmt.Sprintf("`[%d]` Streaming Swap", block.Header.Height)
		lines := []string{}
		if uint64(usdValue) > config.Styles.USDPerMoneyBag {
			lines = append(lines, Moneybags(uint64(usdValue)))
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
// Scheduled Outbounds
////////////////////////////////////////////////////////////////////////////////////////

// rescheduledOutbounds alerts on rescheduled outbounds and returns true if rescheduled.
func rescheduledOutbounds(height int64, event map[string]string) bool {
	// skip null in hash
	if event["in_hash"] == common.BlankTxID.String() {
		return false
	}

	// the key must be unique for refunds and multi-output outbounds
	key := fmt.Sprintf(
		"scheduled-outbound/%s-%s-%s-%s",
		event["memo"], event["coin_asset"], event["coin_amount"], event["to_address"],
	)

	// store this as the last seen event on return
	defer func() {
		err := Store(key, event)
		if err != nil {
			log.Panic().
				Err(err).
				Str("key", key).
				Msg("unable to store last seen height")
		}
	}()

	// load the last seen event for this key
	lastSeen := map[string]string{}
	err := Load(key, &lastSeen)
	if err != nil {
		return false
	}

	// build the notification
	title := fmt.Sprintf("`[%d]` Rescheduled Outbound", height)
	fields := NewOrderedMap()
	links := []string{
		fmt.Sprintf("[Explorer](%s/tx/%s)", config.Links.Explorer, event["in_hash"]),
	}
	lines := []string{}

	// get value
	asset, err := common.NewAsset(event["coin_asset"])
	if err != nil {
		log.Panic().
			Err(err).
			Str("asset", event["coin_asset"]).
			Msg("failed to parse asset")
	}
	amount := cosmos.NewUintFromString(event["coin_amount"])
	coin := common.NewCoin(asset, amount)
	usdValue := USDValue(height, coin)
	if uint64(usdValue) > config.Styles.USDPerMoneyBag {
		lines = append(lines, Moneybags(uint64(usdValue)))
	}
	fields.Set("Coin", fmt.Sprintf(
		"%f %s (%s)",
		float64(coin.Amount.Uint64())/common.One, coin.Asset, FormatUSD(usdValue),
	))

	// get the transaction status if this was not a ragnarok outbound
	if !reMemoRagnarok.MatchString(event["memo"]) {
		statusURL := fmt.Sprintf("thorchain/tx/status/%s", event["in_hash"])
		status := openapi.TxStatusResponse{}
		err = ThornodeCachedRetryGet(statusURL, height, &status)
		if err != nil {
			log.Panic().
				Err(err).
				Str("txid", event["in_hash"]).
				Int64("height", height).
				Msg("failed to get transaction status")
		}

		// set age field
		blockAge := status.Stages.OutboundSigned.GetBlocksSinceScheduled()
		ageDuration := time.Duration(blockAge*common.THORChain.ApproximateBlockMilliseconds()) * time.Millisecond
		fields.Set("Age", fmt.Sprintf("%s (%d blocks)", FormatDuration(ageDuration), blockAge))

		// add track link for swaps
		memoParts := strings.Split(*status.Tx.Memo, ":")
		var memoType memo.TxType
		memoType, err = memo.StringToTxType(memoParts[0])
		if err != nil {
			log.Error().Err(err).Str("txid", event["in_hash"]).Msg("failed to parse memo type")
		}
		if memoType == thorchain.TxSwap {
			links = append(links, fmt.Sprintf("[Track](%s/%s)", config.Links.Track, event["in_hash"]))
		}

		// include the inbound memo
		fields.Set("Inbound Memo", fmt.Sprintf("`%s`", *status.Tx.Memo))
	}

	// add the outbound data
	fields.Set("Outbound Memo", fmt.Sprintf("`%s`", event["memo"]))
	vaultStr := fmt.Sprintf(
		"`%s` -> `%s`",
		lastSeen["vault_pub_key"][len(lastSeen["vault_pub_key"])-4:],
		event["vault_pub_key"][len(event["vault_pub_key"])-4:],
	)
	if event["vault_pub_key"] != lastSeen["vault_pub_key"] {
		vaultStr = EmojiRotatingLight + " " + vaultStr + " " + EmojiRotatingLight
	}
	fields.Set("Vault", vaultStr)
	fields.Set("Gas Rate", fmt.Sprintf("%s -> %s", lastSeen["gas_rate"], event["gas_rate"]))
	fields.Set("Max Gas", fmt.Sprintf("%s -> %s", lastSeen["max_gas_amount_0"], event["max_gas_amount_0"]))
	fields.Set("Links", strings.Join(links, " | "))

	// send notifications
	Notify(config.Notifications.Activity, title, lines, false, fields)

	return true
}

// scheduledOutbound is called for scheduled_outbound block and tx events. It assumes
// all events are for multi-output outbounds corresponding to the same inbound.
func scheduledOutbound(height int64, events []map[string]string) {
	// skip migrate outbounds
	if reMemoMigration.MatchString(events[0]["memo"]) {
		return
	}

	// check for reschedule
	rescheduled := rescheduledOutbounds(height, events[0])

	// skip ragnarok transactions
	if reMemoRagnarok.MatchString(events[0]["memo"]) {
		return
	}

	// extract memo and coins
	var coins []common.Coin
	for _, event := range events {
		asset, err := common.NewAsset(event["coin_asset"])
		if err != nil {
			log.Panic().Str("asset", event["coin_asset"]).Err(err).Msg("unable to parse asset")
		}
		amount := cosmos.NewUintFromString(event["coin_amount"])
		coin := common.NewCoin(asset, amount)
		coins = append(coins, coin)
	}

	// skip small outbounds, delta value is lower, but only fires if percent threshold met
	usdValue := 0.0
	for _, coin := range coins {
		usdValue += USDValue(height, coin)
	}
	if uint64(usdValue) < config.Thresholds.USDValue && uint64(usdValue) < config.Thresholds.Delta.USDValue {
		return
	}

	// determine if the outbound value is a security alert
	security := usdValue > float64(config.Thresholds.Security.USDValue)
	tag := security

	// skip rescheduled outbound alerts, unless over the security threshold
	if rescheduled && !security {
		return
	}

	// get the inbound status
	statusURL := fmt.Sprintf("thorchain/tx/status/%s", events[0]["in_hash"])
	status := openapi.TxStatusResponse{}
	err := ThornodeCachedRetryGet(statusURL, height, &status)
	if err != nil {
		log.Panic().
			Err(err).
			Str("txid", events[0]["in_hash"]).
			Int64("height", height).
			Msg("failed to get transaction status")
	}
	memoType := memo.TxUnknown
	if status.Tx != nil {
		memoParts := strings.Split(*status.Tx.Memo, ":")
		memoType, err = memo.StringToTxType(memoParts[0])
		if err != nil {
			log.Panic().Err(err).Msg("failed to parse memo type")
		}
	}

	// build the notification
	title := fmt.Sprintf("`[%d]` Scheduled Outbound", height)
	if len(events) > 1 {
		title = fmt.Sprintf("`[%d]` Scheduled Outbounds (%d)", height, len(events))
		for _, event := range events {
			if reMemoRefund.MatchString(event["memo"]) {
				title += " _(partial fill)_"
				break
			}
		}
	}

	lines := []string{}
	if uint64(usdValue) > config.Styles.USDPerMoneyBag {
		lines = append(lines, Moneybags(uint64(usdValue)))
	}
	fields := NewOrderedMap()
	if status.Tx != nil {
		fields.Set("Inbound Memo", fmt.Sprintf("`%s`", *status.Tx.Memo))
	}

	links := []string{
		fmt.Sprintf("[Explorer](%s/tx/%s)", config.Links.Explorer, events[0]["in_hash"]),
		fmt.Sprintf("[Live Outbounds](%s)", config.Links.Track),
	}

	// add the inbound coins for inbound swap or outbound refund
	if memoType == thorchain.TxSwap || reMemoRefund.MatchString(events[0]["memo"]) {
		inboundCoin := CoinToCommon(status.Tx.Coins[0])
		inboundUSDValue := USDValue(height, inboundCoin)
		fields.Set("Inbound Amount", fmt.Sprintf(
			"%f %s (%s)",
			float64(inboundCoin.Amount.Uint64())/common.One,
			inboundCoin.Asset,
			USDValueString(height, inboundCoin),
		))

		// add the delta
		delta := usdValue - inboundUSDValue
		deltaPercent := float64(delta) / inboundUSDValue * 100
		deltaStr := fmt.Sprintf("%s (%.02f%%)", FormatUSD(delta), deltaPercent)
		if delta > 0 {
			// red triangle if perceived value increased
			deltaStr = EmojiSmallRedTriangle + " " + deltaStr
		}

		// skip if delta below threshold and below the broader usd value threshold
		if deltaPercent < float64(config.Thresholds.Delta.Percent) &&
			uint64(inboundUSDValue) < config.Thresholds.USDValue {
			return
		}

		if deltaPercent > float64(config.Thresholds.Delta.Percent) {
			// rotating light and tag @here if delta
			deltaStr = EmojiRotatingLight + " " + deltaStr + " " + EmojiRotatingLight
			tag = true
		}
		fields.Set("Delta", deltaStr)
	} else if uint64(usdValue) < config.Thresholds.USDValue {
		// skip when no delta and below the broader usd value threshold
		return
	}

	// extra fields for streaming swap alerts
	if memoType == thorchain.TxSwap {
		links = append(links, fmt.Sprintf("[Track](%s/%s)", config.Links.Track, events[0]["in_hash"]))

		// add streaming swap durations
		lastStatus := openapi.TxStatusResponse{}
		err = ThornodeCachedRetryGet(statusURL, height-1, &lastStatus)
		if err != nil {
			log.Error().Err(err).Msg("failed to get last transaction status")
		} else if lastStatus.Stages.SwapStatus != nil &&
			lastStatus.Stages.SwapStatus.Streaming != nil &&
			lastStatus.Stages.SwapStatus.Streaming.Interval > 0 &&
			lastStatus.Stages.SwapStatus.Streaming.Quantity > 0 {

			interval := lastStatus.Stages.SwapStatus.Streaming.Interval
			quantity := lastStatus.Stages.SwapStatus.Streaming.Quantity
			ms := quantity * interval * common.THORChain.ApproximateBlockMilliseconds()
			swapDuration := time.Duration(ms) * time.Millisecond
			fields.Set("Stream Duration", FormatDuration(swapDuration))

			// add the price delta for both swap assets
			inCoin := CoinToCommon(status.Tx.Coins[0])
			outCoin := coins[0]
			beginHeight := height - quantity*interval
			beginInValue := USDValue(beginHeight, inCoin)
			beginOutValue := USDValue(beginHeight, outCoin)
			endInValue := USDValue(height, inCoin)
			endOutValue := USDValue(height, outCoin)
			deltaIn := 1 - beginInValue/endInValue
			deltaOut := 1 - beginOutValue/endOutValue
			key := fmt.Sprintf("Stream Price Shift (%s)", strings.Split(inCoin.Asset.String(), "-")[0])
			fields.Set(key, fmt.Sprintf("%.02f%%", deltaIn*100))
			key = fmt.Sprintf("Stream Price Shift (%s)", strings.Split(outCoin.Asset.String(), "-")[0])
			fields.Set(key, fmt.Sprintf("%.02f%%", deltaOut*100))
		}
	}

	// add the outbound data
	for i, coin := range coins {
		amountField := "Outbound Amount"
		memoField := "Outbound Memo"
		if len(coins) > 1 {
			amountField = fmt.Sprintf("Outbound %d Amount", i+1)
			memoField = fmt.Sprintf("Outbound %d Memo", i+1)
		}
		fields.Set(amountField, fmt.Sprintf(
			"%f %s (%s)",
			float64(coin.Amount.Uint64())/common.One,
			coin.Asset,
			USDValueString(height, coin),
		))
		fields.Set(memoField, fmt.Sprintf("`%s`", events[i]["memo"]))
	}

	// determine the expected delay
	outboundDelay := status.Stages.GetOutboundDelay()
	delayDuration := time.Duration((&outboundDelay).GetRemainingDelaySeconds()) * time.Second
	fields.Set("Expected Delay", FormatDuration(delayDuration))
	fields.Set("Links", strings.Join(links, " | "))

	// send notifications
	Notify(config.Notifications.Activity, title, lines, tag, fields)
	if security {
		Notify(config.Notifications.Security, title, lines, tag, fields)
	}
}

func ScheduledOutbounds(block *thorscan.BlockResponse) {
	events := []map[string]string{}

	// gather block events
	for _, event := range block.EndBlockEvents {
		if event["type"] != types.ScheduledOutboundEventType {
			continue
		}
		events = append(events, event)
	}

	// gather transaction events
	for _, tx := range block.Txs {
		// skip failed decode transactions
		if tx.Tx == nil {
			continue
		}
		for _, event := range tx.Result.Events {
			if event["type"] != types.ScheduledOutboundEventType {
				continue
			}
			events = append(events, event)
		}
	}

	// coalesce scheduled outbounds by inbound hash
	scheduledOutbounds := map[string][]map[string]string{}
	for _, event := range events {
		if _, ok := scheduledOutbounds[event["in_hash"]]; !ok {
			scheduledOutbounds[event["in_hash"]] = []map[string]string{}
		}
		scheduledOutbounds[event["in_hash"]] = append(scheduledOutbounds[event["in_hash"]], event)
	}

	// send notifications for each in hash scheduled outbounds
	for _, events := range scheduledOutbounds {
		scheduledOutbound(block.Header.Height, events)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Large Transfers
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
				log.Panic().Msg("failed to cast tx to TxWithMemo")
			}
			matches := reMemoMigration.FindStringSubmatch(txWithMemo.GetMemo())
			if len(matches) > 0 {
				title := fmt.Sprintf(
					"`[%d]` External Migration `%s` (%sᚱ)",
					block.Header.Height, txWithMemo.GetMemo(), FormatLocale(amount/common.One),
				)
				fields.Set(
					"Links",
					fmt.Sprintf("[Transaction](%s/tx/%s)", config.Links.Explorer, tx.Hash),
				)
				Notify(config.Notifications.Activity, title, nil, false, fields)
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
			Notify(config.Notifications.Activity, title, nil, false, fields)
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Inactive Vault Inbounds
////////////////////////////////////////////////////////////////////////////////////////

type Vaults struct {
	Active   map[string]bool
	Retiring map[string]bool
	Height   int64
}

var vaults *Vaults

func init() {
	_ = Load("vaults", vaults)
}

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

	if vaults == nil || update {
		vaults = &Vaults{
			Active:   make(map[string]bool),
			Retiring: make(map[string]bool),
			Height:   block.Header.Height,
		}
		vaultsRes := []openapi.Vault{}
		err := ThornodeCachedRetryGet("thorchain/vaults/asgard", block.Header.Height, &vaultsRes)
		if err != nil {
			log.Panic().Err(err).Msg("failed to get vaults")
		}
		for _, vault := range vaultsRes {
			if vault.Status == types.VaultStatus_ActiveVault.String() {
				vaults.Active[*vault.PubKey] = true
			}
			if vault.Status == types.VaultStatus_RetiringVault.String() {
				vaults.Retiring[*vault.PubKey] = true
			}
		}
		err = Store("vaults", vaults)
		if err != nil {
			log.Panic().Err(err).Msg("failed to store vaults")
		}
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
				if vaults.Active[tx.ObservedPubKey.String()] {
					continue
				}

				// skip inbounds to retiring vaults within 12 hours
				if vaults.Retiring[tx.ObservedPubKey.String()] &&
					block.Header.Height-vaults.Height < 7200 {
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

				// skip finalized inbounds
				stages := openapi.TxStagesResponse{}
				err = ThornodeCachedRetryGet(fmt.Sprintf("thorchain/tx/stages/%s", tx.Tx.ID), block.Header.Height, &stages)
				if err != nil {
					log.Panic().Err(err).Msg("failed to get tx stages")
				}
				if stages.InboundFinalised != nil && stages.InboundFinalised.Completed {
					continue
				}

				// mark this inbound as seen
				err = Store(seenKey, true)
				if err != nil {
					log.Panic().Err(err).Msg("unable to store seen inactive inbound")
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

////////////////////////////////////////////////////////////////////////////////////////
// New Node
////////////////////////////////////////////////////////////////////////////////////////

func NewNode(block *thorscan.BlockResponse) {
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			if event["type"] != "new_node" {
				continue
			}

			for _, msg := range tx.Tx.GetMsgs() {
				// skip anything other than deposit
				msgDeposit, ok := msg.(*thorchain.MsgDeposit)
				if !ok {
					continue
				}
				amount := uint64(0)
				for _, coin := range msgDeposit.Coins {
					if coin.Asset.Equals(common.RuneAsset()) {
						amount = coin.Amount.Uint64()
					}
				}

				title := fmt.Sprintf("`[%d]` New Node", block.Header.Height)
				fields := NewOrderedMap()
				operator := msgDeposit.Signer.String()
				operator = operator[len(operator)-4:]
				fields.Set("Hash", tx.Hash)
				fields.Set("Operator", fmt.Sprintf("`%s`", operator))
				fields.Set("Node", fmt.Sprintf("`%s`", event["address"][len(event["address"])-4:]))
				fields.Set("Amount", fmt.Sprintf("%sᚱ", FormatLocale(float64(amount)/common.One)))
				Notify(config.Notifications.Activity, title, nil, false, fields)
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Bond
////////////////////////////////////////////////////////////////////////////////////////

func Bond(block *thorscan.BlockResponse) {
txs:
	for _, tx := range block.Txs {
		for _, event := range tx.Result.Events {
			// skip if this was an initial node bond (picked up by new node alert)
			if event["type"] == "new_node" {
				continue txs
			}

			if event["type"] != types.BondEventType {
				continue
			}

			for _, msg := range tx.Tx.GetMsgs() {
				// skip anything other than deposit
				msgDeposit, ok := msg.(*thorchain.MsgDeposit)
				if !ok {
					continue
				}
				amount := uint64(0)
				for _, coin := range msgDeposit.Coins {
					if coin.Asset.Equals(common.RuneAsset()) {
						amount = coin.Amount.Uint64()
					}
				}

				title := fmt.Sprintf("`[%d]` Bond", block.Header.Height)
				fields := NewOrderedMap()
				provider := msgDeposit.Signer.String()
				provider = provider[len(provider)-4:]
				fields.Set("Hash", tx.Hash)
				fields.Set("Provider", fmt.Sprintf("`%s`", provider))
				fields.Set("Memo", fmt.Sprintf("`%s`", msgDeposit.Memo))
				fields.Set("Amount", fmt.Sprintf("%sᚱ", FormatLocale(float64(amount)/common.One)))

				// extract node address from memo
				m, err := thorchain.ParseMemo(common.LatestVersion, msgDeposit.Memo)
				if err != nil {
					log.Panic().Str("memo", msgDeposit.Memo).Err(err).Msg("failed to parse memo")
				}

				addNodeInfo := func(nodeAddress string) {
					fields.Set("Node", fmt.Sprintf("`%s`", nodeAddress[len(nodeAddress)-4:]))

					// lookup node to determine operator
					nodes := []openapi.Node{}
					err = ThornodeCachedRetryGet("thorchain/nodes", block.Header.Height, &nodes)
					if err != nil {
						log.Panic().Err(err).Msg("failed to get nodes")
					}
					for _, node := range nodes {
						if node.NodeAddress == nodeAddress {
							fields.Set("Operator", fmt.Sprintf("`%s`", node.NodeOperatorAddress[len(node.NodeOperatorAddress)-4:]))
							break
						}
					}
				}

				switch memo := m.(type) {
				case thorchain.BondMemo:
					addNodeInfo(memo.NodeAddress.String())
				case thorchain.UnbondMemo:
					addNodeInfo(memo.NodeAddress.String())
					unbondAmount := cosmos.NewUintFromString(event["amount"]).Uint64()
					fields.Set("Unbond Amount", FormatLocale(float64(unbondAmount)/common.One))
				}

				Notify(config.Notifications.Activity, title, nil, false, fields)
			}
		}
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Failed Transactions
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
		case 99: // internal, avoid noise
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
		Notify(config.Notifications.Activity, title, nil, false, fields)
	}
}
