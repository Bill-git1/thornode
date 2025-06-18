package main

import (
	"fmt"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ecommon "github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/suites"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	"gitlab.com/thorchain/thornode/v3/x/thorchain"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Chain RPCs
////////////////////////////////////////////////////////////////////////////////////////

var chainRPCs = map[common.Chain]string{
	common.BTCChain:  "http://localhost:18443",
	common.LTCChain:  "http://localhost:38443",
	common.BCHChain:  "http://localhost:28443",
	common.DOGEChain: "http://localhost:18332",
	common.ETHChain:  "http://localhost:8545",
	common.AVAXChain: "http://localhost:9650/ext/bc/C/rpc",
	common.GAIAChain: "localhost:9091",
	common.BASEChain: "http://localhost:8547",
	common.XRPChain:  "http://localhost:5005",
}

////////////////////////////////////////////////////////////////////////////////////////
// Mocknet Mnemonics
////////////////////////////////////////////////////////////////////////////////////////

var (
	mocknetMasterMnemonic = strings.Repeat("master ", 23) + "notice"

	mocknetValidatorMnemonics = [...]string{
		strings.Repeat("dog ", 23) + "fossil",
		strings.Repeat("cat ", 23) + "crawl",
		strings.Repeat("fox ", 23) + "filter",
		strings.Repeat("pig ", 23) + "quick",
	}

	mocknetUserMnemonics = [...]string{
		strings.Repeat("bird ", 23) + "asthma",
		strings.Repeat("deer ", 23) + "diesel",
		strings.Repeat("duck ", 23) + "face",
		strings.Repeat("fish ", 23) + "fade",
		strings.Repeat("frog ", 23) + "flat",
		strings.Repeat("goat ", 23) + "install",
		strings.Repeat("hawk ", 23) + "juice",
		strings.Repeat("lion ", 23) + "misery",
		strings.Repeat("mouse ", 23) + "option",
		strings.Repeat("mule ", 23) + "major",
		strings.Repeat("rabbit ", 23) + "rent",
		strings.Repeat("wolf ", 23) + "victory",
	}
)

////////////////////////////////////////////////////////////////////////////////////////
// Init
////////////////////////////////////////////////////////////////////////////////////////

func InitConfig(parallelism int, seed bool) *OpConfig {
	if parallelism > len(mocknetUserMnemonics) {
		log.Error().
			Int("parallelism", parallelism).
			Int("accounts", len(mocknetUserMnemonics)).
			Msg("parallelism limited by available user accounts")
	}

	c := &OpConfig{
		NodeUsers: make([]*User, len(mocknetValidatorMnemonics)),
	}
	mu := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	sem := make(chan struct{}, 8)

	// since we reuse the bifrost thorclient, load endpoints into config package
	os.Setenv("BIFROST_THORCHAIN_CHAIN_HOST", "localhost:1317")
	os.Setenv("BIFROST_THORCHAIN_CHAIN_RPC", "localhost:26657")
	os.Setenv("BIFROST_THORCHAIN_CHAIN_EBIFROST", "localhost:50051")
	config.Init()

	// validators
	for i, mnemonic := range mocknetValidatorMnemonics {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, mnemonic string) {
			a := NewUser(mnemonic, liteClientConstructors)
			mu.Lock()
			c.NodeUsers[i] = a
			mu.Unlock()

			defer func() {
				<-sem
				wg.Done()
			}()

			// send gaia network fee observation if this is a seed run
			if !seed {
				return
			}

			// only the first mnemonic is an active node at init
			if i != 0 {
				return
			}

			// halt churning
			accAddr, err := a.PubKey(common.THORChain).GetThorAddress()
			if err != nil {
				log.Error().Err(err).Msg("failed to get thor address")
			}
			mimir := thorchain.NewMsgMimir("HALTCHURNING", 1, accAddr)
			_, err = a.Thorchain.Broadcast(mimir)
			if err != nil {
				log.Error().Err(err).Msg("failed to broadcast mimir")
			}

			// default network fees on chains needing a window of blocks before bifrost sends
			defaultFees := []struct {
				chain common.Chain
				size  uint64
				rate  uint64
			}{
				{common.GAIAChain, 1, 1_000_000},
				{common.XRPChain, 1, 1_000},
				{common.AVAXChain, 80000, 150},
				{common.BASEChain, 80000, 30},
				{common.ETHChain, 80000, 30},
			}
			for _, fee := range defaultFees {
				log.Info().Msgf("posting %s network fee", fee.chain)
				for {
					_, err := a.Thorchain.PostNetworkFee(1, fee.chain, fee.size, fee.rate)
					if err == nil {
						break
					}
					log.Error().Err(err).Msg("failed to post network fee")
					time.Sleep(2 * time.Second)
				}
			}
		}(i, mnemonic)
	}

	// users
	for _, mnemonic := range mocknetUserMnemonics[:parallelism] {
		wg.Add(1)
		sem <- struct{}{}
		go func(mnemonic string) {
			a := NewUser(mnemonic, liteClientConstructors)
			mu.Lock()
			c.Users = append(c.Users, a)
			mu.Unlock()
			<-sem
			wg.Done()
		}(mnemonic)
	}

	// wait for all users to be created
	wg.Wait()

	// fund all user accounts from master
	master := NewUser(mocknetMasterMnemonic, liteClientConstructors)

	// log all configured tokens, their decimals, and master balance
	for chain := range liteClientConstructors {
		account, err := master.ChainClients[chain].GetAccount(nil)
		if err != nil {
			log.Fatal().Stringer("chain", chain).Err(err).Msg("failed to get master account")
		}
		for _, coin := range account.Coins {
			ctxLog := log.Info().
				Stringer("chain", chain).
				Stringer("asset", coin.Asset).
				Stringer("address", master.Address(chain)).
				Str("amount", coin.Amount.String())

			// on evm chains, also log token decimals for debugging
			if chain.IsEVM() {
				token := evm.Tokens(chain)[coin.Asset]
				evmClient := master.ChainClients[chain].(*evm.Client)
				tokenDecimals, err := evmClient.GetTokenDecimals(token.Address)
				if err != nil {
					log.Fatal().Err(err).Msg("failed to get token decimals")
				}

				// sanity check our configured token decimals
				if tokenDecimals != token.Decimals {
					log.Fatal().
						Int("actual", tokenDecimals).
						Int("configured", token.Decimals).
						Err(err).
						Msg("token decimals mismatch")
				}

				ctxLog = ctxLog.Int("decimals", tokenDecimals)
			}

			// log balance
			ctxLog.Msg("master account balance")
		}
	}

	// return if not seeding accounts
	if !seed {
		return c
	}
	log.Info().Msg("initializing mocknet simulation user accounts")

	// fund all user accounts
	funded := []*User{}
	for _, user := range c.Users {
		if fundUserThorAccount(master, user) {
			funded = append(funded, user)
		}
	}

	// fund user accounts with one goroutine per chain
	wg = &sync.WaitGroup{}
	for _, chain := range suites.SimChains {
		// determine the amount to seed
		chainSeedAmount := sdkmath.ZeroUint()
		switch chain {
		case common.BTCChain, common.LTCChain, common.BCHChain:
			chainSeedAmount = sdkmath.NewUint(10 * common.One)
		case common.BSCChain, common.BASEChain, common.ETHChain:
			chainSeedAmount = sdkmath.NewUint(100 * common.One)
		case common.GAIAChain:
			chainSeedAmount = sdkmath.NewUint(1000 * common.One)
		case common.AVAXChain, // more since local gas is high
			common.XRPChain: // more since dust threshold is 1 XRP
			chainSeedAmount = sdkmath.NewUint(10000 * common.One)
		case common.DOGEChain:
			chainSeedAmount = sdkmath.NewUint(100000 * common.One)
		default:
			continue // all other chains currently unsupported
		}

		wg.Add(1)
		go func(chain common.Chain, amount sdkmath.Uint) {
			defer wg.Done()
			fundUserChainAccounts(master, funded, chain, chainSeedAmount)
		}(chain, chainSeedAmount)

	}
	wg.Wait()

	return c
}

