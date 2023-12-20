//go:build !stagenet && !mocknet
// +build !stagenet,!mocknet

package bsctokens

import (
	_ "embed"
)

//go:embed bsc_mainnet_V111.json
var BSCTokenListRawV111 []byte

//go:embed bsc_mainnet_latest.json
var BSCTokenListRawV122 []byte
