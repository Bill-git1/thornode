package thorchain

import (
	"fmt"
	"sort"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

const PreferredAssetSwapMemoPrefix = "THOR-PREFERRED-ASSET"

type swapItem struct {
	index int
	msg   MsgSwap
	fee   cosmos.Uint
	slip  cosmos.Uint
}
type swapItems []swapItem

func (items swapItems) HasItem(hash common.TxID) bool {
	for _, item := range items {
		if item.msg.Tx.ID.Equals(hash) {
			return true
		}
	}
	return false
}

func (items swapItems) Sort() swapItems {
	// sort by liquidity fee , descending
	byFee := items
	sort.SliceStable(byFee, func(i, j int) bool {
		return byFee[i].fee.GT(byFee[j].fee)
	})

	// sort by slip fee , descending
	bySlip := items
	sort.SliceStable(bySlip, func(i, j int) bool {
		return bySlip[i].slip.GT(bySlip[j].slip)
	})

	type score struct {
		msg   MsgSwap
		score int
		index int
	}

	// add liquidity fee score
	scores := make([]score, len(items))
	for i, item := range byFee {
		scores[i] = score{
			msg:   item.msg,
			score: i,
			index: item.index,
		}
	}

	// add slip score
	for i, item := range bySlip {
		for j, score := range scores {
			if score.msg.Tx.ID.Equals(item.msg.Tx.ID) && score.index == item.index {
				scores[j].score += i
				break
			}
		}
	}

	// This sorted appears to sort twice, but actually the first sort informs
	// the second. If we have multiple swaps with the same score, it will use
	// the ID sort to deterministically sort within the same score

	// sort by ID, first
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].msg.Tx.ID.String() < scores[j].msg.Tx.ID.String()
	})

	// sort by score, second
	sort.SliceStable(scores, func(i, j int) bool {
		return scores[i].score < scores[j].score
	})

	// sort our items by score
	sorted := make(swapItems, len(items))
	for i, score := range scores {
		for _, item := range items {
			if item.msg.Tx.ID.Equals(score.msg.Tx.ID) && score.index == item.index {
				sorted[i] = item
				break
			}
		}
	}

	return sorted
}

type tradePair struct {
	source common.Asset
	target common.Asset
}

type tradePairs []tradePair

func genTradePair(s, t common.Asset) tradePair {
	return tradePair{
		source: s,
		target: t,
	}
}

func (pair tradePair) String() string {
	return fmt.Sprintf("%s>%s", pair.source, pair.target)
}

func (pair tradePair) HasRune() bool {
	return pair.source.IsRune() || pair.target.IsRune()
}

func (pair tradePair) Equals(p tradePair) bool {
	return pair.source.Equals(p.source) && pair.target.Equals(p.target)
}

// given a trade pair, find the trading pairs that are the reverse of this
// trade pair. This helps us build a list of trading pairs adv swap queue to check
// for limit swaps later
func (p tradePairs) findMatchingTrades(trade tradePair, pairs tradePairs) tradePairs {
	var comp func(pair tradePair) bool
	switch {
	case trade.source.IsRune():
		comp = func(pair tradePair) bool { return pair.source.Equals(trade.target) }
	case trade.target.IsRune():
		comp = func(pair tradePair) bool { return pair.target.Equals(trade.source) }
	default:
		comp = func(pair tradePair) bool { return pair.source.Equals(trade.target) || pair.target.Equals(trade.source) }
	}
	for _, pair := range pairs {
		if comp(pair) {
			// check for duplicates
			exists := false
			for _, p2 := range p {
				if p2.Equals(pair) {
					exists = true
					break
				}
			}
			if !exists {
				p = append(p, pair)
			}
		}
	}
	return p
}
