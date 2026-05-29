// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

const (
	// BlockInterval — run when (thornode height % 100 == 0)
	BlockInterval = 100
	// PollInterval — how often to poll thornode height
	PollInterval = 6 * time.Second
	// PerVaultTimeout — bound per-vault verify attempt
	PerVaultTimeout = 90 * time.Second
	// StaggerStep — per-party-index delay before this node initiates round 1
	StaggerStep = 200 * time.Millisecond
)

// Vault is the slim per-vault info the scheduler needs. Constructed from
// thornode's stypes.Vault by the listVaults adapter; defined here so callers
// (and tests) don't need to import the thorchain types.
type Vault struct {
	PubKey  string
	Members []string // member thorpubs, sorted (sort order determines stagger)
}

// Scheduler runs the periodic keyverify loop.
type Scheduler struct {
	store                  *MarkerStore
	verifier               Verifier
	logger                 zerolog.Logger
	getHeight              func(context.Context) (int64, error)
	listVaults             func(context.Context) ([]Vault, error)
	localPubKey            string
	isTimeoutErr           func(error) bool
	extractCulprits        func(err error, members []string) (msg string, culprits []string, ok bool)
	extractTimeoutCulprits func(err error) []string

	lastTickHeight int64
	vaultMu        sync.Mutex
	perVaultLocks  map[string]*sync.Mutex
}

// NewScheduler constructs the production scheduler. The closures (getHeight,
// listVaults, isTimeoutErr, extractCulprits) are injected so this package has
// no compile-time dependency on thorclient or tss — keeping it self-contained
// and removable.
func NewScheduler(
	store *MarkerStore,
	verifier Verifier,
	logger zerolog.Logger,
	getHeight func(context.Context) (int64, error),
	listVaults func(context.Context) ([]Vault, error),
	localPubKey string,
	isTimeoutErr func(error) bool,
	extractCulprits func(err error, members []string) (string, []string, bool),
	extractTimeoutCulprits func(err error) []string,
) *Scheduler {
	return &Scheduler{
		store:                  store,
		verifier:               verifier,
		logger:                 logger.With().Str("module", "keyverify").Logger(),
		getHeight:              getHeight,
		listVaults:             listVaults,
		localPubKey:            localPubKey,
		isTimeoutErr:           isTimeoutErr,
		extractCulprits:        extractCulprits,
		extractTimeoutCulprits: extractTimeoutCulprits,
		lastTickHeight:         -1,
		perVaultLocks:          map[string]*sync.Mutex{},
	}
}

// Start blocks until ctx is cancelled. Polls thornode height every
// PollInterval; on aligned heights, calls tickOnce.
//
// A top-level defer recover() guards against panics from downstream tss-lib,
// metrics, or other dependencies taking the scheduler goroutine down silently.
// runOneAttempt has its own per-vault recover so a single bad vault doesn't
// abort the rest of the tick; the recover here is a belt-and-suspenders guard
// for panics outside the per-vault loop (e.g. in getHeight or tickOnce setup).
func (s *Scheduler) Start(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error().Interface("panic", r).Msg("keyverify scheduler panic; goroutine exiting")
		}
	}()
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		h, err := s.getHeight(ctx)
		if err != nil {
			s.logger.Warn().Err(err).Msg("fetch thornode height")
			continue
		}
		s.tickOnce(ctx, h)
	}
}

// tickOnce decides whether to run a tick at the given height and runs it.
//
// Fires once per BlockInterval-aligned window. The "window" for height h is
// floor(h/BlockInterval)*BlockInterval, and we fire as soon as we observe a
// height whose window is greater than the last window we ticked in. This
// makes the scheduler drift-tolerant: with a 6s poll cadence and ~6s blocks
// we routinely skip the exact multiple, and validators running on slightly
// different wall-clocks would never co-fire on the same height. The window
// rule lets every validator land on the same logical tick even when their
// observed heights for that tick differ by a few blocks.
func (s *Scheduler) tickOnce(ctx context.Context, height int64) {
	window := (height / BlockInterval) * BlockInterval
	if window == 0 {
		// height is below the first BlockInterval boundary; nothing to do.
		return
	}
	if s.lastTickHeight >= 0 && window <= s.lastTickHeight {
		return
	}

	vaults, err := s.listVaults(ctx)
	if err != nil {
		s.logger.Warn().Err(err).Int64("height", height).Int64("window", window).Msg("list vaults")
		return
	}
	s.lastTickHeight = window

	pubkeys := make([]string, 0, len(vaults))
	for _, v := range vaults {
		pubkeys = append(pubkeys, v.PubKey)
	}
	s.store.SetExpected(pubkeys)

	for _, v := range vaults {
		if s.store.HasSuccess(v.PubKey) {
			continue
		}
		// staggerFor is a per-party-index delay that lower-indexed peers do
		// NOT sleep through up front; we pass it to the verifier which
		// subscribes first and sleeps only between subscription and the
		// local protocol start. This keeps early broadcasts from peers
		// with smaller delays from being dropped at this node.
		delay := s.staggerFor(v.Members)
		s.runOneAttempt(ctx, window, v, delay)
	}
}

