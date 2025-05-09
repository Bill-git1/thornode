package observer

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog"
	"gitlab.com/thorchain/thornode/v3/bifrost/metrics"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/common"
)

// we have one semaphore per peer (per active validator node), so we don't need to prune that often
// but we do need to prune it, because we don't want to keep semaphores of nodes that aren't
// online anymore around forever.
const semaphorePruneInterval = 5 * time.Minute

type AttestationBatcher struct {
	observedTxBatch []*common.AttestTx
	networkFeeBatch []*common.AttestNetworkFee
	solvencyBatch   []*common.AttestSolvency
	errataTxBatch   []*common.AttestErrataTx

	batchPool sync.Pool

	mu                  sync.Mutex
	batchInterval       time.Duration
	maxBatchSize        int
	peerTimeout         time.Duration // Timeout for peer send
	peerConcurrentSends int           // Number of concurrent sends to a single peer

	getActiveValidators func() map[peer.ID]bool

	lastBatchSent time.Time
	batchTicker   *time.Ticker
	forceSendChan chan struct{} // Channel to signal immediate send

	host    host.Host
	logger  zerolog.Logger
	metrics *batchMetrics

	peerSemaphores   map[peer.ID]*peerSemaphore
	peerSemaphoresMu sync.Mutex
}

type peerSemaphore struct {
	tokens   chan struct{}
	refCount int
	lastZero time.Time
}

// Metrics for the batcher
type batchMetrics struct {
	BatchSends      prometheus.Counter
	BatchClears     prometheus.Counter
	MessagesBatched *prometheus.CounterVec // By type
	BatchSize       prometheus.Histogram
	BatchSendTime   prometheus.Histogram
}

// NewAttestationBatcher creates a new instance of AttestationBatcher
func NewAttestationBatcher(
	host host.Host,
	logger zerolog.Logger,
	m *metrics.Metrics,
	batchInterval time.Duration,
	maxBatchSize int,
	peerTimeout time.Duration,
	peerConcurrentSends int,
) *AttestationBatcher {
	// Set default values if not specified
	if batchInterval == 0 {
		batchInterval = 2 * time.Second // Default to 2 second
	}

	if maxBatchSize == 0 {
		maxBatchSize = 100 // Default max batch size
	}

	if peerTimeout == 0 {
		peerTimeout = 20 * time.Second // Default peer timeout
	}

	if peerConcurrentSends == 0 {
		peerConcurrentSends = 4 // Default concurrent sends
	}

	// Create batch metrics
	batchMetrics := &batchMetrics{
		BatchSends:      m.GetCounter(metrics.BatchSends),
		BatchClears:     m.GetCounter(metrics.BatchClears),
		MessagesBatched: m.GetCounterVec(metrics.MessagesBatched),
		BatchSize:       m.GetHistograms(metrics.BatchSize),
		BatchSendTime:   m.GetHistograms(metrics.BatchSendTime),
	}

	logger = logger.With().Str("module", "attestation_batcher").Logger()

	return &AttestationBatcher{
		// Initialize with empty slices with initial capacity
		observedTxBatch: make([]*common.AttestTx, 0, maxBatchSize),
		networkFeeBatch: make([]*common.AttestNetworkFee, 0, maxBatchSize),
		solvencyBatch:   make([]*common.AttestSolvency, 0, maxBatchSize),
		errataTxBatch:   make([]*common.AttestErrataTx, 0, maxBatchSize),

		batchInterval:       batchInterval,
		maxBatchSize:        maxBatchSize,
		peerTimeout:         peerTimeout,
		peerConcurrentSends: peerConcurrentSends,

		peerSemaphores: make(map[peer.ID]*peerSemaphore),

		lastBatchSent: time.Time{}, // Zero time

		host:    host,
		logger:  logger,
		metrics: batchMetrics,

		// sync.Pool for reusing AttestationBatch objects to reduce memory allocations.
		batchPool: sync.Pool{
			New: func() interface{} {
				return &common.AttestationBatch{
					AttestTxs:         make([]*common.AttestTx, 0, maxBatchSize), // Preallocate with maxBatchSize capacity
					AttestNetworkFees: make([]*common.AttestNetworkFee, 0, maxBatchSize),
					AttestSolvencies:  make([]*common.AttestSolvency, 0, maxBatchSize),
					AttestErrataTxs:   make([]*common.AttestErrataTx, 0, maxBatchSize),
				}
			},
		},
	}
}

