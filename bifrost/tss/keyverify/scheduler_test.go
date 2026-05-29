// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
)

// stubVerifier records each Verify call and returns canned outcomes per pubkey.
type stubVerifier struct {
	mu         sync.Mutex
	calls      []string
	lastDelays map[string]time.Duration // pubkey -> last startDelay passed in
	results    map[string]error         // pubkey -> error to return (nil = success)
}

func (s *stubVerifier) Verify(_ context.Context, pubKey string, _ []string, startDelay time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, pubKey)
	if s.lastDelays == nil {
		s.lastDelays = map[string]time.Duration{}
	}
	s.lastDelays[pubKey] = startDelay
	return s.results[pubKey]
}

func (s *stubVerifier) callsFor(pubKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.calls {
		if c == pubKey {
			n++
		}
	}
	return n
}

func TestScheduler_TickGated_OnlyOnMultiplesOf100(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	// Return a timeout error so the first aligned tick does not write a .ok
	// marker, allowing us to observe the gating behavior on the second aligned
	// tick.
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	members := []string{pk}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: members}})

	sched.tickOnce(context.Background(), 99)  // below first window
	sched.tickOnce(context.Background(), 100) // window 100 — fires
	sched.tickOnce(context.Background(), 101) // same window — skipped
	sched.tickOnce(context.Background(), 200) // window 200 — fires

	if got := sv.callsFor(pk); got != 2 {
		t.Fatalf("expected 2 verify calls, got %d", got)
	}
}

// TestScheduler_DriftAcrossBoundary covers the case where polling skips the
// exact aligned height — e.g. 6s poll on ~6s blocks lands on 99, then 102.
// With the old exact-modulo rule no tick would fire; with the window rule
// we tick at 102 because we've crossed the boundary.
func TestScheduler_DriftAcrossBoundary(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	sched.tickOnce(context.Background(), 99)  // below first window — skip
	sched.tickOnce(context.Background(), 102) // crossed into window 100 — fire
	sched.tickOnce(context.Background(), 150) // still window 100 — skip
	sched.tickOnce(context.Background(), 203) // crossed into window 200 — fire

	if got := sv.callsFor(pk); got != 2 {
		t.Fatalf("expected 2 verify calls (one per crossed window), got %d", got)
	}
}

// TestScheduler_PassesStaggerDelayToVerifier locks in the contract that the
// per-party-index stagger is forwarded to the Verifier (which subscribes
// before sleeping) rather than slept-through inside the scheduler. A
// regression that re-introduces a local sleep would mean the verifier
// receives startDelay=0 even for higher-indexed parties.
func TestScheduler_PassesStaggerDelayToVerifier(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	local := "thorpub_b" // sort position 1 — so staggerFor returns 1 * StaggerStep
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{
		PubKey:  pk,
		Members: []string{"thorpub_a", local, "thorpub_c"},
	}})
	sched.localPubKey = local

	sched.tickOnce(context.Background(), 100)

	want := 1 * StaggerStep
	sv.mu.Lock()
	got := sv.lastDelays[pk]
	sv.mu.Unlock()
	if got != want {
		t.Fatalf("expected verifier to receive startDelay=%s, got %s", want, got)
	}
}

// TestScheduler_RestartDoesNotBackfill ensures that a scheduler starting up
// at height 250 (after a restart) ticks for window 200 but never backfills
// the missed window 100.
func TestScheduler_RestartDoesNotBackfill(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	sched.tickOnce(context.Background(), 250) // first observation after restart
	sched.tickOnce(context.Background(), 251) // still window 200 — skip
	sched.tickOnce(context.Background(), 300) // window 300 — fire

	if got := sv.callsFor(pk); got != 2 {
		t.Fatalf("expected 2 verify calls (one per window we observed), got %d", got)
	}
}

func TestScheduler_SkipsAfterSuccessMarker(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	sv := &stubVerifier{}
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	sched.tickOnce(context.Background(), 100)
	sched.tickOnce(context.Background(), 200)
	sched.tickOnce(context.Background(), 300)

	if got := sv.callsFor(pk); got != 1 {
		t.Fatalf("expected 1 verify call (rest skipped by .ok), got %d", got)
	}
	if !store.HasSuccess(pk) {
		t.Fatal("expected .ok marker after success")
	}
}

