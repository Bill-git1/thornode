//go:build !mocknet && !stagenet
// +build !mocknet,!stagenet

package aggregators

func DexAggregators() []Aggregator {
	return DexAggregatorsList()
}
