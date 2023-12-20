//go:build mocknet
// +build mocknet

package avaxtokens

import (
	_ "embed"
)

//go:embed avax_mocknet_V95.json
var AVAXTokenListRawV95 []byte

//go:embed avax_mocknet_latest.json
var AVAXTokenListRawV101 []byte
