package observer

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/opt"
	"github.com/syndtr/goleveldb/leveldb/util"

	"gitlab.com/thorchain/thornode/v3/bifrost/db"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient/types"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
)

// ObserverStorage save the ondeck tx in item to key value store, in case bifrost restart
type ObserverStorage struct {
	db       *leveldb.DB
	errataMu sync.Mutex
}

const (
	LegacyOnDeckKey           = "ondeck-tx"
	LegacyMigrationHandledKey = "ondeck-migration-handled"
	OnDeckTxKeyPrefix         = "txs:"
	PendingErrataKeyPrefix    = "errata:v1:"
)

type persistedErrata struct {
	Height                    int64          `json:"height"`
	DeliveryID                string         `json:"delivery_id,omitempty"`
	Tx                        types.ErrataTx `json:"tx"`
	DeliverySeen              bool           `json:"delivery_seen,omitempty"`
	CleanupComplete           bool           `json:"cleanup_complete,omitempty"`
	Committed                 bool           `json:"committed,omitempty"`
	CommittedAtUnixNano       int64          `json:"committed_at_unix_nano,omitempty"`
	AttestationAttempts       uint32         `json:"attestation_attempts,omitempty"`
	FirstAttemptAtUnixNano    int64          `json:"first_attempt_at_unix_nano,omitempty"`
	LastAttemptAtUnixNano     int64          `json:"last_attempt_at_unix_nano,omitempty"`
	AttestationRetryExhausted bool           `json:"attestation_retry_exhausted,omitempty"`
	RetryExhaustedAtUnixNano  int64          `json:"retry_exhausted_at_unix_nano,omitempty"`
}

// errataDeckMutation is one exact on-deck storage transition performed in the
// same batch that records local errata cleanup. FinalizeHeight is the original
// txInKey height, not the orphaned block height.
type errataDeckMutation struct {
	TxIn           *types.TxIn
	FinalizeHeight int64
	Delete         bool
}

// NewObserverStorage create a new instance of LevelDBScannerStorage
func NewObserverStorage(path string, opts config.LevelDBOptions) (*ObserverStorage, error) {
	ldb, err := db.NewLevelDB(path, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create observer storage: %w", err)
	}

	return &ObserverStorage{db: ldb}, nil
}

// createTxKey creates a unique key for a TxIn based on chain, finalization height, and future-observation mode.
func (s *ObserverStorage) createTxKey(txIn *types.TxIn, finalizeHeight int64) string {
	if finalizeHeight == 0 && len(txIn.TxArray) > 0 {
		finalizeHeight = txIn.TxArray[0].BlockHeight + txIn.ConfirmationRequired
	}

	key := fmt.Sprintf("%s%s:%d",
		OnDeckTxKeyPrefix,
		txIn.Chain.String(),
		finalizeHeight)
	if txIn.AllowFutureObservation {
		key += ":future"
	}
	return key
}

func (s *ObserverStorage) MigrateLegacy() ([]*types.TxIn, error) {
	_, err := s.db.Get([]byte(LegacyMigrationHandledKey), nil)
	if !errors.Is(err, leveldb.ErrNotFound) {
		return nil, nil
	}
	buf, err := s.db.Get([]byte(LegacyOnDeckKey), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("fail to get ondeck tx from key value store: %w", err)
	}
	var result []*types.TxIn
	if err = json.Unmarshal(buf, &result); err != nil {
		return nil, fmt.Errorf("fail to unmarshal ondeck tx: %w", err)
	}

	// Migrate the ondeck tx to the new format
	for _, txIn := range result {
		key := s.createTxKey(txIn, 0)
		data, err := json.Marshal(txIn)
		if err != nil {
			return nil, fmt.Errorf("fail to marshal ondeck tx to json: %w", err)
		}
		if err := s.db.Put([]byte(key), data, nil); err != nil {
			return nil, fmt.Errorf("fail to put ondeck tx to key value store: %w", err)
		}
	}

	if err := s.db.Delete([]byte(LegacyOnDeckKey), nil); err != nil {
		return nil, fmt.Errorf("fail to delete legacy ondeck tx from key value store: %w", err)
	}

	if err := s.db.Put([]byte(LegacyMigrationHandledKey), []byte("true"), nil); err != nil {
		return nil, fmt.Errorf("fail to put migration handled flag to key value store: %w", err)
	}

	return result, nil
}