func (s *Scheduler) staggerFor(members []string) time.Duration {
	sorted := append([]string(nil), members...)
	sort.Strings(sorted)
	for i, m := range sorted {
		if m == s.localPubKey {
			return time.Duration(i) * StaggerStep
		}
	}
	return 0
}

func (s *Scheduler) runOneAttempt(ctx context.Context, height int64, v Vault, startDelay time.Duration) {
	// Per-vault panic recovery: a panic inside the verifier (downstream tss-lib,
	// the curve lock, the metrics collector) must not poison the other vaults
	// in the same tick or kill the scheduler. Log + record as a failure metric
	// and move on; the next tick retries.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error().Interface("panic", r).Str("pubkey", v.PubKey).Int64("height", height).Msg("keyverify attempt panic")
			metricFailure.WithLabelValues(v.PubKey, "panic").Inc()
		}
	}()

	s.vaultMu.Lock()
	lk, ok := s.perVaultLocks[v.PubKey]
	if !ok {
		lk = &sync.Mutex{}
		s.perVaultLocks[v.PubKey] = lk
	}
	s.vaultMu.Unlock()

	if !lk.TryLock() {
		s.logger.Debug().Str("pubkey", v.PubKey).Msg("attempt already in flight; skipping")
		return
	}
	defer lk.Unlock()

	metricAttempts.WithLabelValues(v.PubKey).Inc()
	start := time.Now()

	attemptCtx, cancel := context.WithTimeout(WithHeight(ctx, height), PerVaultTimeout)
	defer cancel()
	err := s.verifier.Verify(attemptCtx, v.PubKey, v.Members, startDelay)
	dur := time.Since(start).Seconds()

	switch {
	case err == nil:
		metricSuccess.WithLabelValues(v.PubKey).Inc()
		metricDuration.WithLabelValues(v.PubKey, "success").Observe(dur)
		if werr := s.store.WriteSuccess(v.PubKey, height); werr != nil {
			s.logger.Error().Err(werr).Str("pubkey", v.PubKey).Msg("write .ok marker")
		} else {
			s.logger.Info().Str("pubkey", v.PubKey).Int64("height", height).Msg("keyverify success")
		}
	case err != nil && ctx.Err() != nil:
		// Parent context canceled — graceful shutdown. No marker, no metric.
		// (ctx here is the scheduler's parent context, not attemptCtx. A
		//  PerVaultTimeout-induced cancellation leaves ctx.Err() == nil and
		//  falls through to the timeout/culprit/other branches below.)
		return
	case s.isTimeoutErr(err):
		metricFailure.WithLabelValues(v.PubKey, "timeout").Inc()
		metricDuration.WithLabelValues(v.PubKey, "timeout").Observe(dur)
		var culprits []string
		if s.extractTimeoutCulprits != nil {
			culprits = s.extractTimeoutCulprits(err)
			for _, c := range culprits {
				metricCulprit.WithLabelValues(v.PubKey, c).Inc()
			}
		}
		rec := FailureRecord{
			PubKey:      v.PubKey,
			Height:      height,
			AttemptedAt: time.Now().UTC(),
			Kind:        "timeout",
			Error:       err.Error(),
			Culprits:    culprits,
		}
		if werr := s.store.WriteFailure(rec); werr != nil {
			s.logger.Error().Err(werr).Str("pubkey", v.PubKey).Msg("write .fail marker (timeout)")
		}
		logEvt := s.logger.Warn().Err(err).Str("pubkey", v.PubKey).Int64("height", height)
		if len(culprits) > 0 {
			logEvt = logEvt.Strs("non_participating", culprits)
		}
		logEvt.Msg("keyverify timeout (peers offline?)")
	default:
		if msg, culprits, ok := s.extractCulprits(err, v.Members); ok {
			metricFailure.WithLabelValues(v.PubKey, "culprit").Inc()
			metricDuration.WithLabelValues(v.PubKey, "culprit").Observe(dur)
			for _, c := range culprits {
				metricCulprit.WithLabelValues(v.PubKey, c).Inc()
			}
			rec := FailureRecord{
				PubKey:      v.PubKey,
				Height:      height,
				AttemptedAt: time.Now().UTC(),
				Kind:        "culprit",
				Error:       msg,
				Culprits:    culprits,
			}
			if werr := s.store.WriteFailure(rec); werr != nil {
				s.logger.Error().Err(werr).Str("pubkey", v.PubKey).Msg("write .fail marker (culprit)")
			} else {
				s.logger.Error().Strs("culprits", culprits).Str("pubkey", v.PubKey).Int64("height", height).Msg("keyverify failed with culprits")
			}
			return
		}
		metricFailure.WithLabelValues(v.PubKey, "other").Inc()
		metricDuration.WithLabelValues(v.PubKey, "other").Observe(dur)
		rec := FailureRecord{
			PubKey:      v.PubKey,
			Height:      height,
			AttemptedAt: time.Now().UTC(),
			Kind:        "other",
			Error:       err.Error(),
			Culprits:    nil,
		}
		if werr := s.store.WriteFailure(rec); werr != nil {
			s.logger.Error().Err(werr).Str("pubkey", v.PubKey).Msg("write .fail marker (other)")
		}
		s.logger.Error().Err(err).Str("pubkey", v.PubKey).Int64("height", height).Msg("keyverify unexpected error")
	}
}