////////////////////////////////////////////////////////////////////////////////////////
// Helpers
////////////////////////////////////////////////////////////////////////////////////////

// nolint:typecheck
func fundUserChainAccounts(master *User, users []*User, chain common.Chain, amount sdkmath.Uint) {
	for _, user := range users {
		fundUserChainAccount(master, user, chain, amount)
	}
}

// nolint:typecheck
func fundUserChainAccount(master, user *User, chain common.Chain, amount sdkmath.Uint) {
	// build tx
	addr, err := user.PubKey(chain).GetAddress(chain)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get address")
	}
	tx := SimTx{
		Chain:     chain,
		ToAddress: addr,
		Coin:      common.NewCoin(chain.GetGasAsset(), amount),
		Memo:      fmt.Sprintf("SIMULATION:%s", user.Name()),
	}

	// sign tx
	signed, err := master.ChainClients[chain].SignTx(tx)
	if err != nil {
		log.Fatal().Err(err).Stringer("chain", chain).Msg("failed to sign master tx")
	}

	// broadcast tx
	txid, err := master.ChainClients[chain].BroadcastTx(signed)
	if err != nil {
		log.Fatal().Err(err).Interface("tx", tx).Msg("failed to broadcast funding tx")
	}

	amountFloat := float64(amount.Uint64()) / float64(common.One)
	log.Info().
		Str("txid", txid).
		Str("user", user.Name()).
		Stringer("chain", chain).
		Stringer("address", addr).
		Str("amount", fmt.Sprintf("%08f", amountFloat)).
		Msg("account funded")

	// if this is an EVM chain also fund token balances
	if !chain.IsEVM() {
		return
	}

	// fund token balances
	eAddr := ecommon.HexToAddress(addr.String())
	for asset, token := range evm.Tokens(chain) {
		// convert funding amount to token decimals
		factor := big.NewInt(1).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals)), nil)
		tokenAmount := amount.Mul(sdkmath.NewUintFromBigInt(factor))
		tokenAmount = tokenAmount.Quo(sdkmath.NewUint(common.One))

		tokenTx := SimContractTx{
			Chain:    chain,
			Contract: common.Address(token.Address),
			ABI:      evm.ERC20ABI(),
			Method:   "transfer",
			Args:     []interface{}{eAddr, tokenAmount.BigInt()},
		}
		tokenSigned, err := master.ChainClients[chain].(*evm.Client).SignContractTx(tokenTx)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to sign master token tx")
		}
		tokenTxid, err := master.ChainClients[chain].BroadcastTx(tokenSigned)
		if err != nil {
			from, _ := master.PubKey(chain).GetAddress(chain)
			log.Fatal().Err(err).
				Stringer("chain", chain).
				Stringer("from", from).
				Msg("failed to broadcast funding token tx")
		}
		amountFloat := float64(amount.Uint64()) / float64(common.One)
		log.Info().
			Str("txid", tokenTxid).
			Str("account", user.Name()).
			Stringer("asset", asset).
			Stringer("address", addr).
			Str("token", token.Address).
			Str("amount", fmt.Sprintf("%08f", amountFloat)).
			Msg("token balance funded")
	}
}

