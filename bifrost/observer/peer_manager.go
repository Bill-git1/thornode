package observer

import (
	"fmt"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/rs/zerolog"
)

const semAcquireTimeout = 50 * time.Millisecond

type peerSemaphore struct {
	tokens   chan struct{}
	refCount int
	lastZero time.Time
}

type peerManager struct {
	logger zerolog.Logger

	semaphores map[peer.ID]*peerSemaphore
	mu         sync.Mutex
	limit      int
}

func newPeerManager(logger zerolog.Logger, limit int) *peerManager {
	return &peerManager{
		logger:     logger.With().Str("component", "peer_manager").Logger(),
		semaphores: make(map[peer.ID]*peerSemaphore),
		limit:      limit,
	}
}

func (m *peerManager) getSemaphoreForAcquire(peer peer.ID) *peerSemaphore {
	m.mu.Lock()
	defer m.mu.Unlock()
	sem, exists := m.semaphores[peer]
	if !exists {
		sem = &peerSemaphore{
			tokens:   make(chan struct{}, m.limit),
			refCount: 0,
		}
		m.semaphores[peer] = sem
	}
	sem.refCount++
	return sem
}

func (m *peerManager) acquire(peer peer.ID) (*peerSemaphore, error) {
	sem := m.getSemaphoreForAcquire(peer)
	// Try to acquire token with timeout
	select {
	case sem.tokens <- struct{}{}: // Acquire the semaphore
		return sem, nil

	case <-time.After(semAcquireTimeout): // Short timeout to avoid blocking too long
		m.decRefCount(sem)
		return nil, fmt.Errorf("peer %s is busy", peer.String())
	}
}

func (m *peerManager) release(sem *peerSemaphore) {
	// Release token
	<-sem.tokens

	// Clean up semaphore if this was the last reference
	m.decRefCount(sem)
}

func (m *peerManager) decRefCount(sem *peerSemaphore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sem.refCount--
	if sem.refCount <= 0 {
		// do not delete here, delete in main loop periodically if ref counts are zero
		sem.lastZero = time.Now()
	}
}

func (m *peerManager) prune() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for peerID, sem := range m.semaphores {
		if sem.refCount == 0 && time.Since(sem.lastZero) >= semaphorePruneInterval {
			delete(m.semaphores, peerID)
			m.logger.Debug().Msgf("pruned semaphore for peer: %s", peerID)
		}
	}
}