func (b *AttestationBatcher) setActiveValGetter(getter func() map[peer.ID]bool) {
	b.getActiveValidators = getter
}

func (b *AttestationBatcher) Start(ctx context.Context) {
	b.batchTicker = time.NewTicker(b.batchInterval)
	b.forceSendChan = make(chan struct{}, 1) // Buffer of 1 to avoid blocking
	semPruneTicker := time.NewTicker(semaphorePruneInterval)

	defer func() {
		b.batchTicker.Stop()
		semPruneTicker.Stop()
		close(b.forceSendChan)
	}()

	for {
		select {
		case <-ctx.Done():
			b.sendBatches(ctx, true)
			return
		case <-b.batchTicker.C:
			b.sendBatches(ctx, false)
		case <-b.forceSendChan:
			b.sendBatches(ctx, true)
		case <-semPruneTicker.C:
			// Periodically prune semaphores that have been idle for a while
			b.peerSemaphoresMu.Lock()
			for peerID, sem := range b.peerSemaphores {
				if sem.refCount == 0 && time.Since(sem.lastZero) >= semaphorePruneInterval {
					delete(b.peerSemaphores, peerID)
					b.logger.Debug().Msgf("pruned semaphore for peer: %s", peerID)
				}
			}
			b.peerSemaphoresMu.Unlock()
		}
	}
}

func (b *AttestationBatcher) sendBatches(ctx context.Context, force bool) {
	b.mu.Lock()

	// Only send if we have messages to send or enough time has passed
	hasMessages := len(b.observedTxBatch) > 0 || len(b.networkFeeBatch) > 0 ||
		len(b.solvencyBatch) > 0 || len(b.errataTxBatch) > 0
	timeThresholdMet := time.Since(b.lastBatchSent) >= b.batchInterval

	if !hasMessages || (!timeThresholdMet && !force) {
		b.mu.Unlock()
		return
	}

	start := time.Now()
	// Get a batched message from the pool
	batch, ok := b.batchPool.Get().(*common.AttestationBatch)
	if !ok {
		batch = &common.AttestationBatch{
			AttestTxs:         make([]*common.AttestTx, 0, b.maxBatchSize),
			AttestNetworkFees: make([]*common.AttestNetworkFee, 0, b.maxBatchSize),
			AttestSolvencies:  make([]*common.AttestSolvency, 0, b.maxBatchSize),
			AttestErrataTxs:   make([]*common.AttestErrataTx, 0, b.maxBatchSize),
		}
	}
	// Populate the batch
	batch.AttestTxs = append(batch.AttestTxs[:0], b.observedTxBatch...)
	batch.AttestNetworkFees = append(batch.AttestNetworkFees[:0], b.networkFeeBatch...)
	batch.AttestSolvencies = append(batch.AttestSolvencies[:0], b.solvencyBatch...)
	batch.AttestErrataTxs = append(batch.AttestErrataTxs[:0], b.errataTxBatch...)

	txCount, nfCount, solvencyCount, errataCount := len(batch.AttestTxs), len(batch.AttestNetworkFees), len(batch.AttestSolvencies), len(batch.AttestErrataTxs)
	b.mu.Unlock()

	// Send to all peers
	b.broadcastToAllPeers(ctx, *batch)

	// Return the batch to the pool after clearing it
	batch.AttestTxs = batch.AttestTxs[:0]
	batch.AttestNetworkFees = batch.AttestNetworkFees[:0]
	batch.AttestSolvencies = batch.AttestSolvencies[:0]
	batch.AttestErrataTxs = batch.AttestErrataTxs[:0]
	b.batchPool.Put(batch)

	batchDuration := time.Since(start)
	b.metrics.MessagesBatched.WithLabelValues("observed_tx").Add(float64(txCount))
	b.metrics.MessagesBatched.WithLabelValues("network_fee").Add(float64(nfCount))
	b.metrics.MessagesBatched.WithLabelValues("solvency").Add(float64(solvencyCount))
	b.metrics.MessagesBatched.WithLabelValues("errata_tx").Add(float64(errataCount))
	b.metrics.BatchSendTime.Observe(batchDuration.Seconds())
	b.metrics.BatchSends.Inc()

	// Clear batches and update timestamp
	b.clearBatches()
	b.lastBatchSent = time.Now()
}