// GetOnDeckTxs retrieve the ondeck tx from key value store
func (s *ObserverStorage) GetOnDeckTxs() ([]*types.TxIn, error) {
	// Check if the legacy migration has been handled
	legacyTxs, err := s.MigrateLegacy()
	if err != nil {
		return nil, fmt.Errorf("fail to migrate legacy ondeck tx: %w", err)
	}

	// If legacy txs exist, return them
	if len(legacyTxs) > 0 {
		return legacyTxs, nil
	}

	// Use a DB iterator to get all keys with the prefix
	iter := s.db.NewIterator(util.BytesPrefix([]byte(OnDeckTxKeyPrefix)), nil)
	defer iter.Release()

	var result []*types.TxIn
	for iter.Next() {
		var txIn types.TxIn
		if err := json.Unmarshal(iter.Value(), &txIn); err != nil {
			return nil, fmt.Errorf("fail to unmarshal ondeck tx: %w", err)
		}
		result = append(result, &txIn)
	}

	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("error iterating through ondeck txs: %w", err)
	}

	return result, nil
}

// AddOrUpdateTx adds or updates a single TxIn in storage
func (s *ObserverStorage) AddOrUpdateTx(txIn *types.TxIn) error {
	key := s.createTxKey(txIn, 0)
	data, err := json.Marshal(txIn)
	if err != nil {
		return fmt.Errorf("fail to marshal ondeck tx to json: %w", err)
	}
	return s.db.Put([]byte(key), data, observerWriteOptions(txIn.Chain))
}

// AddErrataBlock persists errata before the scanner's reorg journal advances.
func (s *ObserverStorage) AddErrataBlock(block types.ErrataBlock) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("observer storage is not configured")
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	batch := new(leveldb.Batch)
	for _, tx := range block.Txs {
		key := []byte(pendingErrataKey(tx))
		record, found, err := s.getPersistedErrataLocked(tx)
		if err != nil {
			return err
		}
		if !found {
			record = persistedErrata{Tx: tx}
		}
		record.Height = block.Height
		record.DeliveryID = block.DeliveryID
		record.Tx = tx
		record.DeliverySeen = true
		if errataFinished(record) {
			batch.Delete(key)
			continue
		}
		data, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("fail to marshal pending errata: %w", err)
		}
		batch.Put(key, data)
	}
	if batch.Len() == 0 {
		return nil
	}
	if err := s.db.Write(batch, errataWriteOptions(block.Txs)); err != nil {
		return fmt.Errorf("fail to persist pending errata: %w", err)
	}
	return nil
}

func (s *ObserverStorage) GetPendingErrata() ([]persistedErrata, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	iter := s.db.NewIterator(util.BytesPrefix([]byte(PendingErrataKeyPrefix)), nil)
	defer iter.Release()

	result := make([]persistedErrata, 0)
	for iter.Next() {
		record, err := decodePersistedErrata(iter.Value())
		if err != nil {
			return nil, fmt.Errorf("fail to decode pending errata: %w", err)
		}
		if record.AttestationRetryExhausted {
			continue
		}
		result = append(result, record)
	}
	if err := iter.Error(); err != nil {
		return nil, fmt.Errorf("fail to iterate pending errata: %w", err)
	}
	return result, nil
}

func (s *ObserverStorage) GetPendingErrataTx(tx types.ErrataTx) (persistedErrata, bool, error) {
	if s == nil || s.db == nil {
		return persistedErrata{}, false, nil
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()
	return s.getPersistedErrataLocked(tx)
}

// errataOutboxChain reports whether a chain uses the durable, acknowledged
// errata outbox. Only XMR does: the outbox exists to make XMR reorg replay
// restartable, and only the XMR scanner sets ErrataBlock.Persisted. Other
// chains publish errata without a local durability handshake, so there is no
// local delivery for a THORChain commit to race against.
func errataOutboxChain(chain common.Chain) bool {
	return chain.Equals(common.XMRChain)
}

// MarkErrataCommitted records the THORChain commit independently from local
// on-deck cleanup. A commit that arrives before the local scanner delivery is
// retained as a tombstone so a later AddErrataBlock cannot resurrect work that
// THORChain already accepted.
//
// The commit handler fires for every quorum errata on every chain, including
// ones this node never delivered. Creating a tombstone for those would write a
// LevelDB record per BTC/ETH/DOGE errata on every node, which the retry ticker
// then re-reads until the 30-day prune collects it. The tombstone only defends
// against a commit racing ahead of a local delivery, which is outbox-only, so
// a commit with no local record on a non-outbox chain is dropped.
func (s *ObserverStorage) MarkErrataCommitted(tx types.ErrataTx) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("observer storage is not configured")
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	record, found, err := s.getPersistedErrataLocked(tx)
	if err != nil {
		return err
	}
	if !found {
		if !errataOutboxChain(tx.Chain) {
			return nil
		}
		record = persistedErrata{Tx: tx}
	}
	record.Tx = tx
	record.Committed = true
	if record.CommittedAtUnixNano == 0 {
		record.CommittedAtUnixNano = time.Now().UnixNano()
	}
	return s.writePersistedErrataLocked(record)
}

