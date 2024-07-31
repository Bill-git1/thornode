//go:build !mocknet && !stagenet
// +build !mocknet,!stagenet

package aggregators

import (
	"github.com/blang/semver"
)

func DexAggregators(version semver.Version) []Aggregator {
	switch {
	case version.GTE(semver.MustParse("1.134.0")):
		return DexAggregatorsV134()
	default:
		return make([]Aggregator, 0)
	}
}
