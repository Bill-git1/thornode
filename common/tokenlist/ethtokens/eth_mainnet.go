//go:build !mocknet
// +build !mocknet

package ethtokens

import (
	_ "embed"
)

//go:embed eth_mainnet_latest.json
var ETHTokenListRawV133 []byte

//go:embed eth_mainnet_V128.json
var ETHTokenListRawV128 []byte
