package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist/ethtokens"
)

var ethTokenListV3_0_0 EVMTokenList

func init() {
	if err := json.Unmarshal(ethtokens.ETHTokenListRawV3_0_0, &ethTokenListV3_0_0); err != nil {
		panic(err)
	}
}

func GetETHTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return ethTokenListV3_0_0
	default:
		return EVMTokenList{}
	}
}
