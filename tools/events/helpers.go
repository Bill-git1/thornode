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
// Regexes
////////////////////////////////////////////////////////////////////////////////////////

var (
	reMemoMigration = regexp.MustCompile(`MIGRATE:(\d+)`)
	reMemoRagnarok  = regexp.MustCompile(`RAGNAROK:(\d+)`)
	reMemoRefund    = regexp.MustCompile(`REFUND:(.+)`)
)

////////////////////////////////////////////////////////////////////////////////////////
// Type Conversions
////////////////////////////////////////////////////////////////////////////////////////

func CoinToCommon(coin openapi.Coin) common.Coin {
	amount := cosmos.NewUintFromString(coin.Amount)
	asset, err := common.NewAsset(coin.Asset)
	if err != nil {
		log.Panic().Err(err).Str("asset", coin.Asset).Msg("failed to parse coin asset")
	}
	return common.NewCoin(asset, amount)
}

////////////////////////////////////////////////////////////////////////////////////////
// Format
////////////////////////////////////////////////////////////////////////////////////////

func FormatDuration(d time.Duration) string {
	str := ""
	days := d / (24 * time.Hour)
	if days > 0 {
		str += fmt.Sprintf("%dd ", days)
	}
	d -= days * (24 * time.Hour)
	hours := d / time.Hour
	if hours > 0 {
		str += fmt.Sprintf("%dh ", hours)
	}
	d -= hours * time.Hour
	minutes := d / time.Minute
	if minutes > 0 {
		str += fmt.Sprintf("%dm ", minutes)
	}
	d -= minutes * time.Minute
	seconds := d / time.Second
	if seconds > 0 || str == "" {
		str += fmt.Sprintf("%ds", seconds)
	}

	return strings.TrimSpace(str)
}

var reStripMarkdownLinks = regexp.MustCompile(`\[[0-9a-zA-Z_ ]+\]\((.+?)\)`)

func StripMarkdownLinks(input string) string {
	return reStripMarkdownLinks.ReplaceAllString(input, "$1")
}

func FormatLocale[T int | uint | int64 | uint64 | float64](n T) string {
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}

	// extract decimals from float
	var decimals string
	if x, ok := any(n).(float64); ok {
		n = T(int64(x))
		decimals = fmt.Sprintf("%.8f", x)[len(fmt.Sprintf("%v", n)):]
	}

	s := fmt.Sprintf("%v", n)
	if len(s) <= 3 {
		if negative {
			return "-" + s + decimals
		}
		return s + decimals
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

	result.WriteString(decimals)

	if negative {
		return "-" + result.String()
	}
	return result.String()
}

func FormatUSD(value float64) string {
	negative := false
	if value < 0 {
		negative = true
		value = -value
	}
	integerPart := int(value)
	decimalPart := int((value - float64(integerPart)) * 100)
	str := fmt.Sprintf("$%s.%02d", FormatLocale(integerPart), decimalPart)
	if negative {
		str = "-" + str
	}
	return str
}

func Moneybags(usdValue uint64) string {
	count := int(usdValue / config.Styles.USDPerMoneyBag)
	return strings.Repeat(EmojiMoneybag, count)
}

////////////////////////////////////////////////////////////////////////////////////////
// USD Value
////////////////////////////////////////////////////////////////////////////////////////

func USDValue(height int64, coin common.Coin) float64 {
	if coin.Asset.Equals(common.TOR) {
		return float64(coin.Amount.Uint64()) / common.One
	}

	if coin.IsRune() {
		network := openapi.NetworkResponse{}
		err := ThornodeCachedRetryGet("thorchain/network", height, &network)
		if err != nil {
			log.Panic().Err(err).Msg("failed to get network")
		}

		price, err := strconv.ParseFloat(network.RunePriceInTor, 64)
		if err != nil {
			log.Panic().Err(err).Msg("failed to parse network rune price")
		}
		return float64(coin.Amount.Uint64()) / common.One * price / common.One
	}

	// get pools response
	pools := []openapi.Pool{}
	err := ThornodeCachedRetryGet("thorchain/pools", height, &pools)
	if err != nil {
		log.Panic().Err(err).Msg("failed to get pools")
	}

	// find pool and convert value
	asset := coin.Asset.GetLayer1Asset()
	if asset.IsDerivedAsset() {
		asset.Chain = common.Chain(asset.Symbol)
	}
	for _, pool := range pools {
		if pool.Asset != asset.GetLayer1Asset().String() {
			continue
		}
		price := cosmos.NewUintFromString(pool.AssetTorPrice)
		return float64(coin.Amount.Uint64()) / common.One * float64(price.Uint64()) / common.One
	}

	log.Error().Str("asset", asset.String()).Msg("failed to find pool")
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

	return float64(coin.Amount.Uint64()) * price.USD / common.One
}

func USDValueString(height int64, coin common.Coin) string {
	value := USDValue(height, coin)
	return FormatUSD(value)
}
