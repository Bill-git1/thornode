//go:build !mocknet
// +build !mocknet

package bsctokens

import (
	_ "embed"
)

//go:embed bsc_mainnet_latest.json
var BSCTokenListRawV131 []byte
