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

type AttestationBatcher struct {
	observedTxBatch []*common.AttestTx
	networkFeeBatch []*common.AttestNetworkFee
	solvencyBatch   []*common.AttestSolvency
	errataTxBatch   []*common.AttestErrataTx

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

	peerSemaphores   map[peer.ID]chan struct{}
	peerSemaphoresMu sync.Mutex
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
		peerTimeout = 10 * time.Second // Default peer timeout
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

		peerSemaphores: make(map[peer.ID]chan struct{}),

		lastBatchSent: time.Time{}, // Zero time

		host:    host,
		logger:  logger,
		metrics: batchMetrics,
	}
}

func (b *AttestationBatcher) setActiveValGetter(getter func() map[peer.ID]bool) {
	b.getActiveValidators = getter
}

func (b *AttestationBatcher) Start(ctx context.Context) {
	b.batchTicker = time.NewTicker(b.batchInterval)
	b.forceSendChan = make(chan struct{}, 1) // Buffer of 1 to avoid blocking

	defer func() {
		b.batchTicker.Stop()
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
	// Create batched message
	batch := common.AttestationBatch{
		AttestTxs:         b.observedTxBatch,
		AttestNetworkFees: b.networkFeeBatch,
		AttestSolvencies:  b.solvencyBatch,
		AttestErrataTxs:   b.errataTxBatch,
	}

	txCount, nfCount, solvencyCount, errataCount := len(batch.AttestTxs), len(batch.AttestNetworkFees), len(batch.AttestSolvencies), len(batch.AttestErrataTxs)
	b.mu.Unlock()

	// Send to all peers
	b.broadcastToAllPeers(ctx, batch)

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
			b.peerSemaphoresMu.Lock()
			sem, exists := b.peerSemaphores[peer]
			if !exists {
				sem = make(chan struct{}, b.peerConcurrentSends)
				b.peerSemaphores[peer] = sem
			}
			b.peerSemaphoresMu.Unlock()

			select {
			case sem <- struct{}{}: // Acquire the semaphore
				defer func() {
					<-sem // Release the semaphore
					b.peerSemaphoresMu.Lock()
					if len(sem) == 0 {
						delete(b.peerSemaphores, peer) // Clean up if no more sends
					}
					b.peerSemaphoresMu.Unlock()
				}()
			default:
				b.logger.Debug().Msgf("peer %s is busy, skipping this send", peer)
				return
			}

			// Create a context with timeout for this specific peer
			peerCtx, cancel := context.WithTimeout(ctx, b.peerTimeout)
			defer cancel()

			b.logger.Debug().Msgf("starting attestation send to peer: %s", peer)

			stream, err := b.host.NewStream(peerCtx, peer, batchedAttestationProtocol)
			if err != nil {
				if strings.Contains(err.Error(), "protocol not supported") {
					b.logger.Debug().Msgf("peer %s does not support batched attestation protocol, sending unbatched", peer)
					// send unbatched attestations
					legacyAtts := getMarshaledLegacyAttestations()
					for _, att := range legacyAtts {
						legacyStream, err := b.host.NewStream(peerCtx, peer, att.protocolID)
						if err != nil {
							b.logger.Error().Err(err).Msgf("fail to create stream to peer: %s", peer)
							return
						}
						b.sendPayloadToStream(legacyStream, att.payload)
					}
					return
				}
				b.logger.Error().Err(err).Msgf("fail to create stream to peer: %s", peer)
				return
			}

			b.sendPayloadToStream(stream, payload)
			b.logger.Debug().Msgf("completed attestation send to peer: %s", peer)
		}(p)
	}

	// Function returns immediately without waiting for sends to complete
	b.logger.Debug().Msg("broadcast initiated to all peers")
}

// clearBatches resets all batch arrays to empty
func (b *AttestationBatcher) clearBatches() {
	// Clear all batches
	b.observedTxBatch = make([]*common.AttestTx, 0, b.maxBatchSize)
	b.networkFeeBatch = make([]*common.AttestNetworkFee, 0, b.maxBatchSize)
	b.solvencyBatch = make([]*common.AttestSolvency, 0, b.maxBatchSize)
	b.errataTxBatch = make([]*common.AttestErrataTx, 0, b.maxBatchSize)

	// Log batch clear
	b.logger.Debug().Msg("attestation batches cleared")

	// Update metrics
	b.metrics.BatchClears.Inc()
}

func (b *AttestationBatcher) sendPayloadToStream(stream network.Stream, payload []byte) {
	peer := stream.Conn().RemotePeer()

	defer func() {
		if err := stream.Close(); err != nil {
			b.logger.Error().Err(err).Msgf("fail to close stream to peer: %s", peer)
		}
	}()

	if err := p2p.WriteStreamWithBuffer(payload, stream); err != nil {
		b.logger.Error().Err(err).Msgf("fail to write payload to peer: %s", peer)
		return
	}

	// Wait for acknowledgment
	reply, err := p2p.ReadStreamWithBuffer(stream)
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
