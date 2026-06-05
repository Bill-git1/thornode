package main

import (
	crypto_rand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "net/http/pprof"

	"github.com/rivo/tview"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/term"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/features"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/suites"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/cli"
	pkgcosmos "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/dag"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/monero"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/solana"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/tron"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/utxo"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/xrp"
	"gitlab.com/thorchain/thornode/v3/test/simulation/watchers"
)

////////////////////////////////////////////////////////////////////////////////////////
// Config
////////////////////////////////////////////////////////////////////////////////////////

const (
	DefaultParallelism = "8"
	// Clean mocknet-cluster boots can take a few minutes before the TRON fullnode
	// starts relaying stable block production from the supernode.
	tronRPCReadinessTimeout   = 4 * time.Minute
	solanaRPCReadinessTimeout = 4 * time.Minute
	zecRPCReadinessTimeout    = 8 * time.Minute
)

var bifrostReadinessEndpoints = []string{
	"http://localhost:6040/ping",
	"http://localhost:6040/p2pid",
	"http://localhost:9000/metrics",
}

var liteClientConstructors = map[common.Chain]LiteChainClientConstructor{
	common.BTCChain:   utxo.NewConstructor(chainRPCs[common.BTCChain]),
	common.LTCChain:   utxo.NewConstructor(chainRPCs[common.LTCChain]),
	common.BCHChain:   utxo.NewConstructor(chainRPCs[common.BCHChain]),
	common.DOGEChain:  utxo.NewConstructor(chainRPCs[common.DOGEChain]),
	common.ETHChain:   evm.NewConstructor(chainRPCs[common.ETHChain]),
	common.AVAXChain:  evm.NewConstructor(chainRPCs[common.AVAXChain]),
	common.GAIAChain:  pkgcosmos.NewConstructor(chainRPCs[common.GAIAChain]),
	common.BASEChain:  evm.NewConstructor(chainRPCs[common.BASEChain]),
	common.TRONChain:  tron.NewConstructor(chainRPCs[common.TRONChain]),
	common.XRPChain:   xrp.NewConstructor(chainRPCs[common.XRPChain]),
	common.NOBLEChain: pkgcosmos.NewConstructor(chainRPCs[common.NOBLEChain]),
	common.SOLChain:   solana.NewConstructor(chainRPCs[common.SOLChain]),
	common.XMRChain:   monero.NewConstructor(chainRPCs[common.XMRChain]),
	common.POLChain:   evm.NewConstructor(chainRPCs[common.POLChain]),
	common.ZECChain:   utxo.NewConstructor(chainRPCs[common.ZECChain]),
}

type simulationStage struct {
	name        string
	defaultOn   bool
	constructor func(rng *rand.Rand) *Actor
}

func simulationStages() []simulationStage {
	return []simulationStage{
		{name: "seed", defaultOn: true},
		{name: "bootstrap", defaultOn: true, constructor: suites.Bootstrap},
		{name: "xmr-coverage", defaultOn: true, constructor: suites.XMRCoverage},
		{name: "arb", defaultOn: true, constructor: core.NewArbActor},
		{name: "swaps", defaultOn: true, constructor: suites.Swaps},
		{name: "memoless-swaps", defaultOn: true, constructor: suites.MemolessSwaps},
		{name: "consolidate", defaultOn: true, constructor: features.Consolidate},
		{name: "churn", defaultOn: false, constructor: core.NewChurnActor},
		{name: "xmr-post-churn", defaultOn: false, constructor: suites.XMRPostChurn},
		{name: "xmr-spent-refs", defaultOn: false, constructor: suites.XMRSpentRefTracking},
		{name: "forced-to-leave-keygen", defaultOn: false, constructor: core.NewForcedToLeaveKeygenActor},
		{name: "forced-to-leave-keysign", defaultOn: false, constructor: core.NewForcedToLeaveKeysignActor},
		{name: "jailed-keysign-fallback", defaultOn: false, constructor: core.NewJailedKeysignFallbackActor},
		{name: "inactive-vault-refunds", defaultOn: false, constructor: features.InactiveVaultRefunds},
		{name: "solvency", defaultOn: true, constructor: core.NewSolvencyCheckActor},
		{name: "ragnarok", defaultOn: true, constructor: suites.Ragnarok},
	}
}

