package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common/tokenlist/bsctokens"
)

var bscTokenListV131 EVMTokenList

func init() {
	if err := json.Unmarshal(bsctokens.BSCTokenListRawV131, &bscTokenListV131); err != nil {
		panic(err)
	}
}

func GetBSCTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("1.131.0")):
		return bscTokenListV131
	default:
		return EVMTokenList{}
	}
}
