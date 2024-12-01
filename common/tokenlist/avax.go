package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/v3/common/tokenlist/avaxtokens"
)

var avaxTokenListV3_0_0 EVMTokenList

func init() {
	if err := json.Unmarshal(avaxtokens.AVAXTokenListRawV3_0_0, &avaxTokenListV3_0_0); err != nil {
		panic(err)
	}
}

func GetAVAXTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return avaxTokenListV3_0_0
	default:
		return EVMTokenList{}
	}
}