func simulationStageOptions() []cli.Option {
	stages := simulationStages()
	options := make([]cli.Option, 0, len(stages))
	for _, stage := range stages {
		options = append(options, cli.Option{Name: stage.name, Default: stage.defaultOn})
	}
	return options
}

func appendSimulationStages(root *Actor, enabledStages map[string]bool, rng *rand.Rand) {
	appendIfEnabled := func(key string, constructor func(rng *rand.Rand) *Actor) {
		if constructor == nil {
			return
		}
		if enabledStages[key] || enabledStages["all"] {
			root.Append(constructor(rng))
		}
	}

	for _, stage := range simulationStages() {
		appendIfEnabled(stage.name, stage.constructor)
	}
}

////////////////////////////////////////////////////////////////////////////////////////
// Init
////////////////////////////////////////////////////////////////////////////////////////

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.TimeOnly,
	}).With().Caller().Logger()
}

////////////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////////////

func main() {
	// start pprof server
	go func() {
		log.Info().Msg("starting pprof server on :6061")
		if err := http.ListenAndServe(":6061", nil); err != nil {
			log.Fatal().Err(err).Msg("pprof server failed")
		}
	}()

	// prompt to filter run stages if connected to a terminal
	enabledStages := map[string]bool{}
	stages := simulationStageOptions()
	if os.Getenv("STAGES") != "" {
		for _, stage := range strings.Split(os.Getenv("STAGES"), ",") {
			enabledStages[stage] = true
		}
	} else if term.IsTerminal(int(os.Stdout.Fd())) {
		app := tview.NewApplication()
		opts := cli.NewOptions(app, stages)
		opts.SetBorder(true).SetTitle(" Select Stages ")
		app.SetRoot(opts, true)
		if err := app.Run(); err != nil {
			panic(err)
		}
		enabledStages = opts.Selected()
	} else {
		for _, stage := range stages {
			enabledStages[stage.Name] = stage.Default
		}
	}

	waitForBifrostReadiness()

	if err := waitForTronRPCReadiness(chainRPCs[common.TRONChain]); err != nil {
		log.Fatal().Err(err).Msg("failed waiting for tron rpc readiness")
	}
	if err := waitForSolanaRPCReadiness(chainRPCs[common.SOLChain]); err != nil {
		log.Fatal().Err(err).Msg("failed waiting for solana rpc readiness")
	}
	if err := waitForZecRPCReadiness(chainRPCs[common.ZECChain]); err != nil {
		log.Fatal().Err(err).Msg("failed waiting for zec rpc readiness")
	}

	for _, chain := range []common.Chain{
		common.ETHChain,
		common.AVAXChain,
		common.BASEChain,
		common.POLChain,
	} {
		waitForEVMRPCReadiness(chain, chainRPCs[chain])
	}

	var randSeed [32]byte
	randSeedHex := os.Getenv("RANDOM_SEED_HEX")
	if randSeedHex == "" {
		_, err := io.ReadFull(crypto_rand.Reader, randSeed[:])
		if err != nil {
			log.Fatal().Err(err).Msg("failed to generate random seed")
		}
	} else {
		randSeedBytes, err := hex.DecodeString(randSeedHex)
		if err != nil || len(randSeedBytes) > 32 {
			log.Fatal().Err(err).Msg("failed to parse RANDOM_SEED_HEX")
		}
		for byte_idx, byte_value := range randSeedBytes {
			randSeed[32-len(randSeedBytes)+byte_idx] = byte_value
		}
	}
	log.Info().Msgf("Random seed set to `%s`", new(big.Int).SetBytes(randSeed[:]).Text(16))
	rng := rand.New(rand.NewChaCha8(randSeed))

	// combine all actor dags for the complete test run
	root := NewActor("Root", rng)
	appendSimulationStages(root, enabledStages, rng)

	// gather config from the environment
	parallelism := os.Getenv("PARALLELISM")
	if parallelism == "" {
		parallelism = DefaultParallelism
	}
	parallelismInt, err := strconv.Atoi(parallelism)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to parse PARALLELISM")
	}

	cfg := InitConfig(parallelismInt, enabledStages["seed"] || enabledStages["all"], rng)

	// start watchers
	enabledWatchers := []*Watcher{
		watchers.NewInvariants(),
		watchers.NewSolvencyHalt(),
		watchers.NewSecurityEvents(),
	}
	for _, w := range enabledWatchers {
		log.Info().Str("watcher", w.Name).Msg("starting watcher")
		go func(w *Watcher) {
			err = w.Execute(cfg, log.Output(os.Stderr))
			if err != nil {
				log.Fatal().Err(err).Msg("watcher failed")
			}
		}(w)
	}

	// run the simulation
	dag.Execute(cfg, root, parallelismInt, rng)
}

