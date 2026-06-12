// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestHandler_Empty(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	rr := httptest.NewRecorder()
	Handler(store, zerolog.Nop(), false)(rr, httptest.NewRequest(http.MethodGet, "/status/keyverify", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	body, _ := io.ReadAll(rr.Body)
	var got []VaultStatus
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, string(body))
	}
	if len(got) != 0 {
		t.Fatalf("expected empty array, got %d entries", len(got))
	}
	if h := rr.Header().Get("X-Keyverify-Disabled"); h != "" {
		t.Fatalf("X-Keyverify-Disabled header set when enabled: %q", h)
	}
}

// TestHandler_DisabledHeader pins the non-breaking disabled signal: when the
// scheduler is off the body is still a normal (here empty) array, but the
// X-Keyverify-Disabled header lets an operator confirm it's disabled rather
// than merely idle.
func TestHandler_DisabledHeader(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	rr := httptest.NewRecorder()
	Handler(store, zerolog.Nop(), true)(rr, httptest.NewRequest(http.MethodGet, "/status/keyverify", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if h := rr.Header().Get("X-Keyverify-Disabled"); h != "true" {
		t.Fatalf("X-Keyverify-Disabled: got %q, want \"true\"", h)
	}
	var got []VaultStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("body must remain a valid VaultStatus array: %v", err)
	}
}

func TestHandler_MixedSuccessFailurePending(t *testing.T) {
	dir := t.TempDir()
	store := NewMarkerStore(dir)
	pkVerified := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	pkFailed := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	pkPending := "thorpub1addwnpepqf6c8j2qx0vy5fk6lz9q7zaarq6gcjvg85ck6kqz0xv7pmlmskpqv6q9h0g"
	if err := store.WriteSuccess(pkVerified, 100); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFailure(FailureRecord{
		PubKey: pkFailed, Height: 200, AttemptedAt: time.Now().UTC(),
		Kind: "culprit", Error: "bad", Culprits: []string{"peerA"},
	}); err != nil {
		t.Fatal(err)
	}
	store.SetExpected([]string{pkVerified, pkFailed, pkPending})

	rr := httptest.NewRecorder()
	Handler(store, zerolog.Nop(), false)(rr, httptest.NewRequest(http.MethodGet, "/status/keyverify", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	var got []VaultStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}
	byKey := map[string]VaultStatus{}
	for _, st := range got {
		byKey[st.PubKey] = st
	}
	if byKey[pkVerified].Status != "verified" || !byKey[pkVerified].Verified {
		t.Fatalf("pkVerified: want status=verified, verified=true; got %+v", byKey[pkVerified])
	}
	if byKey[pkFailed].Status != "failed" || byKey[pkFailed].Verified ||
		byKey[pkFailed].Failure == nil || byKey[pkFailed].Failure.Kind != "culprit" {
		t.Fatalf("pkFailed: want status=failed, kind=culprit; got %+v", byKey[pkFailed])
	}
	if byKey[pkPending].Status != "pending" || byKey[pkPending].Verified ||
		byKey[pkPending].Failure != nil {
		t.Fatalf("pkPending: want status=pending, verified=false, failure=nil; got %+v", byKey[pkPending])
	}
}
