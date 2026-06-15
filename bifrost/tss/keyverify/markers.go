// Package keyverify contains the temporary scheduler, marker store, and HTTP
// handler that drive periodic tss-lib keyverify re-attestation of ECDSA
// keyshares. The entire package is expected to be removed once the tss-lib v3
// rollout is complete — see REMOVAL.md.
//
// TEMP: keyverify — remove this entire package with the keyverify rollout.
package keyverify

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	markerPrefix = "keyverify-"
	okSuffix     = ".ok"
	failSuffix   = ".fail"
)

// validPubKey rejects pubkey strings that could escape the marker directory or
// otherwise produce surprising filenames. Real pubkeys in this codebase are
// bech32 (thorpub1..., lowercase + digits) and pass trivially.
func validPubKey(pk string) error {
	if pk == "" {
		return errors.New("empty pubKey")
	}
	for _, r := range pk {
		if r == '/' || r == '\\' || r == 0 {
			return fmt.Errorf("invalid pubKey: contains forbidden character %q", r)
		}
	}
	if strings.Contains(pk, "..") {
		return errors.New("invalid pubKey: contains \"..\"")
	}
	return nil
}

// Culprit is the structured per-culprit entry attached to a FailureRecord.
// Mirrors kvecdsa.CulpritDetail; defined here so this package keeps no
// compile-time dependency on the tss packages (the main.go closure converts).
type Culprit struct {
	PubKey string `json:"pubkey"`
	// LastAcceptedRound is the last round whose share from this peer was
	// accepted (passed hash-check / was applied), or "none".
	LastAcceptedRound string `json:"last_accepted_round"`
	// LastWireRound is the last round seen from this peer at the wire level,
	// including received-but-unconfirmed broadcasts, or "none". A peer with
	// LastWireRound ahead of LastAcceptedRound delivered messages whose
	// confirms never assembled — a different failure than total silence.
	LastWireRound  string    `json:"last_wire_round"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	SharesAccepted int       `json:"shares_accepted"`
	SharesExpected int       `json:"shares_expected"`
}

// FailureRecord is the on-disk payload of a keyverify-<pubkey>.fail file.
//
// Schema-evolution rule: fields may only be ADDED, never have their type
// changed — older .fail files (and external consumers of /status/keyverify)
// must keep decoding. Culprits in particular stays []string; the structured
// counterpart is the additive CulpritDetails.
type FailureRecord struct {
	PubKey      string    `json:"pubkey"`
	Height      int64     `json:"height"`
	AttemptedAt time.Time `json:"attempted_at"`
	// Kind classifies the failure: culprit | timeout | other. Older .fail
	// files written before this field existed decode with Kind == "".
	Kind     string   `json:"kind"`
	Error    string   `json:"error"`
	Culprits []string `json:"culprits"`
	// ReporterPubKey identifies the node that emitted this record, enabling
	// cross-node correlation: all members' records of the same round share
	// (PubKey, Height), and ReporterPubKey distinguishes the reporters.
	ReporterPubKey string `json:"reporter_pubkey,omitempty"`
	// LocalRound is the local party's last produced round at failure time
	// ("none" if the local party never started). Distinguishes "we never
	// started" from "peers went silent at round X".
	LocalRound string `json:"local_round,omitempty"`
	// CulpritDetails carries per-culprit round/receipt enrichment, parallel
	// to Culprits. Empty for older records and non-timeout failures.
	CulpritDetails []Culprit `json:"culprit_details,omitempty"`
}

// VaultStatus is the API response shape for one vault.
type VaultStatus struct {
	PubKey string `json:"pubkey"`
	// Status is the high-level summary: verified | failed | pending.
	Status   string         `json:"status"`
	Verified bool           `json:"verified"`
	Failure  *FailureRecord `json:"failure,omitempty"`
}

// MarkerStore manages keyverify-<pubkey>.{ok,fail} files in a single directory.
// Invariant: .ok and .fail are never both present for the same pubkey — a
// successful attempt deletes any pre-existing .fail.
//
// Concurrency contract:
//   - List, HasSuccess, GetExpected, SetExpected are safe for concurrent use.
//   - WriteSuccess and WriteFailure are NOT internally serialized per pubkey;
//     they assume a single writer per pubkey at a time. WriteSuccess in
//     particular is a non-atomic two-step (write .ok, then remove .fail), so
//     concurrent WriteSuccess + WriteFailure for the same pubkey could leave
//     both files on disk, violating the .ok/.fail-mutual-exclusion invariant.
//     The scheduler provides this serialization via perVaultLocks (see
//     scheduler.go runOneAttempt) and a single-goroutine tickOnce loop.
//
// MarkerStore also tracks an in-memory "expected" set of vault pubkeys this
// node is a member of. The scheduler updates the set each tick; the HTTP
// handler reads it to synthesize pending entries for vaults that have not
// yet produced a marker. The set is intentionally process-local — on bifrost
// restart it's empty until the next scheduler tick (≤ one poll interval).
type MarkerStore struct {
	dir      string
	mu       sync.RWMutex
	expected map[string]struct{}
}

func NewMarkerStore(dir string) *MarkerStore {
	return &MarkerStore{
		dir:      dir,
		expected: map[string]struct{}{},
	}
}

func (m *MarkerStore) okPath(pk string) string { return filepath.Join(m.dir, markerPrefix+pk+okSuffix) }

func (m *MarkerStore) failPath(pk string) string {
	return filepath.Join(m.dir, markerPrefix+pk+failSuffix)
}

// HasSuccess reports whether keyverify-<pubkey>.ok exists.
func (m *MarkerStore) HasSuccess(pubKey string) bool {
	if err := validPubKey(pubKey); err != nil {
		return false
	}
	_, err := os.Stat(m.okPath(pubKey))
	return err == nil
}

// SetExpected replaces the in-memory set of expected vault pubkeys
// atomically. The scheduler calls this after each successful listVaults so
// the handler can synthesize {status: "pending"} entries for vaults with no
// .ok or .fail marker yet. Passing nil or empty clears the set.
func (m *MarkerStore) SetExpected(pubKeys []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expected = make(map[string]struct{}, len(pubKeys))
	for _, pk := range pubKeys {
		if err := validPubKey(pk); err != nil {
			log.Printf("keyverify: SetExpected: skip %q: %v", pk, err)
			continue
		}
		m.expected[pk] = struct{}{}
	}
}

// WriteSuccess creates an empty .ok marker and deletes any pre-existing .fail.
// _height is currently unused on disk (mtime is the timestamp signal); it is
// accepted to keep the API symmetric with WriteFailure and to allow future
// JSON-bodied .ok files without an interface change.
func (m *MarkerStore) WriteSuccess(pubKey string, _height int64) error {
	if err := validPubKey(pubKey); err != nil {
		return fmt.Errorf("WriteSuccess: %w", err)
	}
	if err := os.WriteFile(m.okPath(pubKey), nil, 0o600); err != nil {
		return fmt.Errorf("write .ok: %w", err)
	}
	if err := os.Remove(m.failPath(pubKey)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale .fail: %w", err)
	}
	return nil
}

// WriteFailure atomically writes the .fail record (tmp + rename).
func (m *MarkerStore) WriteFailure(rec FailureRecord) error {
	if err := validPubKey(rec.PubKey); err != nil {
		return fmt.Errorf("WriteFailure: %w", err)
	}
	buf, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	final := m.failPath(rec.PubKey)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// List enumerates every pubkey with either a .ok or a .fail marker, sorted by
// pubkey. Pubkeys with neither marker are not included.
func (m *MarkerStore) List() ([]VaultStatus, error) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// dir doesn't exist — still consider expected pending entries.
			entries = nil
		} else {
			return nil, fmt.Errorf("read dir: %w", err)
		}
	}
	statuses := map[string]VaultStatus{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, markerPrefix) {
			continue
		}
		switch {
		case strings.HasSuffix(name, okSuffix):
			pk := strings.TrimSuffix(strings.TrimPrefix(name, markerPrefix), okSuffix)
			if err := validPubKey(pk); err != nil {
				log.Printf("keyverify: list: skip %q (bad pubkey): %v", name, err)
				continue
			}
			st := statuses[pk]
			st.PubKey = pk
			st.Status = "verified"
			st.Verified = true
			st.Failure = nil
			statuses[pk] = st
		case strings.HasSuffix(name, failSuffix):
			pk := strings.TrimSuffix(strings.TrimPrefix(name, markerPrefix), failSuffix)
			if err := validPubKey(pk); err != nil {
				log.Printf("keyverify: list: skip %q (bad pubkey): %v", name, err)
				continue
			}
			st, ok := statuses[pk]
			if ok && st.Verified {
				continue // invariant violation; .ok wins
			}
			buf, rerr := os.ReadFile(filepath.Join(m.dir, name))
			if rerr != nil {
				log.Printf("keyverify: list: skip %q: %v", name, rerr)
				continue
			}
			var rec FailureRecord
			if uerr := json.Unmarshal(buf, &rec); uerr != nil {
				log.Printf("keyverify: list: skip %q: %v", name, uerr)
				continue
			}
			st.PubKey = pk
			st.Status = "failed"
			st.Verified = false
			st.Failure = &rec
			statuses[pk] = st
		}
	}
	// Synthesize pending entries for expected pubkeys not already represented
	// on disk. .ok / .fail always win — the loop above has populated those.
	m.mu.RLock()
	for pk := range m.expected {
		if _, exists := statuses[pk]; exists {
			continue
		}
		statuses[pk] = VaultStatus{PubKey: pk, Status: "pending"}
	}
	m.mu.RUnlock()

	out := make([]VaultStatus, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PubKey < out[j].PubKey })
	return out, nil
}
