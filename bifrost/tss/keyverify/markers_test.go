// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testPubKey = "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"

func TestMarkerStore_WriteSuccess_CreatesOK(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)

	if err := s.WriteSuccess(testPubKey, 12345600); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	okPath := filepath.Join(dir, "keyverify-"+testPubKey+".ok")
	if _, err := os.Stat(okPath); err != nil {
		t.Fatalf(".ok not created: %v", err)
	}
	if !s.HasSuccess(testPubKey) {
		t.Fatal("HasSuccess returned false after WriteSuccess")
	}
}

func TestMarkerStore_WriteSuccess_RemovesFail(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)

	if err := s.WriteFailure(FailureRecord{
		PubKey: testPubKey, Height: 100, AttemptedAt: time.Now().UTC(),
		Error: "bad", Culprits: []string{"peerA"},
	}); err != nil {
		t.Fatalf("WriteFailure: %v", err)
	}
	failPath := filepath.Join(dir, "keyverify-"+testPubKey+".fail")
	if _, err := os.Stat(failPath); err != nil {
		t.Fatalf(".fail not created: %v", err)
	}

	if err := s.WriteSuccess(testPubKey, 200); err != nil {
		t.Fatalf("WriteSuccess: %v", err)
	}
	if _, err := os.Stat(failPath); !os.IsNotExist(err) {
		t.Fatalf(".fail still present after WriteSuccess (err=%v)", err)
	}
}

func TestMarkerStore_WriteFailure_PersistsRecord(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	rec := FailureRecord{
		PubKey: testPubKey, Height: 999,
		AttemptedAt: time.Date(2026, 5, 26, 18, 0, 0, 0, time.UTC),
		Error:       "round 2 facproof verification failed",
		Culprits:    []string{"peerA", "peerB"},
	}
	if err := s.WriteFailure(rec); err != nil {
		t.Fatalf("WriteFailure: %v", err)
	}
	buf, err := os.ReadFile(filepath.Join(dir, "keyverify-"+testPubKey+".fail"))
	if err != nil {
		t.Fatalf("read .fail: %v", err)
	}
	var got FailureRecord
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.PubKey != rec.PubKey || got.Height != rec.Height || got.Error != rec.Error ||
		len(got.Culprits) != 2 || got.Culprits[0] != "peerA" || got.Culprits[1] != "peerB" {
		t.Fatalf("record round-trip mismatch: %+v", got)
	}
}

func TestMarkerStore_List_EmptyDir(t *testing.T) {
	s := NewMarkerStore(t.TempDir())
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(got))
	}
}

func TestMarkerStore_List_SuccessAndFailure(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	if err := s.WriteSuccess(testPubKey, 1); err != nil {
		t.Fatal(err)
	}
	other := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	if err := s.WriteFailure(FailureRecord{
		PubKey: other, Height: 2, AttemptedAt: time.Now().UTC(),
		Error: "x", Culprits: []string{"y"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	byKey := map[string]VaultStatus{got[0].PubKey: got[0], got[1].PubKey: got[1]}
	if !byKey[testPubKey].Verified || byKey[testPubKey].Failure != nil {
		t.Fatalf("expected verified=true,failure=nil for %s, got %+v", testPubKey, byKey[testPubKey])
	}
	if byKey[other].Verified || byKey[other].Failure == nil || byKey[other].Failure.Error != "x" {
		t.Fatalf("expected verified=false with failure record for %s, got %+v", other, byKey[other])
	}
}

func TestMarkerStore_RejectsBadPubKey(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)

	bad := []string{
		"",
		"../escape",
		"a/b",
		"a\\b",
		"foo..bar",
		"with\x00null",
	}
	for _, pk := range bad {
		if err := s.WriteSuccess(pk, 1); err == nil {
			t.Errorf("WriteSuccess(%q) accepted bad pubKey", pk)
		}
		if err := s.WriteFailure(FailureRecord{PubKey: pk, Height: 1, AttemptedAt: time.Now(), Error: "x"}); err == nil {
			t.Errorf("WriteFailure(%q) accepted bad pubKey", pk)
		}
		if s.HasSuccess(pk) {
			t.Errorf("HasSuccess(%q) returned true for bad pubKey", pk)
		}
	}

	// Confirm directory has not been polluted by any escape attempt.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("unexpected files in marker dir: %v", names)
	}
}

func TestMarkerStore_List_SkipsCorruptFailFile(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)

	good := testPubKey
	if err := s.WriteSuccess(good, 1); err != nil {
		t.Fatal(err)
	}
	// Plant a corrupt .fail file for a different pubkey.
	other := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	corruptPath := filepath.Join(dir, "keyverify-"+other+".fail")
	if err := os.WriteFile(corruptPath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List should not error on per-file corruption, got: %v", err)
	}
	if len(got) != 1 || got[0].PubKey != good || !got[0].Verified {
		t.Fatalf("expected good vault still surfaced, got %+v", got)
	}
}