func waitForBifrostReadiness() {
	waitForBifrostReadinessWithSleep(bifrostReadinessEndpoints, http.Get, time.Sleep)
}

func waitForBifrostReadinessWithSleep(
	endpoints []string,
	get func(string) (*http.Response, error),
	sleep func(time.Duration),
) {
	for {
		for _, endpoint := range endpoints {
			res, err := get(endpoint)
			if err == nil && res.StatusCode == http.StatusOK {
				res.Body.Close()
				return
			}
			if res != nil {
				res.Body.Close()
			}
		}

		log.Info().Msg("waiting for bifrost to be ready")
		sleep(time.Second)
	}
}

func waitForEVMRPCReadiness(chain common.Chain, endpoint string) {
	for {
		res, err := http.Post(
			endpoint,
			"application/json",
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}`),
		)
		if err == nil && res.StatusCode == http.StatusOK {
			body, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr == nil && strings.Contains(string(body), `"result"`) {
				return
			}
		} else if res != nil {
			res.Body.Close()
		}

		log.Info().Stringer("chain", chain).Msg("waiting for evm chain to be ready")
		time.Sleep(time.Second)
	}
}

func waitForTronRPCReadiness(endpoint string) error {
	return waitForTronRPCReadinessWithClock(endpoint, getTronBlockHeight, time.Now, time.Sleep, tronRPCReadinessTimeout)
}

func waitForTronRPCReadinessWithClock(
	endpoint string,
	getHeight func(string) (int64, error),
	now func() time.Time,
	sleep func(time.Duration),
	timeout time.Duration,
) error {
	const requiredHeight int64 = 20
	const requiredAdvances = 3

	lastHeight := int64(-1)
	advances := 0
	deadline := now().Add(timeout)

	for {
		if now().After(deadline) {
			return fmt.Errorf("tron readiness timed out after %s with height=%d advances=%d", timeout, lastHeight, advances)
		}

		height, err := getHeight(endpoint)
		switch {
		case err == nil && height >= requiredHeight && lastHeight >= 0 && height > lastHeight:
			advances++
			lastHeight = height
			if advances >= requiredAdvances {
				return nil
			}
		case err == nil && lastHeight < 0:
			lastHeight = height
		case err == nil && height < lastHeight:
			lastHeight = height
			advances = 0
		case err == nil:
			lastHeight = height
		}

		log.Info().
			Int64("height", lastHeight).
			Int("advances", advances).
			Msg("waiting for tron to be ready")
		sleep(time.Second)
	}
}

func getTronBlockHeight(endpoint string) (int64, error) {
	res, err := http.Post(
		endpoint+"/wallet/getnowblock",
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected tron status code: %d", res.StatusCode)
	}

	var response struct {
		Header struct {
			RawData struct {
				Number int64 `json:"number"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}

	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return 0, err
	}

	return response.Header.RawData.Number, nil
}

