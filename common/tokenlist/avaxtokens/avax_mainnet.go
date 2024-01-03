//go:build !stagenet && !mocknet
// +build !stagenet,!mocknet

package avaxtokens

import (
	_ "embed"
)

//go:embed avax_mainnet_V95.json
var AVAXTokenListRawV95 []byte

//go:embed avax_mainnet_V101.json
var AVAXTokenListRawV101 []byte

//go:embed avax_mainnet_latest.json
var AVAXTokenListRawV126 []byte
