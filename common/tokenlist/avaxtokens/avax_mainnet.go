//go:build !mocknet
// +build !mocknet

package avaxtokens

import (
	_ "embed"
)

//go:embed avax_mainnet_latest.json
var AVAXTokenListRawV137 []byte

//go:embed avax_mainnet_V131.json
var AVAXTokenListRawV131 []byte
