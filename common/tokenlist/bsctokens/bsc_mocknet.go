//go:build mocknet
// +build mocknet

package bsctokens

import _ "embed"

//go:embed bsc_mocknet_latest.json
var BSCTokenListRawV3_0_0 []byte
