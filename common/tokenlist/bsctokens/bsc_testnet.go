//go:build testnet || mocknet
// +build testnet mocknet

package bsctokens

import _ "embed"

//go:embed bsc_testnet_V111.json
var BSCTokenListRawV111 []byte

//go:embed bsc_testnet_latest.json
var BSCTokenListRawV122 []byte
