package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common/tokenlist/avaxtokens"
)

var (
	avaxTokenListV131 EVMTokenList
	avaxTokenListV137 EVMTokenList
)

func init() {
	if err := json.Unmarshal(avaxtokens.AVAXTokenListRawV131, &avaxTokenListV131); err != nil {
		panic(err)
	}

	if err := json.Unmarshal(avaxtokens.AVAXTokenListRawV137, &avaxTokenListV137); err != nil {
		panic(err)
	}
}

func GetAVAXTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("2.137.0")):
		return avaxTokenListV137
	case version.GTE(semver.MustParse("1.131.0")):
		return avaxTokenListV131
	default:
		return EVMTokenList{}
	}
}
