//go:build mocknet
// +build mocknet

package avaxtokens

import (
	_ "embed"
)

//go:embed avax_mocknet_latest.json
var AVAXTokenListRawV3_0_0 []byte