func TestFailureRecord_KindRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	rec := FailureRecord{
		PubKey: testPubKey, Height: 100,
		AttemptedAt: time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
		Kind:        "timeout",
		Error:       "tss timeout",
		Culprits:    []string{"peerOffline"},
	}
	if err := s.WriteFailure(rec); err != nil {
		t.Fatalf("WriteFailure: %v", err)
	}
	buf, err := os.ReadFile(filepath.Join(dir, "keyverify-"+testPubKey+".fail"))
	if err != nil {
		t.Fatalf("read .fail: %v", err)
	}
	var got FailureRecord
	if err := json.Unmarshal(buf, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Kind != "timeout" {
		t.Fatalf("Kind round-trip: want %q, got %q", "timeout", got.Kind)
	}
}

func TestVaultStatus_StatusFieldPopulated(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	pk1 := testPubKey
	pk2 := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	if err := s.WriteSuccess(pk1, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFailure(FailureRecord{
		PubKey: pk2, Height: 200, AttemptedAt: time.Now().UTC(),
		Kind: "culprit", Error: "x", Culprits: []string{"y"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]VaultStatus{}
	for _, st := range got {
		byKey[st.PubKey] = st
	}
	if byKey[pk1].Status != "verified" {
		t.Fatalf("pk1 Status: want %q, got %q", "verified", byKey[pk1].Status)
	}
	if byKey[pk2].Status != "failed" {
		t.Fatalf("pk2 Status: want %q, got %q", "failed", byKey[pk2].Status)
	}
}

func TestMarkerStore_SetExpected_SynthesizesPending(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	pkVerified := testPubKey
	pkFailed := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	pkPending := "thorpub1addwnpepqf6c8j2qx0vy5fk6lz9q7zaarq6gcjvg85ck6kqz0xv7pmlmskpqv6q9h0g"
	if err := s.WriteSuccess(pkVerified, 100); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFailure(FailureRecord{
		PubKey: pkFailed, Height: 200, AttemptedAt: time.Now().UTC(),
		Kind: "culprit", Error: "x", Culprits: []string{"y"},
	}); err != nil {
		t.Fatal(err)
	}

	s.SetExpected([]string{pkVerified, pkFailed, pkPending})

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]VaultStatus{}
	for _, st := range got {
		byKey[st.PubKey] = st
	}
	if byKey[pkVerified].Status != "verified" {
		t.Fatalf("pkVerified Status: want verified, got %q", byKey[pkVerified].Status)
	}
	if byKey[pkFailed].Status != "failed" {
		t.Fatalf("pkFailed Status: want failed, got %q", byKey[pkFailed].Status)
	}
	pendingEntry, ok := byKey[pkPending]
	if !ok {
		t.Fatalf("pkPending missing from List() — want a pending entry, got %d entries", len(got))
	}
	if pendingEntry.Status != "pending" {
		t.Fatalf("pkPending Status: want pending, got %q", pendingEntry.Status)
	}
	if pendingEntry.Verified {
		t.Fatalf("pkPending Verified: want false, got true")
	}
	if pendingEntry.Failure != nil {
		t.Fatalf("pkPending Failure: want nil, got %+v", pendingEntry.Failure)
	}
}

func TestMarkerStore_SetExpected_DiskMarkersShadowPending(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	if err := s.WriteSuccess(testPubKey, 100); err != nil {
		t.Fatal(err)
	}
	// SetExpected lists pubkey that already has .ok — pending must NOT appear
	// because the .ok takes precedence.
	s.SetExpected([]string{testPubKey})
	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}
	if got[0].Status != "verified" {
		t.Fatalf("Status: want verified, got %q", got[0].Status)
	}
}

func TestMarkerStore_SetExpected_EmptyClearsPending(t *testing.T) {
	dir := t.TempDir()
	s := NewMarkerStore(dir)
	s.SetExpected([]string{testPubKey})
	got, _ := s.List()
	if len(got) != 1 {
		t.Fatalf("after SetExpected: want 1 entry, got %d", len(got))
	}
	s.SetExpected(nil)
	got, _ = s.List()
	if len(got) != 0 {
		t.Fatalf("after SetExpected(nil): want 0 entries, got %d", len(got))
	}
}
