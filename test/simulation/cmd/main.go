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
)

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
	common.POLChain:   evm.NewConstructor(chainRPCs[common.POLChain]),
	common.ZECChain:   utxo.NewConstructor(chainRPCs[common.ZECChain]),
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
	stages := []cli.Option{
		{Name: "seed", Default: true},
		{Name: "bootstrap", Default: true},
		{Name: "arb", Default: true},
		{Name: "swaps", Default: true},
		{Name: "memoless-swaps", Default: true},
		{Name: "consolidate", Default: true},
		{Name: "churn", Default: false},
		{Name: "inactive-vault-refunds", Default: false},
		{Name: "solvency", Default: true},
		{Name: "ragnarok", Default: true},
	}
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

	// wait until bifrost is ready
	for {
		res, err := http.Get("http://localhost:6040/p2pid")
		if err == nil && res.StatusCode == 200 {
			break
		}
		log.Info().Msg("waiting for bifrost to be ready")
		time.Sleep(time.Second)
	}

	// wait for tron fullnode to sync to a fresh tip with an active peer
	tronReadyMaxTipAge := 30 * time.Second
	for {
		height, tipAge, peers, err := getTronFullnodeStatus()
		if err == nil && tipAge >= 0 && tipAge < tronReadyMaxTipAge && peers > 0 {
			break
		}

		log.Info().
			Err(err).
			Int64("height", height).
			Dur("tip_age", tipAge).
			Int64("peers", peers).
			Msg("waiting for tron fullnode to sync")
		time.Sleep(time.Second)
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

	appendIfEnabled := func(key string, constructor func(rng *rand.Rand) *Actor) {
		if enabledStages[key] || enabledStages["all"] {
			root.Append(constructor(rng))
		}
	}
	appendIfEnabled("bootstrap", suites.Bootstrap)
	appendIfEnabled("arb", core.NewArbActor)
	appendIfEnabled("swaps", suites.Swaps)
	appendIfEnabled("memoless-swaps", suites.MemolessSwaps)
	appendIfEnabled("consolidate", features.Consolidate)
	appendIfEnabled("churn", core.NewChurnActor)
	appendIfEnabled("inactive-vault-refunds", features.InactiveVaultRefunds)
	appendIfEnabled("solvency", core.NewSolvencyCheckActor)
	appendIfEnabled("ragnarok", suites.Ragnarok)

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

func getTronFullnodeStatus() (int64, time.Duration, int64, error) {
	postJSON := func(path string, out any) error {
		res, err := http.Post(
			"http://localhost:8090/wallet/"+path,
			"application/json",
			strings.NewReader(`{}`),
		)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%s returned status code: %d", path, res.StatusCode)
		}
		return json.NewDecoder(res.Body).Decode(out)
	}

	var block struct {
		BlockHeader struct {
			RawData struct {
				Number    int64 `json:"number"`
				Timestamp int64 `json:"timestamp"`
			} `json:"raw_data"`
		} `json:"block_header"`
	}
	if err := postJSON("getnowblock", &block); err != nil {
		return 0, 0, 0, err
	}
	if block.BlockHeader.RawData.Timestamp == 0 {
		return 0, 0, 0, fmt.Errorf("missing block timestamp")
	}

	tipTime := time.UnixMilli(block.BlockHeader.RawData.Timestamp)
	height := block.BlockHeader.RawData.Number
	tipAge := time.Since(tipTime)

	var node struct {
		CurrentConnectCount int64 `json:"currentConnectCount"`
	}
	if err := postJSON("getnodeinfo", &node); err != nil {
		return 0, 0, 0, err
	}
	peers := node.CurrentConnectCount

	return height, tipAge, peers, nil
}
