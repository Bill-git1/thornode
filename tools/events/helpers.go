package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gitlab.com/thorchain/thornode/common"
	"gitlab.com/thorchain/thornode/common/cosmos"
	openapi "gitlab.com/thorchain/thornode/openapi/gen"
)

////////////////////////////////////////////////////////////////////////////////////////
// Format
////////////////////////////////////////////////////////////////////////////////////////

func FormatDuration(d time.Duration) string {
	hours := d / time.Hour
	d -= hours * time.Hour
	minutes := d / time.Minute
	d -= minutes * time.Minute
	seconds := d / time.Second

	return fmt.Sprintf("%02dh %02dm %02ds", hours, minutes, seconds)
}

var reStripMarkdownLinks = regexp.MustCompile(`\[[0-9a-zA-Z_ ]+\]\((.+?)\)`)

func StripMarkdownLinks(input string) string {
	return reStripMarkdownLinks.ReplaceAllString(input, "$1")
}

func FormatLocale[T int | uint | int64 | uint64](n T) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var result strings.Builder
	prefix := len(s) % 3
	if prefix > 0 {
		result.WriteString(s[:prefix])
		if len(s) > prefix {
			result.WriteByte(',')
		}
	}

	for i := prefix; i < len(s); i += 3 {
		result.WriteString(s[i : i+3])
		if i+3 < len(s) {
			result.WriteByte(',')
		}
	}

	return result.String()
}

func Moneybags(usdValue int) string {
	count := usdValue / config.Styles.USDPerMoneyBag
	return strings.Repeat(EmojiMoneybag, count)
}

////////////////////////////////////////////////////////////////////////////////////////
// USD Value
////////////////////////////////////////////////////////////////////////////////////////

func USDValue(height int64, coin common.Coin) float64 {
	if coin.Asset.IsRune() {
		network := openapi.NetworkResponse{}
		err := ThornodeCachedRetryGet("thorchain/network", height, &network)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get network")
		}

		price, err := strconv.ParseFloat(network.RunePriceInTor, 64)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to parse network rune price")
		}
		return float64(coin.Amount.Uint64()) / common.One * price / common.One
	}

	// get pools response
	pools := []openapi.Pool{}
	err := ThornodeCachedRetryGet("thorchain/pools", height, &pools)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get pools")
	}

	// find pool and convert value
	for _, pool := range pools {
		if pool.Asset != coin.Asset.GetLayer1Asset().String() {
			continue
		}
		price := cosmos.NewUintFromString(pool.AssetTorPrice)
		return float64(coin.Amount.Uint64()) / common.One * float64(price.Uint64()) / common.One
	}

	// should be unreachable
	log.Fatal().Str("asset", coin.Asset.String()).Msg("failed to find pool")
	return 0
}

func ExternalUSDValue(coin common.Coin) float64 {
	// parameters for crypto compare api
	fsym := ""

	switch coin.Asset.GetLayer1Asset() {
	case common.BTCAsset:
		fsym = "BTC"
	case common.ETHAsset:
		fsym = "ETH"
	default:
		log.Error().Str("asset", coin.Asset.String()).Msg("unsupported external value asset")
		return 0
	}

	// get price from crypto compare
	tsyms := "USD"
	url := fmt.Sprintf("https://min-api.cryptocompare.com/data/price?fsym=%s&tsyms=%s", fsym, tsyms)
	price := struct {
		USD float64 `json:"USD"`
	}{}
	err := RetryGet(url, &price)
	if err != nil {
		log.Error().Err(err).Str("url", url).Msg("failed to get external price")
		return 0
	}

	return float64(coin.Amount.Uint64()) / common.One * price.USD / common.One
}

func USDValueString(height int64, coin common.Coin) string {
	value := USDValue(height, coin)
	integerPart := int(value)
	decimalPart := int((value - float64(integerPart)) * 100)
	return fmt.Sprintf("$%s.%02d", FormatLocale(integerPart), decimalPart)
}
