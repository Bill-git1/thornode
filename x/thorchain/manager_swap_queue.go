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

func (item swapItem) GetHash() common.TxID {
	return item.msg.Tx.ID
}

func (items swapItems) HasItem(hash common.TxID) bool {
	for _, item := range items {
		if item.msg.Tx.ID.Equals(hash) {
			return true
		}
	}
	return false
}

func (items swapItems) Sort() swapItems {
	// Create independent copies so each sort operates on its own slice.
	byFee := make(swapItems, len(items))
	copy(byFee, items)

	bySlip := make(swapItems, len(items))
	copy(bySlip, items)

	// sort by liquidity fee, descending
	sort.SliceStable(byFee, func(i, j int) bool {
		return byFee[i].fee.GT(byFee[j].fee)
	})

	// sort by slip fee, descending
	sort.SliceStable(bySlip, func(i, j int) bool {
		return bySlip[i].slip.GT(bySlip[j].slip)
	})

	type score struct {
		msg   MsgSwap
		score int
		index int
	}

	// Build a map from (TxID, index) -> scores index for O(1) lookups.
	type itemKey struct {
		txID  common.TxID
		index int
	}
	scoreIdx := make(map[itemKey]int, len(items))

	// add liquidity fee score
	scores := make([]score, len(byFee))
	for i, item := range byFee {
		key := itemKey{txID: item.msg.Tx.ID, index: item.index}
		scores[i] = score{
			msg:   item.msg,
			score: i,
			index: item.index,
		}
		scoreIdx[key] = i
	}

	// add slip score
	for i, item := range bySlip {
		key := itemKey{txID: item.msg.Tx.ID, index: item.index}
		if j, ok := scoreIdx[key]; ok {
			scores[j].score += i
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

	// Build a map from (TxID, index) -> item for O(1) lookups.
	itemMap := make(map[itemKey]swapItem, len(items))
	for _, item := range items {
		key := itemKey{txID: item.msg.Tx.ID, index: item.index}
		itemMap[key] = item
	}

	// sort our items by score
	sorted := make(swapItems, len(items))
	for i, score := range scores {
		key := itemKey{txID: score.msg.Tx.ID, index: score.index}
		sorted[i] = itemMap[key]
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

// Append adds a tradePair to the list if it doesn't already exist
func (p tradePairs) Append(pair tradePair) tradePairs {
	// Check if the pair already exists
	for _, existing := range p {
		if existing.Equals(pair) {
			return p
		}
	}
	// If not found, append it
	return append(p, pair)
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
			p = p.Append(pair)
		}
	}
	return p
}