func fundUserThorAccount(master, user *User) bool {
	masterThorAddress, err := master.PubKey(common.THORChain).GetThorAddress()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get master thor address")
	}

	// skip seeding user if thorchain account has balance
	userThorAddress, err := user.PubKey(common.THORChain).GetAddress(common.THORChain)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get user thor address")
	}
	coins, _ := thornode.GetBalances(userThorAddress)
	if len(coins) > 0 {
		log.Info().Str("account", user.Name()).Msg("user has rune, skipping seed")
		return false
	}

	// seed thorchain account
	userThorAccAddress, err := user.PubKey(common.THORChain).GetThorAddress()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get user thor address")
	}
	seedAmount := sdkmath.NewInt(1000 * common.One)
	seedAmountFloat := float64(seedAmount.Uint64()) / float64(common.One)
	tx := &ttypes.MsgSend{
		FromAddress: masterThorAddress,
		ToAddress:   userThorAccAddress,
		Amount:      sdk.NewCoins(sdk.NewCoin("rune", seedAmount)),
	}
	thorTxid, err := master.Thorchain.Broadcast(tx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to broadcast tx")
	}
	log.Info().
		Stringer("txid", thorTxid).
		Str("account", user.Name()).
		Stringer("chain", common.THORChain).
		Stringer("address", userThorAccAddress).
		Str("amount", fmt.Sprintf("%08f", seedAmountFloat)).
		Msg("account funded")

	return true
}
