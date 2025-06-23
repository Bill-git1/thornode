//go:build mainnet
// +build mainnet

package trontokens

import (
	_ "embed"
)

//go:embed tron_mainnet_latest.json
var TRONTokenListRaw []byte