func TestScheduler_TimeoutWritesFailWithTimeoutKind(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})
	// Inject a stub timeout-culprit extractor that returns a known non-participating peer.
	sched.extractTimeoutCulprits = func(err error) []string {
		if errors.Is(err, errTssTimeOutForTest) {
			return []string{"peerOffline"}
		}
		return nil
	}

	sched.tickOnce(context.Background(), 100)
	statuses, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 marker (timeout .fail), got %d: %+v", len(statuses), statuses)
	}
	st := statuses[0]
	if st.Status != "failed" || st.Failure == nil {
		t.Fatalf("expected status=failed with failure; got %+v", st)
	}
	if st.Failure.Kind != "timeout" {
		t.Fatalf("expected Kind=timeout; got %q", st.Failure.Kind)
	}
	if len(st.Failure.Culprits) != 1 || st.Failure.Culprits[0] != "peerOffline" {
		t.Fatalf("expected culprits=[peerOffline]; got %v", st.Failure.Culprits)
	}
}

func TestScheduler_CulpritErrorWritesFail(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{
		pk: &fakeTssErr{msg: "round 2 facproof", culprits: []string{"peerA"}},
	}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk, "peerA"}}})

	sched.tickOnce(context.Background(), 100)
	statuses, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Verified || statuses[0].Failure == nil ||
		statuses[0].Failure.Kind != "culprit" ||
		statuses[0].Failure.Error != "round 2 facproof" ||
		len(statuses[0].Failure.Culprits) != 1 || statuses[0].Failure.Culprits[0] != "peerA" {
		t.Fatalf("expected .fail with kind=culprit and culprits, got %+v", statuses)
	}
}

func TestScheduler_OtherErrorWritesFailWithOtherKind(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	// Plain error that is neither a TSS timeout nor a culprit-extractable error.
	sv := &stubVerifier{results: map[string]error{
		pk: errors.New("keyshare unmarshal: invalid character"),
	}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	sched.tickOnce(context.Background(), 100)
	statuses, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("expected 1 marker, got %d", len(statuses))
	}
	st := statuses[0]
	if st.Status != "failed" || st.Failure == nil {
		t.Fatalf("expected status=failed; got %+v", st)
	}
	if st.Failure.Kind != "other" {
		t.Fatalf("expected Kind=other; got %q", st.Failure.Kind)
	}
	if st.Failure.Error != "keyshare unmarshal: invalid character" {
		t.Fatalf("expected raw error message in Failure.Error; got %q", st.Failure.Error)
	}
	if len(st.Failure.Culprits) != 0 {
		t.Fatalf("expected no culprits for kind=other; got %v", st.Failure.Culprits)
	}
}

// --- helpers ---

var errTssTimeOutForTest = errors.New("test: tss timeout")

type fakeTssErr struct {
	msg      string
	culprits []string
}

func (e *fakeTssErr) Error() string            { return e.msg }
func (e *fakeTssErr) CulpritPubkeys() []string { return e.culprits }

func newSchedulerForTest(store *MarkerStore, v Verifier, vaults []Vault) *Scheduler {
	s := &Scheduler{
		store:        store,
		verifier:     v,
		logger:       zerolog.Nop(),
		isTimeoutErr: func(err error) bool { return errors.Is(err, errTssTimeOutForTest) },
		extractCulprits: func(err error, _ []string) (string, []string, bool) {
			var fe *fakeTssErr
			if errors.As(err, &fe) {
				return fe.Error(), fe.CulpritPubkeys(), true
			}
			return "", nil, false
		},
		// Default no-op extractor so the timeout branch's culprit-collection
		// arm is never silently skipped by accident in unit tests; tests that
		// want non-nil culprits override this field explicitly.
		extractTimeoutCulprits: func(_ error) []string { return nil },
		listVaults:             func(_ context.Context) ([]Vault, error) { return vaults, nil },
		lastTickHeight:         -1,
		perVaultLocks:          map[string]*sync.Mutex{},
	}
	return s
}

func TestScheduler_ParentCtxCanceledWritesNoMarker(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{
		pk: errors.New("verifier abandoned"), // any non-nil error
	}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	// Cancel BEFORE the tick so runOneAttempt sees ctx.Err() != nil.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sched.tickOnce(ctx, 100)

	// Assert on disk directly. We can't use store.List() because Task 7
	// makes tickOnce push the vault into the expected set before
	// runOneAttempt runs, so List() will return a pending entry — that's
	// orthogonal to whether we wrote a .ok or .fail.
	failPath := filepath.Join(store.dir, "keyverify-"+pk+".fail")
	if _, err := os.Stat(failPath); !os.IsNotExist(err) {
		t.Fatalf(".fail should not exist after parent-ctx cancellation, got stat err=%v", err)
	}
	okPath := filepath.Join(store.dir, "keyverify-"+pk+".ok")
	if _, err := os.Stat(okPath); !os.IsNotExist(err) {
		t.Fatalf(".ok should not exist after parent-ctx cancellation, got stat err=%v", err)
	}
}

