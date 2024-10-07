package main

import (
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
)

////////////////////////////////////////////////////////////////////////////////////////
// Constants
////////////////////////////////////////////////////////////////////////////////////////

const (
	EmojiMoneybag         = ":moneybag:"
	EmojiMoneyWithWings   = ":money_with_wings:"
	EmojiDollar           = ":dollar:"
	EmojiWhiteCheckMark   = ":white_check_mark:"
	EmojiSmallRedTriangle = ":small_red_triangle:"
	EmojiRotatingLight    = ":rotating_light:"
)

////////////////////////////////////////////////////////////////////////////////////////
// Webhooks
////////////////////////////////////////////////////////////////////////////////////////

type Webhooks struct {
	Category  string `mapstructure:"name"`
	Slack     string `mapstructure:"slack"`
	Discord   string `mapstructure:"discord"`
	PagerDuty string `mapstructure:"pagerduty"`
}

////////////////////////////////////////////////////////////////////////////////////////
// Config
////////////////////////////////////////////////////////////////////////////////////////

type Config struct {
	// StoragePath is parent directory for any persisted state.
	StoragePath string `mapstructure:"storage_path"`

	// Console enables console mode with pretty output and notifications to terminal.
	Console bool `mapstructure:"console"`

	// MaxRetries is the number of times to retry requests with backoff.
	MaxRetries int `mapstructure:"max_retries"`

	// Network is the network to user for prefixes.
	Network string `mapstructure:"network"`

	// Scan contains overrides for the heights to scan.
	Scan struct {
		// Start is the block height to start scanning from.
		Start int `mapstructure:"start"`

		// Stop is the block height to stop scanning at.
		Stop int `mapstructure:"stop"`
	} `mapstructure:"scan"`

	// Endpoints contain URLs to services that are used in block scanning.
	Endpoints struct {
		// CacheSize is the number responses to keep in LRU cache.
		CacheSize int `mapstructure:"cache_size"`

		Thornode string `mapstructure:"thornode"`
		Midgard  string `mapstructure:"midgard"`
	} `mapstructure:"endpoints"`

	// Notifications contain categories of webhooks that route to multiple services.
	Notifications struct {
		Activity    Webhooks `mapstructure:"activity"`
		Lending     Webhooks `mapstructure:"lending"`
		Info        Webhooks `mapstructure:"info"`
		Security    Webhooks `mapstructure:"security"`
		Reschedules Webhooks `mapstructure:"reschedules"`
	} `mapstructure:"notifications"`

	// Links contain URLs to services linked in alerts.
	Links struct {
		// Track is the Nine Realms Tracker service.
		Track string `mapstructure:"track"`

		// Explorer is the native Thorchain explorer, should support:
		// - <explorer>/tx/<txid>
		// - <explorer>/address/<address>
		// - <explorer>/block/<height>
		Explorer string `mapstructure:"explorer"`

		// Thornode is the Thornode API endpoint to use in message links.
		Thornode string `mapstructure:"thornode"`
	} `mapstructure:"explorers"`

	// Thresholds contain various thresholds for alerts.
	Thresholds struct {
		USDValue  uint64 `mapstructure:"usd_value"`
		RuneValue uint64 `mapstructure:"rune_value"`

		// Delta contains thresholds for USD value and percent change. The alert will fire
		// if both thresholds are met.
		Delta struct {
			USDValue uint64 `mapstructure:"usd_value"`
			Percent  uint64 `mapstructure:"percent"`
		} `mapstructure:"delta"`

		Security struct {
			USDValue uint64 `mapstructure:"usd_value"`
		} `mapstructure:"security"`
	} `mapstructure:"thresholds"`

	// Styles contain various styling for alerts.
	Styles struct {
		USDPerMoneyBag uint64 `mapstructure:"usd_per_money_bag"`
	} `mapstructure:"styles"`

	// LabeledAddresses is a map of addresses to labels.
	LabeledAddresses map[string]string `mapstructure:"labeled_addresses"`
}

////////////////////////////////////////////////////////////////////////////////////////
// Default
////////////////////////////////////////////////////////////////////////////////////////

var config = Config{}

