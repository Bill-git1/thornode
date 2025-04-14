//go:build !mocknet && !stagenet
// +build !mocknet,!stagenet

package tcyclaimlist

import (
	_ "embed"
)

//go:embed tcy_claimers_mainnet.json
var TCYClaimsListRaw []byte
