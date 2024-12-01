package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist"
)

// -------------------------------------------------------------------------------------
// Flags
// -------------------------------------------------------------------------------------

var flagVersion *string

func init() {
	flagVersion = flag.String("version", "", "current version allowing changes")
}

// -------------------------------------------------------------------------------------
// Check
// -------------------------------------------------------------------------------------

func check(chain common.Chain) {
	// write all token lists to stdout
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	version, err := semver.Parse("3.0.0") // TODO: bump on hard fork
	if err != nil {
		panic(err)
	}

	currentVersion, err := semver.Parse(*flagVersion)
	if err != nil {
		panic(err)
	}

	for {
		fmt.Println("Check:", chain, version)

		// get token list
		err = enc.Encode(tokenlist.GetEVMTokenList(chain, version))
		if err != nil {
			panic(err)
		}

		// iterate versions up to current
		version.Minor++

		// TODO bump major at last minor version at hard fork

		if version.GTE(currentVersion) {
			break
		}
	}
}

// -------------------------------------------------------------------------------------
// Main
// -------------------------------------------------------------------------------------

func main() {
	flag.Parse()
	if *flagVersion == "" {
		panic("version is required")
	}

	for _, chain := range common.AllChains {
		if chain.IsEVM() {
			check(chain)
		}
	}
}
