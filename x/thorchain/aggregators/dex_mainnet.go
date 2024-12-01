//go:build !mocknet && !stagenet
// +build !mocknet,!stagenet

package aggregators

import (
	"github.com/blang/semver"
)

func DexAggregators(version semver.Version) []Aggregator {
	switch {
	case version.GTE(semver.MustParse("3.0.0")):
		return DexAggregatorsV3_0_0()
	default:
		return make([]Aggregator, 0)
	}
}