// AddObservedTx adds an observed transaction attestation to the batch
func (b *AttestationBatcher) AddObservedTx(tx common.AttestTx) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.observedTxBatch = append(b.observedTxBatch, &tx)

	// If we've reached the maximum batch size, trigger an immediate send
	if len(b.observedTxBatch) >= b.maxBatchSize {
		b.logger.Debug().
			Int("batch_size", len(b.observedTxBatch)).
			Msg("observed tx batch reached max size, triggering immediate send")

		// Use a separate goroutine to avoid deadlock since sendBatches also acquires the mu
		go b.triggerBatchSend()
	}
}

// AddNetworkFee adds a network fee attestation to the batch
func (b *AttestationBatcher) AddNetworkFee(fee common.AttestNetworkFee) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.networkFeeBatch = append(b.networkFeeBatch, &fee)

	// If we've reached the maximum batch size, trigger an immediate send
	if len(b.networkFeeBatch) >= b.maxBatchSize {
		b.logger.Debug().
			Int("batch_size", len(b.networkFeeBatch)).
			Msg("network fee batch reached max size, triggering immediate send")

		go b.triggerBatchSend()
	}
}

// AddSolvency adds a solvency attestation to the batch
func (b *AttestationBatcher) AddSolvency(solvency common.AttestSolvency) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.solvencyBatch = append(b.solvencyBatch, &solvency)

	// If we've reached the maximum batch size, trigger an immediate send
	if len(b.solvencyBatch) >= b.maxBatchSize {
		b.logger.Debug().
			Int("batch_size", len(b.solvencyBatch)).
			Msg("solvency batch reached max size, triggering immediate send")

		go b.triggerBatchSend()
	}
}

// AddErrataTx adds an errata transaction attestation to the batch
func (b *AttestationBatcher) AddErrataTx(errata common.AttestErrataTx) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.errataTxBatch = append(b.errataTxBatch, &errata)

	// If we've reached the maximum batch size, trigger an immediate send
	if len(b.errataTxBatch) >= b.maxBatchSize {
		b.logger.Debug().
			Int("batch_size", len(b.errataTxBatch)).
			Msg("errata tx batch reached max size, triggering immediate send")

		go b.triggerBatchSend()
	}
}

// triggerBatchSend triggers an immediate batch send outside the regular interval
func (b *AttestationBatcher) triggerBatchSend() {
	select {
	case b.forceSendChan <- struct{}{}:
		// Successfully triggered a send
	default:
		// Channel is full, a send is already pending
	}
}

type legacyAtt struct {
	protocolID protocol.ID
	payload    []byte
}

