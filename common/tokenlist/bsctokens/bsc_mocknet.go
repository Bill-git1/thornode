//go:build mocknet
// +build mocknet

package bsctokens

import _ "embed"

//go:embed bsc_mocknet_latest.json
var BSCTokenListRawV137 []byte

//go:embed bsc_mocknet_latest.json
var BSCTokenListRawV131 []byte
