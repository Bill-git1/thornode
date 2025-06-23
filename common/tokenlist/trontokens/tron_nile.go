//go:build !mainnet
// +build !mainnet

package trontokens

import _ "embed"

//go:embed tron_nile_latest.json
var TRONTokenListRaw []byte