// broadcastToAllPeers sends the batch payload to all connected peers without blocking on slow peers
func (b *AttestationBatcher) broadcastToAllPeers(ctx context.Context, batch common.AttestationBatch) {
	// Marshal the batch
	payload, err := batch.Marshal()
	if err != nil {
		b.logger.Error().Err(err).Msg("failed to marshal attestation batch")
		return
	}

	var legacyAttestations []*legacyAtt
	var legacyAttestationsMu sync.Mutex

	// if all peers support the new batch protocol, this will not be called.
	getMarshaledLegacyAttestations := func() []*legacyAtt {
		legacyAttestationsMu.Lock()
		defer legacyAttestationsMu.Unlock()
		if len(legacyAttestations) > 0 {
			// only marshal once if we encounter a peer that doesn't support the new batch protocol
			return legacyAttestations
		}
		legacyAttestations = make([]*legacyAtt, 0, len(batch.AttestTxs)+len(batch.AttestNetworkFees)+len(batch.AttestSolvencies)+len(batch.AttestErrataTxs))
		for _, tx := range batch.AttestTxs {
			legacyPayload, err := tx.Marshal()
			if err != nil {
				b.logger.Error().Err(err).Msg("failed to marshal tx attestation")
				continue
			}
			legacyAttestations = append(legacyAttestations, &legacyAtt{
				protocolID: observeTxProtocol,
				payload:    legacyPayload,
			})
		}
		for _, fee := range batch.AttestNetworkFees {
			legacyPayload, err := fee.Marshal()
			if err != nil {
				b.logger.Error().Err(err).Msg("failed to marshal net fee attestation")
				continue
			}
			legacyAttestations = append(legacyAttestations, &legacyAtt{
				protocolID: networkFeeProtocol,
				payload:    legacyPayload,
			})
		}
		for _, solvency := range batch.AttestSolvencies {
			legacyPayload, err := solvency.Marshal()
			if err != nil {
				b.logger.Error().Err(err).Msg("failed to marshal solvency attestation")
				continue
			}
			legacyAttestations = append(legacyAttestations, &legacyAtt{
				protocolID: solvencyProtocol,
				payload:    legacyPayload,
			})
		}
		for _, errata := range batch.AttestErrataTxs {
			legacyPayload, err := errata.Marshal()
			if err != nil {
				b.logger.Error().Err(err).Msg("failed to marshal errata tx attestation")
				continue
			}
			legacyAttestations = append(legacyAttestations, &legacyAtt{
				protocolID: errataTxProtocol,
				payload:    legacyPayload,
			})
		}
		return legacyAttestations
	}

	peers := b.host.Peerstore().Peers()

	if b.getActiveValidators == nil {
		b.logger.Warn().Msg("active validator getter not set – skipping broadcast")
		return
	}
	// Skip self, send to all other active vals that we are peered with
	activeVals := b.getActiveValidators()
	var peersToSend []peer.ID
	for _, peerID := range peers {
		if peerID == b.host.ID() {
			continue
		}
		if _, ok := activeVals[peerID]; !ok {
			continue
		}
		peersToSend = append(peersToSend, peerID)
	}

	if len(peersToSend) == 0 {
		b.logger.Debug().Msg("no peers to broadcast to")
		return
	}

	b.logger.Debug().
		Int("peer_count", len(peersToSend)).
		Int("payload_bytes", len(payload)).
		Msg("broadcasting attestation batch to peers")

	// Launch each send operation in its own goroutine and don't wait for completion
	for _, p := range peersToSend {
		// Launch a goroutine for each peer and don't wait for completion
		go func(peer peer.ID) {
			// Limit the number of concurrent sends to this peer
			// Get or create semaphore with atomic reference counting
			b.peerSemaphoresMu.Lock()
			sem, exists := b.peerSemaphores[peer]
			if !exists {
				sem = &peerSemaphore{
					tokens:   make(chan struct{}, b.peerConcurrentSends),
					refCount: 0,
				}
				b.peerSemaphores[peer] = sem
			}
			sem.refCount++
			b.peerSemaphoresMu.Unlock()

			// Try to acquire token with timeout
			select {
			case sem.tokens <- struct{}{}: // Acquire the semaphore
				defer func() {
					// Release token
					<-sem.tokens

					// Clean up semaphore if this was the last reference
					b.peerSemaphoresMu.Lock()
					sem.refCount--
					if sem.refCount == 0 {
						// do not delete here, delete in main loop periodically if ref count has been zero for a while
						sem.lastZero = time.Now()
					}
					b.peerSemaphoresMu.Unlock()
				}()

				// Create a context with timeout for this specific peer
				peerCtx, cancel := context.WithTimeout(ctx, b.peerTimeout)
				stream, err := b.host.NewStream(peerCtx, peer, batchedAttestationProtocol)
				b.logger.Debug().Msgf("starting attestation send to peer: %s", peer)

				if err != nil {
					cancel()
					if strings.Contains(err.Error(), "protocol not supported") {
						b.logger.Debug().Msgf("peer %s does not support batched attestation protocol, sending unbatched", peer)
						// send unbatched attestations
						legacyAtts := getMarshaledLegacyAttestations()
						for _, att := range legacyAtts {
							peerCtx, cancel = context.WithTimeout(ctx, b.peerTimeout)
							legacyStream, err := b.host.NewStream(peerCtx, peer, att.protocolID)
							if err != nil {
								cancel()
								b.logger.Error().Err(err).Msgf("fail to create stream to peer: %s", peer)
								return
							}
							b.sendPayloadToStream(peerCtx, legacyStream, att.payload)
							cancel()
						}
						return
					}
					b.logger.Error().Err(err).Msgf("fail to create stream to peer: %s", peer)
					return
				}

				b.sendPayloadToStream(peerCtx, stream, payload)
				cancel()

				b.logger.Debug().Msgf("completed attestation send to peer: %s", peer)
			case <-time.After(50 * time.Millisecond): // Short timeout to avoid blocking too long
				// Couldn't acquire token, clean up and return
				b.peerSemaphoresMu.Lock()
				sem.refCount--
				if sem.refCount <= 0 {
					// do not delete here, delete in main loop periodically if ref counts are zero
					sem.lastZero = time.Now()
				}
				b.peerSemaphoresMu.Unlock()

				b.logger.Debug().Msgf("peer %s is busy, skipping this send", peer)
				return
			}
		}(p)
	}

	// Function returns immediately without waiting for sends to complete
	b.logger.Debug().Msg("broadcast initiated to all peers")
}

