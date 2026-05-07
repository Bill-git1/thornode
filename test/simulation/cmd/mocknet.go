package main

import (
	"fmt"
	"math/big"
	rand "math/rand/v2"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	ecommon "github.com/ethereum/go-ethereum/common"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	acommon "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/monero"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/thornode"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	ttypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

////////////////////////////////////////////////////////////////////////////////////////
// Chain RPCs
////////////////////////////////////////////////////////////////////////////////////////

var chainRPCs = map[common.Chain]string{
	common.BTCChain:   "http://127.0.0.1:18443",
	common.LTCChain:   "http://127.0.0.1:38443",
	common.BCHChain:   "http://127.0.0.1:28443",
	common.DOGEChain:  "http://127.0.0.1:18332",
	common.ZECChain:   "http://127.0.0.1:18232",
	common.ETHChain:   "http://127.0.0.1:8545",
	common.AVAXChain:  "http://127.0.0.1:9650/ext/bc/C/rpc",
	common.GAIAChain:  "127.0.0.1:9091",
	common.NOBLEChain: "127.0.0.1:9092",
	common.BASEChain:  "http://127.0.0.1:8547",
	common.POLChain:   "http://127.0.0.1:8548",
	common.TRONChain:  "http://127.0.0.1:8090",
	common.XRPChain:   "http://127.0.0.1:5005",
	common.SOLChain:   "http://127.0.0.1:8899",
	common.XMRChain:   envOrDefault("XMR_SIM_HELPER_ENDPOINT", "http://127.0.0.1:8284"),
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

const (
	masterAccountRetryAttempts = 30
	masterAccountRetryDelay    = 2 * time.Second
	mocknetInitConcurrency     = 2
)

////////////////////////////////////////////////////////////////////////////////////////
// Init
////////////////////////////////////////////////////////////////////////////////////////

func InitConfig(parallelism int, seed bool, rng *rand.Rand) *OpConfig {
	userCount := parallelism
	if userCount < 2 {
		log.Info().
			Int("parallelism", parallelism).
			Int("user_count", 2).
			Msg("allocating spare simulation user for background actors")
		userCount = 2
	}
	if userCount > len(mocknetUserMnemonics) {
		log.Error().
			Int("parallelism", userCount).
			Int("accounts", len(mocknetUserMnemonics)).
			Msg("parallelism limited by available user accounts")
		userCount = len(mocknetUserMnemonics)
	}

	c := &OpConfig{
		NodeUsers: make([]*User, len(mocknetValidatorMnemonics)),
	}
	mu := &sync.Mutex{}
	wg := &sync.WaitGroup{}
	// Creating a simulation user instantiates every chain client for that mnemonic.
	// Keep the startup fanout low so cluster runs do not spike Docker memory and
	// lose the sim harness to exit 137 before seed/bootstrap even begins.
	sem := make(chan struct{}, mocknetInitConcurrency)

	// since we reuse the bifrost thorclient, load endpoints into config package
	os.Setenv("BIFROST_THORCHAIN_CHAIN_HOST", "127.0.0.1:1317")
	os.Setenv("BIFROST_THORCHAIN_CHAIN_RPC", "127.0.0.1:26657")
	os.Setenv("BIFROST_THORCHAIN_CHAIN_EBIFROST", "127.0.0.1:50051")
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

			// default network fees on chains needing a window of blocks before bifrost sends.
			// Cosmos-family chains currently use a 200k gas limit with a 0.001 min gas
			// price in mocknet, so 200 uatom/uusdc is the minimum accepted fee.
			defaultFees := []struct {
				chain common.Chain
				size  uint64
				rate  uint64
			}{
				{common.GAIAChain, 1, 20_000},
				{common.NOBLEChain, 1, 20_000},
				{common.XRPChain, 1, 10},
				{common.AVAXChain, 80000, 1500},
				{common.BASEChain, 80000, 300000},
				{common.POLChain, 80000, 1500},
				{common.ETHChain, 80000, 300},
				{common.SOLChain, 1, 5000},
				{common.BTCChain, 1000, 4},
				{common.ZECChain, 1, 50_000},
				{common.LTCChain, 250, 103},
				{common.BCHChain, 1500, 7},
				{common.DOGEChain, 1000, 1500000},
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
	for _, mnemonic := range mocknetUserMnemonics[:userCount] {
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
	// sort users deterministically
	mu.Lock()
	sort.Slice(c.Users, func(ux_idx, uy_idx int) bool {
		return c.Users[ux_idx].Name() < c.Users[uy_idx].Name()
	})
	rng.Shuffle(len(c.Users), func(ux_idx, uy_idx int) {
		ux := c.Users[ux_idx]
		c.Users[ux_idx] = c.Users[uy_idx]
		c.Users[uy_idx] = ux
	})
	mu.Unlock()

	// fund all user accounts from master
	master := NewUser(mocknetMasterMnemonic, liteClientConstructors)

	// sort chains deterministically
	chains := make([]common.Chain, 0, len(liteClientConstructors))
	for chain := range liteClientConstructors {
		chains = append(chains, chain)
	}
	sort.Slice(chains, func(cx_idx, cy_idx int) bool {
		return chains[cx_idx] < chains[cy_idx]
	})
	rng.Shuffle(len(chains), func(cx_idx, cy_idx int) {
		cx := chains[cx_idx]
		chains[cx_idx] = chains[cy_idx]
		chains[cy_idx] = cx
	})

	// log all configured tokens, their decimals, and master balance
	for _, chain := range chains {
		account, err := waitForMasterAccount(chain, master.ChainClients[chain])
		if err != nil {
			log.Fatal().Stringer("chain", chain).Err(err).Msg("failed to get master account")
		}
		for _, coin := range account.Coins {
			ctxLog := log.Info().
				Stringer("chain", chain).
				Stringer("asset", coin.Asset).
				Stringer("address", master.Address(chain)).
				Str("amount", coin.Amount.String())

			// on evm chains, also log decimals for debugging
			if chain.IsEVM() {
				if coin.Asset.IsGasAsset() {
					ctxLog = ctxLog.Int64("decimals", chain.GetGasAssetDecimal())
				} else {
					token, ok := evm.Tokens(chain)[coin.Asset]
					if !ok {
						log.Fatal().
							Stringer("chain", chain).
							Stringer("asset", coin.Asset).
							Msg("missing configured token metadata")
					}

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
	for _, chain := range acommon.SimChains {
		// determine the amount to seed
		var chainSeedAmount sdkmath.Uint
		// determine the amount of txs for seeding, to create multiple utxos
		chainSeedTxs := 1
		switch chain {
		case common.BTCChain, common.LTCChain, common.BCHChain:
			chainSeedAmount = sdkmath.NewUint(10 * common.One)
		case common.BSCChain, common.BASEChain, common.ETHChain, common.SOLChain:
			chainSeedAmount = sdkmath.NewUint(100 * common.One)
		case common.ZECChain:
			chainSeedAmount = sdkmath.NewUint(100 * common.One)
			chainSeedTxs = 20
		case common.GAIAChain, common.NOBLEChain:
			chainSeedAmount = sdkmath.NewUint(1000 * common.One)
		case common.AVAXChain, // more since local gas is high
			common.XRPChain: // more since dust threshold is 1 XRP
			chainSeedAmount = sdkmath.NewUint(10000 * common.One)
		case common.DOGEChain:
			chainSeedAmount = sdkmath.NewUint(100000 * common.One)
		case common.TRONChain:
			chainSeedAmount = sdkmath.NewUint(10000 * common.One)
		case common.POLChain:
			chainSeedAmount = sdkmath.NewUint(1_000_000 * common.One)
		case common.XMRChain:
			chainSeedAmount = sdkmath.NewUint(1000 * common.One)
		default:
			continue // all other chains currently unsupported
		}

		if chain.Equals(common.XMRChain) {
			fundUserXMRAccounts(master, funded, chainSeedAmount)
			continue
		}

		for i := 0; i < chainSeedTxs; i++ {
			wg.Add(1)
			go func(chain common.Chain, amount sdkmath.Uint) {
				defer wg.Done()
				fundUserChainAccounts(master, funded, chain, amount)
			}(chain, chainSeedAmount.QuoUint64(uint64(chainSeedTxs)))
		}

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
	addr, err := user.PubKey(chain).GetAddress(chain)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get address")
	}

	userAccount, err := user.ChainClients[chain].GetAccount(nil)
	if err != nil {
		log.Fatal().Err(err).Stringer("chain", chain).Msg("failed to get user account")
	}

	currentAmount := userAccount.Coins.GetCoin(chain.GetGasAsset()).Amount
	if currentAmount.GTE(amount) {
		log.Info().
			Str("user", user.Name()).
			Stringer("chain", chain).
			Stringer("address", addr).
			Stringer("balance", currentAmount).
			Stringer("target", amount).
			Msg("account already funded")
	} else {
		fundingAmount := amount.Sub(currentAmount)
		tx := SimTx{
			Chain:     chain,
			ToAddress: addr,
			Coin:      common.NewCoin(chain.GetGasAsset(), fundingAmount),
			Memo:      fmt.Sprintf("SIMULATION:%s", user.Name()),
		}

		txid, err := SignAndBroadcastTx(master.ChainClients[chain], tx)
		if err != nil {
			log.Fatal().Err(err).Interface("tx", tx).Msg("failed to broadcast funding tx")
		}

		amountFloat := float64(fundingAmount.Uint64()) / float64(common.One)
		log.Info().
			Str("txid", txid).
			Str("user", user.Name()).
			Stringer("chain", chain).
			Stringer("address", addr).
			Str("amount", fmt.Sprintf("%08f", amountFloat)).
			Msg("account funded")
	}

	// if this is an EVM chain also fund token balances
	if !chain.IsEVM() {
		return
	}

	// fund token balances
	eAddr := ecommon.HexToAddress(addr.String())
	for asset, token := range evm.Tokens(chain) {
		// Cap EVM token seeding so the master mocknet inventory, especially on AVAX,
		// can cover every simulation user without later ERC-20 transfers reverting.
		targetTokenAmount := sdkmath.NewUint(5_000 * common.One)
		if amount.LT(targetTokenAmount) {
			targetTokenAmount = amount
		}

		currentTokenAmount := userAccount.Coins.GetCoin(asset).Amount
		if currentTokenAmount.GTE(targetTokenAmount) {
			log.Info().
				Str("account", user.Name()).
				Stringer("asset", asset).
				Stringer("balance", currentTokenAmount).
				Stringer("target", targetTokenAmount).
				Msg("token balance already funded")
			continue
		}
		tokenFundingAmount := targetTokenAmount.Sub(currentTokenAmount)

		// convert funding amount to token decimals
		factor := big.NewInt(1).Exp(big.NewInt(10), big.NewInt(int64(token.Decimals)), nil)
		tokenAmount := tokenFundingAmount.Mul(sdkmath.NewUintFromBigInt(factor))
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
		amountFloat := float64(tokenFundingAmount.Uint64()) / float64(common.One)
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

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func thorUserFundingPlan(coins common.Coins) (seedThor bool, seedChains bool) {
	if len(coins) > 0 {
		return false, true
	}
	return true, true
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
	seedThor, seedChains := thorUserFundingPlan(coins)
	if !seedThor {
		log.Info().Str("account", user.Name()).Msg("user has rune, skipping seed")
		return seedChains
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

	return seedChains
}

func fundUserXMRAccounts(master *User, users []*User, amount sdkmath.Uint) {
	client, ok := master.ChainClients[common.XMRChain].(*monero.Client)
	if !ok {
		log.Fatal().Msg("failed to get monero simulation client")
	}

	for _, user := range users {
		addr, err := user.PubKey(common.XMRChain).GetAddress(common.XMRChain)
		if err != nil {
			log.Fatal().Err(err).Msg("failed to get XMR address")
		}

		txid, err := client.Faucet(addr, amount)
		if err != nil {
			log.Fatal().Err(err).Stringer("address", addr).Msg("failed to faucet XMR simulation account")
		}

		amountFloat := float64(amount.Uint64()) / float64(common.One)
		log.Info().
			Str("txid", txid).
			Str("user", user.Name()).
			Stringer("chain", common.XMRChain).
			Stringer("address", addr).
			Str("amount", fmt.Sprintf("%08f", amountFloat)).
			Msg("account funded")
	}

	for _, user := range users {
		for {
			acct, err := user.ChainClients[common.XMRChain].GetAccount(nil)
			if err == nil && acct.Coins.GetCoin(common.XMRAsset).Amount.GTE(amount) {
				break
			}

			if err != nil {
				log.Warn().Err(err).Str("user", user.Name()).Msg("waiting for XMR simulation account balance")
			} else {
				log.Info().
					Str("user", user.Name()).
					Stringer("balance", acct.Coins.GetCoin(common.XMRAsset).Amount).
					Stringer("target", amount).
					Msg("waiting for XMR funds to unlock")
			}

			time.Sleep(2 * time.Second)
		}
	}
}

func waitForMasterAccount(chain common.Chain, client LiteChainClient) (*common.Account, error) {
	return waitForMasterAccountWithSleep(chain, client, time.Sleep)
}

func waitForMasterAccountWithSleep(
	chain common.Chain,
	client LiteChainClient,
	sleep func(time.Duration),
) (*common.Account, error) {
	var lastErr error
	for attempt := 1; attempt <= masterAccountRetryAttempts; attempt++ {
		account, err := client.GetAccount(nil)
		if err == nil && isMasterAccountReady(chain, account) {
			return account, nil
		}
		if err == nil {
			lastErr = fmt.Errorf("%s master account balance is still zero", chain)
			if !isRetryableZeroBalanceStartupState(chain, account) || attempt == masterAccountRetryAttempts {
				return nil, lastErr
			}
		} else {
			lastErr = err
			if !isRetryableMasterAccountStartupError(chain, err) || attempt == masterAccountRetryAttempts {
				return nil, err
			}
		}

		log.Warn().
			Stringer("chain", chain).
			Int("attempt", attempt).
			Int("max_attempts", masterAccountRetryAttempts).
			Err(lastErr).
			Msg("waiting for master account readiness")
		sleep(masterAccountRetryDelay)
	}

	return nil, lastErr
}

func isMasterAccountReady(chain common.Chain, account *common.Account) bool {
	if account == nil {
		return false
	}

	if requiresFundedMasterAccount(chain) {
		return !account.Coins.GetCoin(chain.GetGasAsset()).Amount.IsZero()
	}

	return true
}

func requiresFundedMasterAccount(chain common.Chain) bool {
	return chain.Equals(common.XRPChain) || chain.Equals(common.SOLChain)
}

func isRetryableZeroBalanceStartupState(chain common.Chain, account *common.Account) bool {
	return requiresFundedMasterAccount(chain) && account != nil &&
		account.Coins.GetCoin(chain.GetGasAsset()).Amount.IsZero()
}

func isRetryableMasterAccountStartupError(chain common.Chain, err error) bool {
	return isRetryableXMRStartupError(chain, err) || isRetryableTransportStartupError(err)
}

func isRetryableXMRStartupError(chain common.Chain, err error) bool {
	if err == nil || !chain.Equals(common.XMRChain) {
		return false
	}

	msg := err.Error()
	return (strings.Contains(msg, "failed to fetch scannable block 1") &&
		strings.Contains(msg, "Requested block height: 1 greater than current top block height: 0")) ||
		strings.Contains(msg, "failed to query daemon height:") ||
		strings.Contains(msg, "daemon RPC get_info failed")
}

func isRetryableTransportStartupError(err error) bool {
	if err == nil {
		return false
	}

	msg := err.Error()
	for _, snippet := range []string{
		"context deadline exceeded",
		"connect: connection refused",
		"connection reset by peer",
		"EOF",
		"i/o timeout",
	} {
		if strings.Contains(msg, snippet) {
			return true
		}
	}

	return false
}
