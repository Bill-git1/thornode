package main

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rivo/tview"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/term"

	prefix "gitlab.com/thorchain/thornode/v3/cmd"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/features"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/suites"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/cli"
	pkgcosmos "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/cosmos"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/dag"
	"gitlab.com/thorchain/thornode/v3/test/simulation/pkg/evm"
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
	common.BTCChain:  utxo.NewConstructor(chainRPCs[common.BTCChain]),
	common.LTCChain:  utxo.NewConstructor(chainRPCs[common.LTCChain]),
	common.BCHChain:  utxo.NewConstructor(chainRPCs[common.BCHChain]),
	common.DOGEChain: utxo.NewConstructor(chainRPCs[common.DOGEChain]),
	common.ETHChain:  evm.NewConstructor(chainRPCs[common.ETHChain]),
	common.AVAXChain: evm.NewConstructor(chainRPCs[common.AVAXChain]),
	common.GAIAChain: pkgcosmos.NewConstructor(chainRPCs[common.GAIAChain]),
	common.BASEChain: evm.NewConstructor(chainRPCs[common.BASEChain]),
	common.XRPChain:  xrp.NewConstructor(chainRPCs[common.XRPChain]),
}

////////////////////////////////////////////////////////////////////////////////////////
// Init
////////////////////////////////////////////////////////////////////////////////////////

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.TimeOnly,
	}).With().Caller().Logger()

	// init prefixes
	ccfg := cosmos.GetConfig()
	ccfg.SetBech32PrefixForAccount(prefix.Bech32PrefixAccAddr, prefix.Bech32PrefixAccPub)
	ccfg.SetBech32PrefixForValidator(prefix.Bech32PrefixValAddr, prefix.Bech32PrefixValPub)
	ccfg.SetBech32PrefixForConsensusNode(prefix.Bech32PrefixConsAddr, prefix.Bech32PrefixConsPub)
	ccfg.SetCoinType(prefix.THORChainCoinType)
	ccfg.SetPurpose(prefix.THORChainCoinPurpose)
	ccfg.Seal()
}

////////////////////////////////////////////////////////////////////////////////////////
// Main
////////////////////////////////////////////////////////////////////////////////////////

func main() {
	// prompt to filter run stages if connected to a terminal
	enabledStages := map[string]bool{}
	stages := []cli.Option{
		{Name: "seed", Default: true},
		{Name: "bootstrap", Default: true},
		{Name: "arb", Default: true},
		{Name: "swaps", Default: true},
		{Name: "consolidate", Default: true},
		{Name: "churn", Default: false},
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

	// combine all actor dags for the complete test run
	root := NewActor("Root")

	appendIfEnabled := func(key string, constructor func() *Actor) {
		if enabledStages[key] || enabledStages["all"] {
			root.Append(constructor())
		}
	}
	appendIfEnabled("bootstrap", suites.Bootstrap)
	appendIfEnabled("arb", core.NewArbActor)
	appendIfEnabled("swaps", suites.Swaps)
	appendIfEnabled("consolidate", features.Consolidate)
	appendIfEnabled("churn", core.NewChurnActor)
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

	cfg := InitConfig(parallelismInt, enabledStages["seed"] || enabledStages["all"])

	// start watchers
	for _, w := range []*Watcher{watchers.NewInvariants()} {
		log.Info().Str("watcher", w.Name).Msg("starting watcher")
		go func(w *Watcher) {
			err = w.Execute(cfg, log.Output(os.Stderr))
			if err != nil {
				log.Fatal().Err(err).Msg("watcher failed")
			}
		}(w)
	}

	// run the simulation
	dag.Execute(cfg, root, parallelismInt)
}
