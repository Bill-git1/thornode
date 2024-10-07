package main

import (
	"os"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/cmd"
	"gitlab.com/thorchain/thornode/constants"
	"gitlab.com/thorchain/thornode/tools/thorscan"
)

////////////////////////////////////////////////////////////////////////////////////////
// InitNetwork
////////////////////////////////////////////////////////////////////////////////////////

func InitNetwork() {
	var bech32PrefixAccAddr string
	var bech32PrefixAccPub string
	var bech32PrefixValAddr string
	var bech32PrefixValPub string
	var bech32PrefixConsAddr string
	var bech32PrefixConsPub string

	switch config.Network {
	case "mainnet":
		bech32PrefixAccAddr = "thor"
		bech32PrefixAccPub = "thorpub"
		bech32PrefixValAddr = "thorv"
		bech32PrefixValPub = "thorvpub"
		bech32PrefixConsAddr = "thorc"
		bech32PrefixConsPub = "thorcpub"

	case "stagenet":
		bech32PrefixAccAddr = "sthor"
		bech32PrefixAccPub = "sthorpub"
		bech32PrefixValAddr = "sthorv"
		bech32PrefixValPub = "sthorvpub"
		bech32PrefixConsAddr = "sthorc"
		bech32PrefixConsPub = "sthorcpub"

	case "mocknet":
		bech32PrefixAccAddr = "tthor"
		bech32PrefixAccPub = "tthorpub"
		bech32PrefixValAddr = "tthorv"
		bech32PrefixValPub = "tthorvpub"
		bech32PrefixConsAddr = "tthorc"
		bech32PrefixConsPub = "tthorcpub"

	default:
		log.Fatal().Str("network", config.Network).Msg("unknown network")
	}

	// initialize the bech32 prefixes
	cfg := sdk.GetConfig()
	cfg.SetBech32PrefixForAccount(bech32PrefixAccAddr, bech32PrefixAccPub)
	cfg.SetBech32PrefixForValidator(bech32PrefixValAddr, bech32PrefixValPub)
	cfg.SetBech32PrefixForConsensusNode(bech32PrefixConsAddr, bech32PrefixConsPub)
	cfg.SetCoinType(cmd.THORChainCoinType)
	cfg.SetPurpose(cmd.THORChainCoinPurpose)
	cfg.Seal()
	sdk.SetCoinDenomRegex(func() string {
		return cmd.DenomRegex
	})
}

////////////////////////////////////////////////////////////////////////////////////////
// ScanBlock
////////////////////////////////////////////////////////////////////////////////////////

func ScanBlock(block *thorscan.BlockResponse) {
	ScanInfo(block)
	ScanActivity(block)
	ScanSecurity(block)
	ScanLoans(block)
}

////////////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////////////

func main() {
	// unix time and JSON logging in the cluster, otherwise make it pretty
	if _, err := os.Stat("/run/secrets/kubernetes.io"); err == nil {
		zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	} else {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
	log.Logger = log.With().Caller().Logger()

	// initialize
	InitCache()
	InitNetwork()
	thorscan.APIEndpoint = config.Endpoints.Thornode

	// prune local storage
	Prune("scheduled-outbound")
	Prune("seen-inactive-inbound")
	Prune("seen-large-unconfirmed-inbound")
	Prune("seen-large-streaming-swap")

	// load the last scanned height from storage
	height := -1
	err := Load("height", &height)
	if err != nil {
		log.Warn().Err(err).Msg("unable to load height")
	} else {
		log.Info().Int("height", height).Msg("loaded height")
		height++ // start from the next block
	}

	// override with config
	if config.Scan.Start != 0 {
		height = config.Scan.Start
		log.Info().Int("height", height).Msg("overriding start height")
	}

	// if in console mode set log level to error
	if config.Console {
		log.Info().Msg("console mode enabled")
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	}

	for block := range thorscan.Scan(height, config.Scan.Stop) {
		// trail by one block to avoid race with downstream midgard use
		var blockTime time.Time
		blockTime, err = time.Parse(time.RFC3339, block.Header.Time)
		if err != nil {
			log.Fatal().Err(err).Msg("unable to parse block time")
		}
		time.Sleep(time.Until(blockTime.Add(constants.ThorchainBlockTime)))

		ScanBlock(block)

		err = Store("height", block.Header.Height)
		if err != nil {
			log.Fatal().Err(err).Int64("height", block.Header.Height).Msg("unable to store height")
		}
		log.Info().Int64("height", block.Header.Height).Msg("scanned block")
	}
}
