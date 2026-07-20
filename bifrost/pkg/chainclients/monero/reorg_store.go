package monero

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"

	stypes "gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
)

const (
	reorgBlockMetaPrefix = "xmr-reorg-block-meta-v1-"
	reorgJournalKey      = "xmr-reorg-journal-v2"
)

const (
	reorgPhaseDiscovered = "discovered"
	reorgPhaseReplayed   = "replayed"
	reorgPhaseDelivered  = "delivered"
)

type reorgBlockMeta struct {
	Height               int64    `json:"height"`
	BlockHash            string   `json:"block_hash"`
	PreviousHash         string   `json:"previous_hash"`
	CustomerTransactions []string `json:"customer_transactions,omitempty"`
}

type reorgErrataRecord struct {
	Height int64    `json:"height"`
	TxIDs  []string `json:"tx_ids"`
}

// reorgJournal keeps reorg replay restart-safe. Metadata is not replaced until
// every canonical height has replayed successfully and its observations have
// been persisted by Observer. BlockScanner finalizes this journal before it
// syncs the new scan position; until then, canonical metadata and replayed
// observations both remain recoverable here.
type reorgJournal struct {
	Version       int                 `json:"version"`
	Phase         string              `json:"phase"`
	AnchorHeight  int64               `json:"anchor_height"`
	AnchorHash    string              `json:"anchor_hash"`
	Heights       []int64             `json:"heights"`
	Errata        []reorgErrataRecord `json:"errata,omitempty"`
	ReplayedMetas []*reorgBlockMeta   `json:"replayed_metas,omitempty"`
	ReplayedItems []*stypes.TxInItem  `json:"replayed_items,omitempty"`
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
	if err = s.db.Put([]byte(reorgBlockMetaKey(meta.Height)), value, &opt.WriteOptions{Sync: true}); err != nil {
		return fmt.Errorf("fail to save xmr reorg block meta at height %d: %w", meta.Height, err)
	}
	return nil
}

func (s *ReorgStore) GetJournal() (*reorgJournal, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	value, err := s.db.Get([]byte(reorgJournalKey), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("fail to load xmr reorg journal: %w", err)
	}
	var journal reorgJournal
	if err = json.Unmarshal(value, &journal); err != nil {
		return nil, fmt.Errorf("fail to decode xmr reorg journal: %w", err)
	}
	if journal.Version != 2 {
		return nil, fmt.Errorf("unsupported xmr reorg journal version %d", journal.Version)
	}
	return &journal, nil
}

func (s *ReorgStore) SetJournal(journal *reorgJournal) error {
	if s == nil || s.db == nil {
		return nil
	}
	if journal == nil || journal.Version != 2 || len(journal.Heights) == 0 {
		return fmt.Errorf("invalid xmr reorg journal")
	}
	value, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("fail to encode xmr reorg journal: %w", err)
	}
	if err = s.db.Put([]byte(reorgJournalKey), value, &opt.WriteOptions{Sync: true}); err != nil {
		return fmt.Errorf("fail to save xmr reorg journal: %w", err)
	}
	return nil
}

func (s *ReorgStore) CommitReplay(journal *reorgJournal, metas []*reorgBlockMeta, items []*stypes.TxInItem) error {
	if s == nil || s.db == nil {
		return nil
	}
	if journal == nil {
		return fmt.Errorf("xmr reorg journal is nil")
	}
	journal.Phase = reorgPhaseReplayed
	journal.ReplayedMetas = metas
	journal.ReplayedItems = items
	journalValue, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("fail to encode replayed xmr reorg journal: %w", err)
	}

	for _, meta := range metas {
		if meta == nil || meta.Height < 0 || meta.BlockHash == "" {
			return fmt.Errorf("invalid xmr canonical metadata in replay batch")
		}
	}
	if err = s.db.Put([]byte(reorgJournalKey), journalValue, &opt.WriteOptions{Sync: true}); err != nil {
		return fmt.Errorf("fail to save xmr reorg replay journal: %w", err)
	}
	return nil
}

// CommitDelivery publishes canonical metadata and removes the replay journal
// in one synced batch, but only after Observer has durably accepted the replay.
// BlockScanner invokes this before advancing its synced scan position, so a
// crash between the two operations causes only a safe duplicate rescan.
func (s *ReorgStore) CommitDelivery(journal *reorgJournal) error {
	if s == nil || s.db == nil {
		return nil
	}
	if journal == nil || journal.Phase != reorgPhaseDelivered || len(journal.ReplayedMetas) == 0 {
		return fmt.Errorf("invalid delivered xmr reorg journal")
	}
	batch := new(leveldb.Batch)
	for _, meta := range journal.ReplayedMetas {
		if meta == nil || meta.Height < 0 || meta.BlockHash == "" {
			return fmt.Errorf("invalid xmr canonical metadata in delivery batch")
		}
		value, err := json.Marshal(meta)
		if err != nil {
			return fmt.Errorf("fail to encode xmr canonical metadata at height %d: %w", meta.Height, err)
		}
		batch.Put([]byte(reorgBlockMetaKey(meta.Height)), value)
	}
	batch.Delete([]byte(reorgJournalKey))
	if err := s.db.Write(batch, &opt.WriteOptions{Sync: true}); err != nil {
		return fmt.Errorf("fail to commit delivered xmr reorg metadata: %w", err)
	}
	return nil
}

func (s *ReorgStore) DeleteJournal() error {
	if s == nil || s.db == nil {
		return nil
	}
	if err := s.db.Delete([]byte(reorgJournalKey), &opt.WriteOptions{Sync: true}); err != nil && !errors.Is(err, leveldb.ErrNotFound) {
		return fmt.Errorf("fail to delete xmr reorg journal: %w", err)
	}
	return nil
}

func (s *ReorgStore) Delete(height int64) error {
	if s == nil || s.db == nil || height < 0 {
		return nil
	}
	if err := s.db.Delete([]byte(reorgBlockMetaKey(height)), &opt.WriteOptions{Sync: true}); err != nil && !errors.Is(err, leveldb.ErrNotFound) {
		return fmt.Errorf("fail to delete xmr reorg block meta at height %d: %w", height, err)
	}
	return nil
}

func reorgBlockMetaKey(height int64) string {
	return fmt.Sprintf("%s%d", reorgBlockMetaPrefix, height)
}
