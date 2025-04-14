package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	tmhttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/app/params"
	"gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	keeperv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	eddsaKey "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

////////////////////////////////////////////////////////////////////////////////////////
// Cosmos
////////////////////////////////////////////////////////////////////////////////////////

var (
	encodingConfig params.EncodingConfig
	keyRing        keyring.Keyring
)

func init() {
	// initialize the bech32 prefix for mocknet
	config := cosmos.GetConfig()
	config.SetBech32PrefixForAccount("tthor", "tthorpub")
	config.SetBech32PrefixForValidator("tthorv", "tthorvpub")
	config.SetBech32PrefixForConsensusNode("tthorc", "tthorcpub")
	config.Seal()

	// initialize the codec
	encodingConfig = app.MakeEncodingConfig()
	keyRing = keyring.NewInMemory(encodingConfig.Codec)

	// Having set the prefixes, derive the module addresses.
	ModuleAddrTransfer = authtypes.NewModuleAddress("transfer").String() // "tthor1yl6hdjhmkf37639730gffanpzndzdpmhv07zme"
	// "transfer" is special, as http://localhost:1317/auth/accounts/tthor1yl6hdjhmkf37639730gffanpzndzdpmhv07zme
	// gets the name from the address, but no address from name from http://localhost:1317/thorchain/balance/module/transfer
	ModuleAddrThorchain = authtypes.NewModuleAddress("thorchain").String()                    // "tthor1v8ppstuf6e3x0r4glqc68d5jqcs2tf38ulmsrp"
	ModuleAddrAsgard = authtypes.NewModuleAddress("asgard").String()                          // "tthor1g98cy3n9mmjrpn0sxmn63lztelera37nrytwp2"
	ModuleAddrBond = authtypes.NewModuleAddress("bond").String()                              // "tthor17gw75axcnr8747pkanye45pnrwk7p9c3uhzgff"
	ModuleAddrReserve = authtypes.NewModuleAddress("reserve").String()                        // "tthor1dheycdevq39qlkxs2a6wuuzyn4aqxhve3hhmlw"
	ModuleAddrFeeCollector = authtypes.NewModuleAddress("fee_collector").String()             // "tthor17xpfvakm2amg962yls6f84z3kell8c5ljftt88"
	ModuleAddrLending = authtypes.NewModuleAddress("lending").String()                        // "tthor1x0kgm82cnj0vtmzdvz4avk3e7sj427t0al8wky"
	ModuleAddrAffiliateCollector = authtypes.NewModuleAddress("affiliate_collector").String() // "tthor1dl7un46w7l7f3ewrnrm6nq58nerjtp0d82uzjg"
	ModuleAddrTreasury = authtypes.NewModuleAddress("treasury").String()                      // "tthor1vmafl8f3s6uuzwnxkqz0eza47v6ecn0ttstnny"
	ModuleAddrRUNEPool = authtypes.NewModuleAddress("rune_pool").String()                     // "tthor1rzqfv62dzu585607s5awqtgnvvwz5rzhfuaw80"
	ModuleAddrClaiming = authtypes.NewModuleAddress("tcy_claim").String()                     // "tthor1ss8rrf3twa20kf9frdyru05dmu2kg9llwwcgag"
	ModuleAddrTCYStake = authtypes.NewModuleAddress("tcy_stake").String()                     // "tthor128a8hqnkaxyqv7qwajpggmfyudh64jl3uxmuaf"
}

func clientContextAndFactory(routine int) (client.Context, tx.Factory) {
	// create new rpc client
	node := fmt.Sprintf("http://localhost:%d", 26657+routine)
	rpcClient, err := tmhttp.NewWithTimeout(node, "/websocket", 5)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create tendermint client")
	}

	// create cosmos-sdk client context
	clientCtx := client.Context{
		Client:            rpcClient,
		ChainID:           "thorchain",
		Codec:             encodingConfig.Codec,
		InterfaceRegistry: encodingConfig.InterfaceRegistry,
		Keyring:           keyRing,
		BroadcastMode:     flags.BroadcastSync,
		SkipConfirm:       true,
		TxConfig:          encodingConfig.TxConfig,
		AccountRetriever:  authtypes.AccountRetriever{},
		NodeURI:           node,
		LegacyAmino:       encodingConfig.Amino,
	}

	// create tx factory
	txFactory := tx.Factory{}
	txFactory = txFactory.WithKeybase(clientCtx.Keyring)
	txFactory = txFactory.WithTxConfig(clientCtx.TxConfig)
	txFactory = txFactory.WithAccountRetriever(clientCtx.AccountRetriever)
	txFactory = txFactory.WithChainID(clientCtx.ChainID)
	txFactory = txFactory.WithGas(0)
	txFactory = txFactory.WithSignMode(signing.SignMode_SIGN_MODE_DIRECT)

	return clientCtx, txFactory
}

////////////////////////////////////////////////////////////////////////////////////////
// Logging
////////////////////////////////////////////////////////////////////////////////////////

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Logger = log.With().Caller().Logger()

	// set to info level if DEBUG is not set (debug is the default level)
	if os.Getenv("DEBUG") == "" {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Colors
////////////////////////////////////////////////////////////////////////////////////////

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorYellow = "\033[33m"
	ColorGreen  = "\033[32m"
	ColorPurple = "\033[35m"

	// save for later
	// ColorBlue   = "\033[34m"
	// ColorCyan   = "\033[36m"
	// ColorGray   = "\033[37m"
	// ColorWhite  = "\033[97m"
)

////////////////////////////////////////////////////////////////////////////////////////
// HTTP
////////////////////////////////////////////////////////////////////////////////////////

