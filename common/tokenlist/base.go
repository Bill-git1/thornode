package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist/basetokens"
)

var baseTokenListV3_1_0 EVMTokenList

func init() {
	if err := json.Unmarshal(basetokens.BASETokenListRawV3_1_0, &baseTokenListV3_1_0); err != nil {
		panic(err)
	}
}

func GetBASETokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("3.1.0")):
		return baseTokenListV3_1_0
	default:
		return EVMTokenList{}
	}
}