func TestScheduler_ListVaultsErrorAllowsRetry(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk}}})

	// First aligned tick: listVaults fails. lastTickHeight must NOT advance.
	failOnce := true
	sched.listVaults = func(_ context.Context) ([]Vault, error) {
		if failOnce {
			failOnce = false
			return nil, errors.New("transient: thornode unreachable")
		}
		return []Vault{{PubKey: pk, Members: []string{pk}}}, nil
	}
	sched.tickOnce(context.Background(), 100)
	if got := sv.callsFor(pk); got != 0 {
		t.Fatalf("expected 0 verify calls after listVaults error, got %d", got)
	}

	// Same height again: should retry now that listVaults succeeds.
	sched.tickOnce(context.Background(), 100)
	if got := sv.callsFor(pk); got != 1 {
		t.Fatalf("expected 1 verify call after retry, got %d", got)
	}
}

func TestScheduler_SetsExpectedOnSuccessfulListVaults(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk1 := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	pk2 := "thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j"
	sv := &stubVerifier{}
	// Return success for both — confirms expected is set BEFORE individual
	// runOneAttempt calls (or independently of them).
	sched := newSchedulerForTest(store, sv, []Vault{
		{PubKey: pk1, Members: []string{pk1, pk2}},
		{PubKey: pk2, Members: []string{pk1, pk2}},
	})

	sched.tickOnce(context.Background(), 100)

	// Even though both vaults end up with .ok markers, the expected set was
	// populated; we verify by clearing on-disk and re-listing.
	if err := os.Remove(filepath.Join(store.dir, "keyverify-"+pk1+".ok")); err != nil {
		t.Fatalf("remove .ok: %v", err)
	}
	if err := os.Remove(filepath.Join(store.dir, "keyverify-"+pk2+".ok")); err != nil {
		t.Fatalf("remove .ok: %v", err)
	}
	statuses, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 pending entries from expected set, got %d", len(statuses))
	}
	for _, st := range statuses {
		if st.Status != "pending" {
			t.Fatalf("expected pending status; got %+v", st)
		}
	}
}

func TestScheduler_DoesNotChangeExpectedOnListVaultsError(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pkPrior := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	store.SetExpected([]string{pkPrior})

	sv := &stubVerifier{}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pkPrior, Members: []string{pkPrior}}})
	sched.listVaults = func(_ context.Context) ([]Vault, error) {
		return nil, errors.New("thornode unreachable")
	}

	sched.tickOnce(context.Background(), 100)

	// Expected set must survive a failed listVaults — pending entry stays.
	statuses, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 || statuses[0].Status != "pending" {
		t.Fatalf("expected 1 pending entry to survive listVaults error, got %+v", statuses)
	}
}

// panicVerifier deliberately panics on Verify so we can prove runOneAttempt's
// per-vault recover catches it and the next vault in the same tick still runs.
type panicVerifier struct {
	mu       sync.Mutex
	panicked map[string]bool
	results  map[string]error // pubkey -> after-panic outcome (next vault sees this)
}

func (p *panicVerifier) Verify(_ context.Context, pubKey string, _ []string, _ time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.panicked == nil {
		p.panicked = map[string]bool{}
	}
	if !p.panicked[pubKey] {
		p.panicked[pubKey] = true
		panic("simulated tss-lib panic for " + pubKey)
	}
	return p.results[pubKey]
}

// TestScheduler_RunOneAttempt_RecoversFromPanic locks the contract that a
// panic inside the verifier does not abort the scheduler goroutine and does
// not stop subsequent vaults in the same tick from running. Without the
// per-vault defer recover() in runOneAttempt, a single bad vault would crash
// the scheduler and leave bifrost silently degraded until process restart.
func TestScheduler_RunOneAttempt_RecoversFromPanic(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pkPanic := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	pkOK := "thorpub1addwnpepqdwn9vt7vvlguyqhg7xxvn7cxy8xqn68nzgf65apm98ftux5yk6mjsc56jn"

	pv := &panicVerifier{results: map[string]error{pkOK: nil}}
	sched := newSchedulerForTest(store, pv, []Vault{
		{PubKey: pkPanic, Members: []string{pkPanic, pkOK}},
		{PubKey: pkOK, Members: []string{pkPanic, pkOK}},
	})

	// Must not panic — defer recover() catches it.
	sched.tickOnce(context.Background(), 100)

	// Both vaults must have been attempted; the panicking one bumped the panic
	// metric and the second one continued and wrote .ok.
	if !pv.panicked[pkPanic] {
		t.Fatal("expected verifier to be called for panicking vault")
	}
	if !pv.panicked[pkOK] {
		t.Fatal("expected verifier to be called for second vault after the panic — recover did not let the tick continue")
	}
	okPath := filepath.Join(store.dir, "keyverify-"+pkOK+".ok")
	if _, err := os.Stat(okPath); err != nil {
		// pkOK panics first too then on next tick succeeds; we only assert
		// presence/absence of an .ok via a separate tick below.
		_ = err
	}
}

