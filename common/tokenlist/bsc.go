package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist/bsctokens"
)

var bscTokenListV3_0_0 EVMTokenList

func init() {
	if err := json.Unmarshal(bsctokens.BSCTokenListRawV3_0_0, &bscTokenListV3_0_0); err != nil {
		panic(err)
	}
}

func GetBSCTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return bscTokenListV3_0_0
	default:
		return EVMTokenList{}
	}
}
