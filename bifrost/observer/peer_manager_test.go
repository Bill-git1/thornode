package observer

import (
	"sync"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeerManager(t *testing.T) {
	// Create a test logger
	logger := zerolog.Nop()

	t.Run("acquires and releases semaphore", func(t *testing.T) {
		pm := newPeerManager(logger, 2)
		peerID, err := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
		require.NoError(t, err)

		// First acquisition should succeed
		sem1, err := pm.acquire(peerID)
		require.NoError(t, err)
		require.NotNil(t, sem1)

		// Second acquisition should also succeed (limit is 2)
		sem2, err := pm.acquire(peerID)
		require.NoError(t, err)
		require.NotNil(t, sem2)

		// Third acquisition should timeout/fail
		sem3, err := pm.acquire(peerID)
		assert.Error(t, err)
		assert.Nil(t, sem3)

		// Release one token
		pm.release(sem1)

		// Now acquisition should succeed again
		sem4, err := pm.acquire(peerID)
		require.NoError(t, err)
		require.NotNil(t, sem4)

		// Release remaining tokens
		pm.release(sem2)
		pm.release(sem4)
	})

	t.Run("prunes unused semaphores", func(t *testing.T) {
		pm := newPeerManager(logger, 2)

		// Create two different peer IDs
		peerID1, err := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
		require.NoError(t, err)
		peerID2, err := peer.Decode("QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ")
		require.NoError(t, err)

		// Acquire and release for both peers
		sem1, err := pm.acquire(peerID1)
		require.NoError(t, err)
		sem2, err := pm.acquire(peerID2)
		require.NoError(t, err)

		pm.release(sem1)
		pm.release(sem2)

		// Verify both peers have semaphores
		pm.mu.Lock()
		assert.Len(t, pm.semaphores, 2)
		pm.mu.Unlock()

		// Override the lastZero time to simulate that peerID1's semaphore has been unused for longer than the prune interval
		pm.mu.Lock()
		pm.semaphores[peerID1].lastZero = time.Now().Add(-2 * semaphorePruneInterval)
		pm.mu.Unlock()

		// Run prune
		pm.prune()

		// Verify peerID1's semaphore was pruned, but peerID2's remains
		pm.mu.Lock()
		assert.Len(t, pm.semaphores, 1)
		_, exists := pm.semaphores[peerID1]
		assert.False(t, exists)
		_, exists = pm.semaphores[peerID2]
		assert.True(t, exists)
		pm.mu.Unlock()
	})

	t.Run("handles concurrent operations", func(t *testing.T) {
		pm := newPeerManager(logger, 5)
		peerID, err := peer.Decode("QmYyQSo1c1Ym7orWxLYvCrM2EmxFTANf8wXmmE7DWjhx5N")
		require.NoError(t, err)

		var wg sync.WaitGroup
		activeCount := 0
		maxActive := 0
		var countMu sync.Mutex

		// Launch 20 concurrent goroutines all trying to acquire
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()

				sem, err := pm.acquire(peerID)
				if err == nil {
					// Successfully acquired, track concurrent usage
					countMu.Lock()
					activeCount++
					if activeCount > maxActive {
						maxActive = activeCount
					}
					countMu.Unlock()

					// Simulate work
					time.Sleep(10 * time.Millisecond)

					countMu.Lock()
					activeCount--
					countMu.Unlock()

					pm.release(sem)
				}
			}()
		}

		wg.Wait()

		// Verify concurrency was limited
		assert.LessOrEqual(t, maxActive, 5, "Concurrency limit should be respected")
		assert.Greater(t, maxActive, 0, "At least some operations should succeed")

		// After all operations, semaphore should still exist but have 0 active tokens
		pm.mu.Lock()
		defer pm.mu.Unlock()
		sem, exists := pm.semaphores[peerID]
		assert.True(t, exists)
		assert.Equal(t, 0, sem.refCount)
	})
}
