package ebifrost

import (
	"time"

	"cosmossdk.io/log"
	sdk "github.com/cosmos/cosmos-sdk/types"
	common "gitlab.com/thorchain/thornode/v3/common"
)

type TimestampedItem[T any] struct {
	Item      T
	Timestamp time.Time
}

type InjectCache[T any] struct {
	items []TimestampedItem[T]
	mu    *PriorityRWLock

	// recentBlockItems is a map of block height to items that were included in that block.
	// This is used to keep track of recently processed items so we don't reprocess them.
	recentBlockItems map[int64][]T
}

// NewInjectCache creates a new inject cache for the given type
func NewInjectCache[T any]() *InjectCache[T] {
	return &InjectCache[T]{
		items:            make([]TimestampedItem[T], 0),
		recentBlockItems: make(map[int64][]T),
		mu:               NewPriorityRWLock(),
	}
}

// Add adds an item to the cache
func (c *InjectCache[T]) Add(item T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.items = append(c.items, TimestampedItem[T]{
		Item:      item,
		Timestamp: time.Now(),
	})
}

// Get returns all items in the cache (thread-safe)
func (c *InjectCache[T]) Get() []T {
	c.mu.RLockPriority()
	defer c.mu.RUnlock()

	result := make([]T, len(c.items))
	for i, item := range c.items {
		result[i] = item.Item
	}
	return result
}

// Lock locks the mutex
func (c *InjectCache[T]) Lock() {
	c.mu.Lock()
}

// Unlock unlocks the mutex
func (c *InjectCache[T]) Unlock() {
	c.mu.Unlock()
}

// RemoveAt removes the item at the given index
func (c *InjectCache[T]) RemoveAt(index int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if index < 0 || index >= len(c.items) {
		return
	}

	c.items = append(c.items[:index], c.items[index+1:]...)
}

// AddToBlock adds items to the specified block height
func (c *InjectCache[T]) AddToBlock(height int64, item T) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.recentBlockItems[height] = append(c.recentBlockItems[height], item)
}

// CleanOldBlocks removes blocks before the specified height
func (c *InjectCache[T]) CleanOldBlocks(currentHeight int64, keepBlocks int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// analyze-ignore(map-iteration)
	for h := range c.recentBlockItems {
		if h < currentHeight-keepBlocks {
			delete(c.recentBlockItems, h)
		}
	}
}

// CheckRecentBlocks checks if any item in the recent blocks matches the provided predicate
func (c *InjectCache[T]) CheckRecentBlocks(matches func(T) bool) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// analyze-ignore(map-iteration)
	for _, items := range c.recentBlockItems {
		for _, item := range items {
			if matches(item) {
				return true
			}
		}
	}

	return false
}

// MergeWithExisting tries to merge an item with an existing one or adds it as new
func (c *InjectCache[T]) MergeWithExisting(item T, equals func(T, T) bool, merge func(existing T, new T)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, existing := range c.items {
		if equals(existing.Item, item) {
			merge(c.items[i].Item, item)
			// Update the timestamp since we modified the item
			c.items[i].Timestamp = time.Now()
			return true
		}
	}

	c.items = append(c.items, TimestampedItem[T]{
		Item:      item,
		Timestamp: time.Now(),
	})
	return false
}

// MarkAttestationsConfirmed is a generic method to mark attestations confirmed and remove items if empty
// This is a template method that needs specific implementations to use
func (c *InjectCache[T]) MarkAttestationsConfirmed(
	item T,
	logger log.Logger,
	equals func(T, T) bool,
	getAttestations func(T) []*common.Attestation,
	removeAttestations func(T, []*common.Attestation) bool,
	logInfo func(T, log.Logger),
) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	found := false
	for i := 0; i < len(c.items); i++ {
		cacheItem := c.items[i].Item
		if equals(cacheItem, item) {
			found = true
			logInfo(cacheItem, logger)
			if empty := removeAttestations(cacheItem, getAttestations(item)); empty {
				// Remove the element at index i
				c.items = append(c.items[:i], c.items[i+1:]...)
			} else {
				// Update the timestamp since we modified the item
				c.items[i].Timestamp = time.Now()
			}
			break
		}
	}

	return found
}

// AddItem is a generic method that handles the common pattern for sending items to the cache
// It filters out attestations that already exist in recent blocks and merges with existing items
func (c *InjectCache[T]) AddItem(
	newItem T,
	getAttestations func(T) []*common.Attestation,
	setAttestations func(T, []*common.Attestation) T,
	itemsEqual func(T, T) bool,
) error {
	// Filter out attestations that are already in recent blocks
	newAttestations := make([]*common.Attestation, 0)
	for _, a := range getAttestations(newItem) {
		found := c.CheckRecentBlocks(func(blockItem T) bool {
			if !itemsEqual(blockItem, newItem) {
				return false
			}

			existingAttestation := getAttestations(blockItem)
			for _, att := range existingAttestation {
				if a.Equals(att) {
					return true
				}
			}

			return false
		})

		if !found {
			newAttestations = append(newAttestations, a)
		}
	}

	if len(newAttestations) == 0 {
		// No new attestations to add
		return nil
	}

	// Create a new item with only the new attestations
	itemToAdd := setAttestations(newItem, newAttestations)

	// Try to merge with an existing item or add as new
	c.MergeWithExisting(
		itemToAdd,
		itemsEqual,
		func(existing T, new T) {
			// Merge attestations that don't already exist
			existingAtts := getAttestations(existing)
			for _, newAtt := range getAttestations(new) {
				attExists := false
				for _, existingAtt := range existingAtts {
					if newAtt.Equals(existingAtt) {
						attExists = true
						break
					}
				}
				if !attExists {
					existingAtts = append(existingAtts, newAtt)
				}
			}
			// Update the attestations using setAttestations
			_ = setAttestations(existing, existingAtts)
		},
	)

	return nil
}

// BroadcastEvent handles the common pattern of broadcasting events
func (c *InjectCache[T]) BroadcastEvent(
	item T,
	marshal func(T) ([]byte, error),
	broadcast func(string, []byte),
	eventType string,
	logger log.Logger,
) {
	itemBz, err := marshal(item)
	if err != nil {
		logger.Error("Failed to marshal item", "error", err)
		return
	}

	broadcast(eventType, itemBz)
}

// ProcessForProposal processes items for the proposal
func (c *InjectCache[T]) ProcessForProposal(
	createMsg func(T) (sdk.Msg, error),
	createTx func(sdk.Msg) ([]byte, error),
	logItem func(T, log.Logger),
	logger log.Logger,
) [][]byte {
	var injectTxs [][]byte

	items := c.Get()
	for _, item := range items {
		msg, err := createMsg(item)
		if err != nil {
			logger.Error("Failed to create message", "error", err)
			continue
		}

		txBz, err := createTx(msg)
		if err != nil {
			logger.Error("Failed to marshal tx", "error", err)
			continue
		}

		injectTxs = append(injectTxs, txBz)
		logItem(item, logger)
	}

	return injectTxs
}

// PruneExpiredItems removes items that have been in the cache longer than the TTL
func (c *InjectCache[T]) PruneExpiredItems(ttl time.Duration) []T {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	var newItems []TimestampedItem[T]

	var prunedItems []T

	for _, item := range c.items {
		if now.Sub(item.Timestamp) < ttl {
			newItems = append(newItems, item)
		} else {
			prunedItems = append(prunedItems, item.Item)
		}
	}

	c.items = newItems

	return prunedItems
}