func waitForSolanaRPCReadiness(endpoint string) error {
	return waitForSolanaRPCReadinessWithClock(endpoint, getSolanaSlot, time.Now, time.Sleep, solanaRPCReadinessTimeout)
}

func waitForSolanaRPCReadinessWithClock(
	endpoint string,
	getSlot func(string) (uint64, error),
	now func() time.Time,
	sleep func(time.Duration),
	timeout time.Duration,
) error {
	const requiredAdvances = 2

	lastSlot := uint64(0)
	haveSlot := false
	advances := 0
	deadline := now().Add(timeout)

	for {
		if now().After(deadline) {
			return fmt.Errorf("solana readiness timed out after %s with slot=%d advances=%d", timeout, lastSlot, advances)
		}

		slot, err := getSlot(endpoint)
		switch {
		case err == nil && !haveSlot:
			lastSlot = slot
			haveSlot = true
		case err == nil && slot > lastSlot:
			advances++
			lastSlot = slot
			if advances >= requiredAdvances {
				return nil
			}
		case err == nil && slot < lastSlot:
			lastSlot = slot
			advances = 0
		case err == nil:
			lastSlot = slot
		}

		log.Info().
			Uint64("slot", lastSlot).
			Int("advances", advances).
			Msg("waiting for solana to be ready")
		sleep(time.Second)
	}
}

func getSolanaSlot(endpoint string) (uint64, error) {
	res, err := http.Post(
		endpoint,
		"application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"getSlot","params":[]}`),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected solana status code: %d", res.StatusCode)
	}

	var response struct {
		Result uint64 `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return 0, err
	}
	if response.Error != nil {
		return 0, fmt.Errorf("solana rpc error %d: %s", response.Error.Code, response.Error.Message)
	}

	return response.Result, nil
}

func waitForZecRPCReadiness(endpoint string) error {
	return waitForZecRPCReadinessWithClock(endpoint, getZecBlockCount, time.Now, time.Sleep, zecRPCReadinessTimeout)
}

func waitForZecRPCReadinessWithClock(
	endpoint string,
	getBlockCount func(string) (int64, error),
	now func() time.Time,
	sleep func(time.Duration),
	timeout time.Duration,
) error {
	const requiredHeight int64 = 250
	const requiredAdvances = 1

	lastHeight := int64(-1)
	advances := 0
	deadline := now().Add(timeout)

	for {
		if now().After(deadline) {
			return fmt.Errorf("zec readiness timed out after %s with height=%d advances=%d", timeout, lastHeight, advances)
		}

		height, err := getBlockCount(endpoint)
		switch {
		case err == nil && height >= requiredHeight && lastHeight >= 0 && height > lastHeight:
			advances++
			lastHeight = height
			if advances >= requiredAdvances {
				return nil
			}
		case err == nil && lastHeight < 0:
			lastHeight = height
		case err == nil && height < lastHeight:
			lastHeight = height
			advances = 0
		case err == nil:
			lastHeight = height
		}

		log.Info().
			Int64("height", lastHeight).
			Int("advances", advances).
			Msg("waiting for zec to be ready")
		sleep(time.Second)
	}
}

func getZecBlockCount(endpoint string) (int64, error) {
	res, err := http.Post(
		endpoint,
		"application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"getblockcount","params":[]}`),
	)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected zec status code: %d", res.StatusCode)
	}

	var response struct {
		Result int64 `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		return 0, err
	}
	if response.Error != nil {
		return 0, fmt.Errorf("zec rpc error %d: %s", response.Error.Code, response.Error.Message)
	}

	return response.Result, nil
}