// RecordErrataAttestationAttempt durably advances the retry schedule before an
// attestation is emitted. It never creates or resurrects a record: a concurrent
// commit notification that removed the outbox item wins.
func (s *ObserverStorage) RecordErrataAttestationAttempt(tx types.ErrataTx, at time.Time) (persistedErrata, bool, error) {
	if s == nil || s.db == nil {
		return persistedErrata{}, false, fmt.Errorf("observer storage is not configured")
	}
	if at.IsZero() {
		return persistedErrata{}, false, fmt.Errorf("errata attestation attempt time is zero")
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	record, found, err := s.getPersistedErrataLocked(tx)
	if err != nil || !found {
		return record, false, err
	}
	if record.Committed || record.AttestationRetryExhausted || !record.CleanupComplete {
		return record, false, nil
	}
	atNano := at.UnixNano()
	if record.FirstAttemptAtUnixNano == 0 {
		record.FirstAttemptAtUnixNano = atNano
	}
	record.LastAttemptAtUnixNano = atNano
	record.AttestationAttempts++
	if err = s.writePersistedErrataLocked(record); err != nil {
		return persistedErrata{}, false, err
	}
	return record, true, nil
}

// MarkErrataAttestationRetryExhausted places an unconfirmed errata record in a
// durable terminal quarantine. A later commit event still completes and
// removes it, but the observer will no longer gossip it indefinitely.
func (s *ObserverStorage) MarkErrataAttestationRetryExhausted(tx types.ErrataTx, at time.Time) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("observer storage is not configured")
	}
	if at.IsZero() {
		return false, fmt.Errorf("errata retry exhaustion time is zero")
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	record, found, err := s.getPersistedErrataLocked(tx)
	if err != nil || !found {
		return false, err
	}
	if record.Committed || record.AttestationRetryExhausted || !record.CleanupComplete {
		return false, nil
	}
	record.AttestationRetryExhausted = true
	record.RetryExhaustedAtUnixNano = at.UnixNano()
	if err = s.writePersistedErrataLocked(record); err != nil {
		return false, err
	}
	return true, nil
}

// PruneCommittedOnlyErrata bounds commit-before-delivery tombstones after a
// conservative safety horizon. Records whose scanner delivery or cleanup has
// begun remain in the durable outbox until normal completion.
func (s *ObserverStorage) PruneCommittedOnlyErrata(cutoff time.Time) (int, error) {
	if s == nil || s.db == nil || cutoff.IsZero() {
		return 0, nil
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	iter := s.db.NewIterator(util.BytesPrefix([]byte(PendingErrataKeyPrefix)), nil)
	defer iter.Release()
	batch := new(leveldb.Batch)
	pruned := 0
	syncWrite := false
	cutoffNano := cutoff.UnixNano()
	for iter.Next() {
		record, err := decodePersistedErrata(iter.Value())
		if err != nil {
			return 0, fmt.Errorf("fail to decode pending errata while pruning: %w", err)
		}
		if !record.Committed || record.DeliverySeen || record.CleanupComplete ||
			record.CommittedAtUnixNano == 0 || record.CommittedAtUnixNano > cutoffNano {
			continue
		}
		batch.Delete(append([]byte(nil), iter.Key()...))
		pruned++
		syncWrite = syncWrite || record.Tx.Chain.Equals(common.XMRChain)
	}
	if err := iter.Error(); err != nil {
		return 0, fmt.Errorf("fail to iterate pending errata while pruning: %w", err)
	}
	if pruned == 0 {
		return 0, nil
	}
	if err := s.db.Write(batch, syncWriteOptions(syncWrite)); err != nil {
		return 0, fmt.Errorf("fail to prune committed-only errata tombstones: %w", err)
	}
	return pruned, nil
}

// CompleteErrataCleanup atomically persists every exact on-deck mutation and
// the cleanup-complete state for the corresponding errata records. In-memory
// state is changed only after this batch succeeds.
func (s *ObserverStorage) CompleteErrataCleanup(txs []types.ErrataTx, mutations []errataDeckMutation) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("observer storage is not configured")
	}
	s.errataMu.Lock()
	defer s.errataMu.Unlock()

	batch := new(leveldb.Batch)
	for _, mutation := range mutations {
		if mutation.TxIn == nil {
			return fmt.Errorf("errata cleanup contains nil on-deck mutation")
		}
		key := []byte(s.createTxKey(mutation.TxIn, mutation.FinalizeHeight))
		if mutation.Delete {
			batch.Delete(key)
			continue
		}
		data, err := json.Marshal(mutation.TxIn)
		if err != nil {
			return fmt.Errorf("fail to marshal errata-cleaned on-deck tx: %w", err)
		}
		batch.Put(key, data)
	}

	seen := make(map[string]struct{}, len(txs))
	for _, tx := range txs {
		keyString := pendingErrataKey(tx)
		if _, ok := seen[keyString]; ok {
			continue
		}
		seen[keyString] = struct{}{}
		record, found, err := s.getPersistedErrataLocked(tx)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		record.CleanupComplete = true
		key := []byte(keyString)
		if errataFinished(record) {
			batch.Delete(key)
			continue
		}
		data, err := json.Marshal(record)
		if err != nil {
			return fmt.Errorf("fail to marshal cleaned errata: %w", err)
		}
		batch.Put(key, data)
	}

	if batch.Len() == 0 {
		return nil
	}
	if err := s.db.Write(batch, errataCleanupWriteOptions(txs, mutations)); err != nil {
		return fmt.Errorf("fail to durably apply errata cleanup: %w", err)
	}
	return nil
}

