package tokenlist

import (
	"encoding/json"

	"github.com/blang/semver"
	"gitlab.com/thorchain/thornode/common/tokenlist/ethtokens"
)

var (
	ethTokenListV133 EVMTokenList
	ethTokenListV137 EVMTokenList
)

func init() {
	if err := json.Unmarshal(ethtokens.ETHTokenListRawV133, &ethTokenListV133); err != nil {
		panic(err)
	}

	if err := json.Unmarshal(ethtokens.ETHTokenListRawV137, &ethTokenListV137); err != nil {
		panic(err)
	}
}

func GetETHTokenList(version semver.Version) EVMTokenList {
	switch {
	case version.GTE(semver.MustParse("2.137.0")):
		return ethTokenListV137
	case version.GTE(semver.MustParse("1.133.0")):
		return ethTokenListV133
	default:
		return EVMTokenList{}
	}
}