func init() {
	// storage path
	config.StoragePath = "/tmp/events"

	// endpoints
	config.Endpoints.CacheSize = 100
	config.Endpoints.Thornode = "https://thornode.ninerealms.com"
	config.Endpoints.Midgard = "https://midgard.ninerealms.com"

	// notifications
	config.Notifications.Activity.Category = "Activity"
	config.Notifications.Info.Category = "Info"
	config.Notifications.Lending.Category = "Lending"
	config.Notifications.Security.Category = "Security"
	config.Notifications.Reschedules.Category = "Reschedules"

	// retries
	config.MaxRetries = 10

	// network
	config.Network = "mainnet"

	// links
	config.Links.Track = "https://track.ninerealms.com"
	config.Links.Explorer = "https://runescan.io"
	config.Links.Thornode = "https://thornode.ninerealms.com"

	// thresholds
	config.Thresholds.USDValue = 100_000
	config.Thresholds.RuneValue = 1_000_000
	config.Thresholds.Delta.USDValue = 50_000
	config.Thresholds.Delta.Percent = 5
	config.Thresholds.Security.USDValue = 3_000_000

	// styles
	config.Styles.USDPerMoneyBag = 100_000

	// labeled addresses
	// https://raw.githubusercontent.com/ViewBlock/cryptometa/master/data/thorchain/labels.json
	config.LabeledAddresses = map[string]string{
		"thor1dheycdevq39qlkxs2a6wuuzyn4aqxhve4qxtxt":  "Reserve Module",
		"thor17gw75axcnr8747pkanye45pnrwk7p9c3cqncsv":  "Bond Module",
		"thor1g98cy3n9mmjrpn0sxmn63lztelera37n8n67c0":  "Pool Module",
		"thor1ty6h2ll07fqfzumphp6kq3hm4ps28xlm2l6kd6":  "crypto.com",
		"thor1505gp5h48zd24uexrfgka70fg8ccedafsnj0e3":  "Treasury1",
		"thor1lj62pg6ryxv2htekqx04nv7wd3g98qf9gfvamy":  "Standby Reserve",
		"thor1lrnrawjlfp6jyrzf39r740ymnuk9qgdgp29rqv":  "Vested Wallet1",
		"thor16qnm285eez48r4u9whedq4qunydu2ucmzchz7p":  "Vested Wallet2",
		"thor1egxvam70a86jafa8gcg3kqfmfax3s0m2g3m754":  "TreasuryLP",
		"thor1wfe7hsuvup27lx04p5al4zlcnx6elsnyft7dzm":  "TreasuryLP2",
		"thor14n2q7tpemxcha8zc26j0g5pksx4x3a9xw9ryq9":  "Treasury2",
		"thor1qd4my7934h2sn5ag5eaqsde39va4ex2asz3yv5":  "Treasury Multisig",
		"thor1y5lk3rzatghv9y4s4j90qt9ayq83e2dpf2hvzc":  "Vesting 9R",
		"thor1t60f02r8jvzjrhtnjgfj4ne6rs5wjnejwmj7fh":  "Binance Hot",
		"thor1cqg8pyxnq03d88cl3xfn5wzjkguw5kh9enwte4":  "Binance Cold",
		"thor1uz4fpyd5f5d6p9pzk8lxyj4qxnwq6f9utg0e7k":  "Binance",
		"thor1v8ppstuf6e3x0r4glqc68d5jqcs2tf38cg2q6y":  "Synth Module",
		"sthor1g98cy3n9mmjrpn0sxmn63lztelera37nn2xgw3": "Pool Module",
		"sthor1dheycdevq39qlkxs2a6wuuzyn4aqxhvepe6as4": "Reserve Module",
		"sthor17gw75axcnr8747pkanye45pnrwk7p9c3ve0wxj": "Bond Module",
		"thor1nm0rrq86ucezaf8uj35pq9fpwr5r82clphp95t":  "Kraken",
	}

	// setup viper and bind to config
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.AllowEmptyEnv(true)
	if err := viper.Unmarshal(&config); err != nil {
		log.Panic().Err(err).Msg("failed to unmarshal config")
	}
}