func (s *ObserverStorage) getPersistedErrataLocked(tx types.ErrataTx) (persistedErrata, bool, error) {
	value, err := s.db.Get([]byte(pendingErrataKey(tx)), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return persistedErrata{}, false, nil
		}
		return persistedErrata{}, false, fmt.Errorf("fail to load pending errata: %w", err)
	}
	record, err := decodePersistedErrata(value)
	if err != nil {
		return persistedErrata{}, false, fmt.Errorf("fail to decode pending errata: %w", err)
	}
	return record, true, nil
}

func (s *ObserverStorage) writePersistedErrataLocked(record persistedErrata) error {
	key := []byte(pendingErrataKey(record.Tx))
	if errataFinished(record) {
		if err := s.db.Delete(key, observerWriteOptions(record.Tx.Chain)); err != nil && !errors.Is(err, leveldb.ErrNotFound) {
			return fmt.Errorf("fail to remove completed errata: %w", err)
		}
		return nil
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("fail to marshal pending errata: %w", err)
	}
	if err = s.db.Put(key, data, observerWriteOptions(record.Tx.Chain)); err != nil {
		return fmt.Errorf("fail to persist pending errata state: %w", err)
	}
	return nil
}

func decodePersistedErrata(data []byte) (persistedErrata, error) {
	var record persistedErrata
	if err := json.Unmarshal(data, &record); err != nil {
		return persistedErrata{}, err
	}
	if !record.DeliverySeen && (record.Height != 0 || record.DeliveryID != "") {
		record.DeliverySeen = true
	}
	return record, nil
}

func errataFinished(record persistedErrata) bool {
	return record.DeliverySeen && record.CleanupComplete && record.Committed
}

func pendingErrataKey(tx types.ErrataTx) string {
	return fmt.Sprintf("%s%s:%s", PendingErrataKeyPrefix, tx.Chain, tx.TxID)
}

func observerWriteOptions(chain common.Chain) *opt.WriteOptions {
	return syncWriteOptions(chain.Equals(common.XMRChain))
}

func syncWriteOptions(sync bool) *opt.WriteOptions {
	if sync {
		return &opt.WriteOptions{Sync: true}
	}
	return nil
}

func errataWriteOptions(txs []types.ErrataTx) *opt.WriteOptions {
	for _, tx := range txs {
		if tx.Chain.Equals(common.XMRChain) {
			return syncWriteOptions(true)
		}
	}
	return nil
}

func errataCleanupWriteOptions(txs []types.ErrataTx, mutations []errataDeckMutation) *opt.WriteOptions {
	if options := errataWriteOptions(txs); options != nil {
		return options
	}
	for _, mutation := range mutations {
		if mutation.TxIn != nil && mutation.TxIn.Chain.Equals(common.XMRChain) {
			return syncWriteOptions(true)
		}
	}
	return nil
}

// RemoveTx removes a single TxIn from storage
func (s *ObserverStorage) RemoveTx(txIn *types.TxIn, finalizeHeight int64) error {
	key := s.createTxKey(txIn, finalizeHeight)
	return s.db.Delete([]byte(key), observerWriteOptions(txIn.Chain))
}

// RemoveAllTxs removes all TxIn from storage
func (s *ObserverStorage) RemoveAllTxs() error {
	iter := s.db.NewIterator(util.BytesPrefix([]byte(OnDeckTxKeyPrefix)), nil)
	defer iter.Release()

	for iter.Next() {
		if err := s.db.Delete(iter.Key(), nil); err != nil {
			return fmt.Errorf("fail to delete ondeck tx from key value store: %w", err)
		}
	}

	if err := iter.Error(); err != nil {
		return fmt.Errorf("error iterating through ondeck txs: %w", err)
	}

	return nil
}

func (s *ObserverStorage) Close() error {
	return s.db.Close()
}