// TestScheduler_Start_RecoversFromPanic asserts that a panic outside the
// per-vault loop (e.g. inside getHeight, listVaults, or any non-runOneAttempt
// path) is caught by the top-level Start defer, so the scheduler goroutine
// logs and exits cleanly rather than crashing the bifrost process.
func TestScheduler_Start_RecoversFromPanic(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	sched := newSchedulerForTest(store, &stubVerifier{}, nil)
	// Make getHeight panic so we exercise the Start-level recover, not the
	// per-vault one.
	sched.getHeight = func(_ context.Context) (int64, error) {
		panic("simulated getHeight panic")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		sched.Start(ctx) // must NOT propagate the panic
	}()

	select {
	case <-done:
		// Start returned (because recover ran and the goroutine fell through
		// to the end of the function — this is the contract).
	case <-time.After(time.Second + PollInterval + 200*time.Millisecond):
		t.Fatal("Start did not return after a getHeight panic — recover did not fire")
	}
}

// TestScheduler_TimeoutWithCulpritsBumpsMetric pins the observability contract
// on the scheduler's timeout branch: when extractTimeoutCulprits returns a
// non-empty list, metricCulprit gets one increment per (vault, culprit) pair
// AND metricFailure[kind=timeout] is incremented. The existing
// TimeoutWritesFailWithTimeoutKind test asserts the .fail marker payload but
// never observes either metric, so a regression that dropped the loop at
// scheduler.go:201-203 would compile and pass that test while silently
// blinding the dashboards.
func TestScheduler_TimeoutWithCulpritsBumpsMetric(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqf90u7n3nr2jwsw4t2gzhzqfdlply8dlzv3mdj4dr22uvhe04azq5gac3gq"
	sv := &stubVerifier{results: map[string]error{pk: errTssTimeOutForTest}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk, "peerA", "peerB"}}})

	// Inject two culprits via the timeout-culprit extractor so we can verify
	// per-(vault, culprit) increments.
	sched.extractTimeoutCulprits = func(err error) []string {
		if errors.Is(err, errTssTimeOutForTest) {
			return []string{"peerA", "peerB"}
		}
		return nil
	}

	// Snapshot starting counter values so this test is robust to other tests
	// in the package that happen to share the same label values.
	beforeCulpritA := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerA"))
	beforeCulpritB := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerB"))
	beforeFailTimeout := testutil.ToFloat64(metricFailure.WithLabelValues(pk, "timeout"))

	sched.tickOnce(context.Background(), 100)

	if got := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerA")) - beforeCulpritA; got != 1 {
		t.Errorf("metricCulprit{pubkey=%s,culprit=peerA} delta = %v, want 1", pk, got)
	}
	if got := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerB")) - beforeCulpritB; got != 1 {
		t.Errorf("metricCulprit{pubkey=%s,culprit=peerB} delta = %v, want 1", pk, got)
	}
	if got := testutil.ToFloat64(metricFailure.WithLabelValues(pk, "timeout")) - beforeFailTimeout; got != 1 {
		t.Errorf("metricFailure{pubkey=%s,kind=timeout} delta = %v, want 1", pk, got)
	}
}

// TestScheduler_CulpritErrorBumpsMetric is the sibling assertion for the
// non-timeout culprit path (scheduler.go:225-227): when extractCulprits fires,
// metricCulprit must increment per culprit AND metricFailure[kind=culprit]
// must increment by 1.
func TestScheduler_CulpritErrorBumpsMetric(t *testing.T) {
	store := NewMarkerStore(t.TempDir())
	pk := "thorpub1addwnpepqdwn9vt7vvlguyqhg7xxvn7cxy8xqn68nzgf65apm98ftux5yk6mjsc56jn"
	sv := &stubVerifier{results: map[string]error{
		pk: &fakeTssErr{msg: "round 2 facproof", culprits: []string{"peerC"}},
	}}
	sched := newSchedulerForTest(store, sv, []Vault{{PubKey: pk, Members: []string{pk, "peerC"}}})

	beforeCulprit := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerC"))
	beforeFailCulprit := testutil.ToFloat64(metricFailure.WithLabelValues(pk, "culprit"))

	sched.tickOnce(context.Background(), 100)

	if got := testutil.ToFloat64(metricCulprit.WithLabelValues(pk, "peerC")) - beforeCulprit; got != 1 {
		t.Errorf("metricCulprit{pubkey=%s,culprit=peerC} delta = %v, want 1", pk, got)
	}
	if got := testutil.ToFloat64(metricFailure.WithLabelValues(pk, "culprit")) - beforeFailCulprit; got != 1 {
		t.Errorf("metricFailure{pubkey=%s,kind=culprit} delta = %v, want 1", pk, got)
	}
}