// clearBatches resets all batch arrays to empty without reallocating
func (b *AttestationBatcher) clearBatches() {
	// Clear all batches in-place to preserve preallocated capacity
	b.observedTxBatch = b.observedTxBatch[:0]
	b.networkFeeBatch = b.networkFeeBatch[:0]
	b.solvencyBatch = b.solvencyBatch[:0]
	b.errataTxBatch = b.errataTxBatch[:0]

	// Log batch clear
	b.logger.Debug().Msg("attestation batches cleared")

	// Update metrics
	b.metrics.BatchClears.Inc()
}

func (b *AttestationBatcher) sendPayloadToStream(ctx context.Context, stream network.Stream, payload []byte) {
	peer := stream.Conn().RemotePeer()

	defer func() {
		if err := stream.Close(); err != nil {
			b.logger.Error().Err(err).Msgf("fail to close stream to peer: %s", peer)
		}

		_ = stream.Reset()
	}()

	if err := p2p.WriteStreamWithBufferWithContext(ctx, payload, stream); err != nil {
		b.logger.Error().Err(err).Msgf("fail to write payload to peer: %s", peer)
		return
	}

	// Wait for acknowledgment
	reply, err := p2p.ReadStreamWithBufferWithContext(ctx, stream)
	if err != nil {
		b.logger.Error().Err(err).Msgf("fail to read reply from peer: %s", peer)
		return
	}

	if string(reply) != p2p.StreamMsgDone {
		b.logger.Error().Msgf("unexpected reply from peer: %s", peer)
		return
	}

	b.logger.Debug().Msgf("attestation sent to peer: %s", peer)
}
