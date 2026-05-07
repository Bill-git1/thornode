package monero

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
)

const reorgBlockMetaPrefix = "xmr-reorg-block-meta-v1-"

type reorgBlockMeta struct {
	Height               int64    `json:"height"`
	BlockHash            string   `json:"block_hash"`
	PreviousHash         string   `json:"previous_hash"`
	CustomerTransactions []string `json:"customer_transactions,omitempty"`
}

type ReorgStore struct {
	db *leveldb.DB
}

func NewReorgStore(db *leveldb.DB) *ReorgStore {
	return &ReorgStore{db: db}
}

func (s *ReorgStore) Get(height int64) (*reorgBlockMeta, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	value, err := s.db.Get([]byte(reorgBlockMetaKey(height)), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("fail to load xmr reorg block meta at height %d: %w", height, err)
	}

	var meta reorgBlockMeta
	if err = json.Unmarshal(value, &meta); err != nil {
		return nil, fmt.Errorf("fail to decode xmr reorg block meta at height %d: %w", height, err)
	}
	return &meta, nil
}

func (s *ReorgStore) Set(meta *reorgBlockMeta) error {
	if s == nil || s.db == nil {
		return nil
	}
	if meta == nil {
		return fmt.Errorf("xmr reorg block meta is nil")
	}
	if meta.Height < 0 {
		return fmt.Errorf("xmr reorg block meta height must be >= 0")
	}
	if meta.BlockHash == "" {
		return fmt.Errorf("xmr reorg block meta hash is empty at height %d", meta.Height)
	}

	value, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("fail to encode xmr reorg block meta at height %d: %w", meta.Height, err)
	}
	if err = s.db.Put([]byte(reorgBlockMetaKey(meta.Height)), value, nil); err != nil {
		return fmt.Errorf("fail to save xmr reorg block meta at height %d: %w", meta.Height, err)
	}
	return nil
}

func (s *ReorgStore) Delete(height int64) error {
	if s == nil || s.db == nil || height < 0 {
		return nil
	}
	if err := s.db.Delete([]byte(reorgBlockMetaKey(height)), nil); err != nil && !errors.Is(err, leveldb.ErrNotFound) {
		return fmt.Errorf("fail to delete xmr reorg block meta at height %d: %w", height, err)
	}
	return nil
}

func reorgBlockMetaKey(height int64) string {
	return fmt.Sprintf("%s%d", reorgBlockMetaPrefix, height)
}