var httpClient = &http.Client{
	Transport: &http.Transport{
		Dial: (&net.Dialer{
			Timeout: 5 * time.Second,
		}).Dial,
	},
	Timeout: 5 * time.Second,
}

////////////////////////////////////////////////////////////////////////////////////////
// Thorchain Module Addresses
////////////////////////////////////////////////////////////////////////////////////////

var (
	// Set these in `init` after the address prefix has been set.
	ModuleAddrTransfer           string
	ModuleAddrThorchain          string
	ModuleAddrAsgard             string
	ModuleAddrBond               string
	ModuleAddrReserve            string
	ModuleAddrFeeCollector       string
	ModuleAddrLending            string
	ModuleAddrAffiliateCollector string
	ModuleAddrTreasury           string
	ModuleAddrRUNEPool           string
	ModuleAddrClaiming           string
	ModuleAddrTCYStake           string
)

////////////////////////////////////////////////////////////////////////////////////////
// Invariants
////////////////////////////////////////////////////////////////////////////////////////

var invariants []string

func init() {
	k := keeperv1.KVStore{}
	for _, ir := range k.InvariantRoutes() {
		invariants = append(invariants, ir.Route)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Keys
////////////////////////////////////////////////////////////////////////////////////////

var (
	addressToName      = map[string]string{} // thor...->dog, 0x...->dog
	templateAddress    = map[string]string{} // addr_thor_dog->thor..., addr_eth_dog->0x...
	templatePubKey     = map[string]string{} // pubkey_dog->thorpub...
	templateConsPubKey = map[string]string{} // cons_pubkey_dog->thorcpub...

	birdMnemonic   = strings.Repeat("bird ", 23) + "asthma"
	catMnemonic    = strings.Repeat("cat ", 23) + "crawl"
	deerMnemonic   = strings.Repeat("deer ", 23) + "diesel"
	dogMnemonic    = strings.Repeat("dog ", 23) + "fossil"
	duckMnemonic   = strings.Repeat("duck ", 23) + "face"
	fishMnemonic   = strings.Repeat("fish ", 23) + "fade"
	foxMnemonic    = strings.Repeat("fox ", 23) + "filter"
	frogMnemonic   = strings.Repeat("frog ", 23) + "flat"
	goatMnemonic   = strings.Repeat("goat ", 23) + "install"
	hawkMnemonic   = strings.Repeat("hawk ", 23) + "juice"
	lionMnemonic   = strings.Repeat("lion ", 23) + "misery"
	mouseMnemonic  = strings.Repeat("mouse ", 23) + "option"
	muleMnemonic   = strings.Repeat("mule ", 23) + "major"
	pigMnemonic    = strings.Repeat("pig ", 23) + "quick"
	rabbitMnemonic = strings.Repeat("rabbit ", 23) + "rent"
	wolfMnemonic   = strings.Repeat("wolf ", 23) + "victory"

	// mnemonics contains the set of all mnemonics for accounts used in tests
	mnemonics = [...]string{
		dogMnemonic,
		catMnemonic,
		foxMnemonic,
		pigMnemonic,
		birdMnemonic,
		deerMnemonic,
		duckMnemonic,
		fishMnemonic,
		frogMnemonic,
		goatMnemonic,
		hawkMnemonic,
		lionMnemonic,
		mouseMnemonic,
		muleMnemonic,
		rabbitMnemonic,
		wolfMnemonic,
	}
)

func init() {
	// register functions for all mnemonic-chain addresses
	for _, m := range mnemonics {
		name := strings.Split(m, " ")[0]

		// create pubkey for mnemonic
		derivedPriv, err := hd.Secp256k1.Derive()(m, "", cmd.THORChainHDPath)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to derive private key")
		}
		privKey := hd.Secp256k1.Generate()(derivedPriv)
		ecdsaPubKey, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, privKey.PubKey())
		if err != nil {
			log.Fatal().Err(err).Msg("failed to bech32ify ecdsa pubkey")
		}

		ed25519PrivKey := eddsaKey.GenPrivKeyFromSecret([]byte(m))
		edd2519ConsPubKey, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeConsPub, ed25519PrivKey.PubKey())
		if err != nil {
			log.Fatal().Err(err).Msg("failed to bech32ify EdDSA cons pubkey")
		}
		ed25519PubKey, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, ed25519PrivKey.PubKey())
		if err != nil {
			log.Fatal().Err(err).Msg("failed to bech32ify EdDSA acc pubkey")
		}

		// add key to keyring
		_, err = keyRing.NewAccount(name, m, "", cmd.THORChainHDPath, hd.Secp256k1)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to add account to keyring")
		}

		for _, chain := range common.AllChains {
			// register template address for all chains
			var addr common.Address
			addr, err = common.PubKey(ecdsaPubKey).GetAddress(chain)
			if err != nil {
				log.Fatal().Err(err).Msg("failed to get address")
			}
			lowerChain := strings.ToLower(chain.String())
			templateAddress[fmt.Sprintf("addr_%s_%s", lowerChain, name)] = addr.String()

			// register address to name
			addressToName[addr.String()] = name

			// register pubkey for thorchain
			if chain == common.THORChain {
				templatePubKey[fmt.Sprintf("pubkey_%s", name)] = ecdsaPubKey
				templateConsPubKey[fmt.Sprintf("cons_pubkey_%s", name)] = edd2519ConsPubKey
				templatePubKey[fmt.Sprintf("pubkey_%s_eddsa", name)] = ed25519PubKey
			}
		}
	}
}
