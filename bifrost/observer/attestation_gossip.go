package observer

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/protocol"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
)

const (
	// validators can get credit for observing a tx for up to this amount of time after it is committed, after which it count against a slash penalty.
	defaultLateObserveTimeout = 2 * time.Minute

	// Prune observed txs after this amount of time, even if they are not yet committed.
	// Gives some time for longer chain halts.
	// If chain halts for longer than this, validators will need to restart their bifrosts to re-share their attestations.
	defaultNonQuorumTimeout = 10 * time.Hour

	// minTimeBetweenAttestations is the minimum time between sending batches of attestations for a quorum tx to thornode.
	defaultMinTimeBetweenAttestations = 30 * time.Second

	// how often to prune old observed txs and check if late attestations should be sent.
	// should be less than lateObserveTimeout and minTimeBetweenAttestations by at least a factor of 2.
	defaultObserveReconcileInterval = 15 * time.Second

	// defaultAskPeers is the number of random peers to ask for their attestation state on startup.
	defaultAskPeers = 3

	// defaultAskPeersDelay is the delay before asking peers for their attestation state on startup.
	defaultAskPeersDelay = 5 * time.Second

	cachedKeysignPartyTTL = 1 * time.Minute

	metadataKeyInbound                = "inbound"
	metadataKeyAllowFutureObservation = "allow_future_observation"

	streamAckBegin  = "ack_begin"
	streamAckHeader = "ack_header"
	streamAckData   = "ack_data"
)

var (
	observeTxProtocol        protocol.ID = "/p2p/observed-tx"
	networkFeeProtocol       protocol.ID = "/p2p/network-fee"
	solvencyProtocol         protocol.ID = "/p2p/solvency"
	errataTxProtocol         protocol.ID = "/p2p/errata-tx"
	attestationStateProtocol protocol.ID = "/p2p/attestation-state"

	// AttestationState protocol prefixes
	prefixSendState = []byte{0x01} // request

	prefixBatchBegin  = []byte{0x02} // start of a batch
	prefixBatchHeader = []byte{0x03} // header of a batch
	prefixBatchData   = []byte{0x04} // data of a batch
	prefixBatchEnd    = []byte{0x05} // end of a batch

	// Maximum number of QuorumTxs to send in a single batch when sending attestation state.
	maxQuorumTxsPerBatch = 100
)

// txKey contains the properties that are required to uniquely identify an observed tx
type txKey struct {
	Chain                  common.Chain
	ID                     common.TxID
	UniqueHash             string
	AllowFutureObservation bool
	Finalized              bool
}

type KeysInterface interface {
	GetPrivateKey() (cryptotypes.PrivKey, error)
}

type EventClientInterface interface {
	Start()
	Stop()
	RegisterHandler(eventType string, handler func(*ebifrost.EventNotification))
}

// AttestationGossip handles observed tx attestations to/from other nodes
type AttestationGossip struct {
	logger      zerolog.Logger
	host        host.Host
	keys        KeysInterface
	grpcClient  ebifrost.LocalhostBifrostClient
	eventClient EventClientInterface
	bridge      thorclient.ThorchainBridge

	pubKey []byte // our public key, cached for performance

	config config.BifrostAttestationGossipConfig

	// Generic maps for different attestation types
	observedTxs map[txKey]*AttestationState[*common.ObservedTx]
	networkFees map[common.NetworkFee]*AttestationState[*common.NetworkFee]
	solvencies  map[common.TxID]*AttestationState[*common.Solvency]
	errataTxs   map[common.ErrataTx]*AttestationState[*common.ErrataTx]
	mu          sync.Mutex

	activeVals common.PubKeys
	avMu       sync.Mutex

	observerHandleObservedTxCommitted func(tx common.ObservedTx)

	cachedKeySignParties map[common.PubKey]cachedKeySignParty
	cachedKeySignMu      sync.Mutex
}

type cachedKeySignParty struct {
	keySignParty common.PubKeys
	lastUpdated  time.Time
}

// NewAttestationGossip create a new instance of AttestationGossip
func NewAttestationGossip(
	host host.Host,
	keys *thorclient.Keys,
	thornodeBifrostGRPCAddress string,
	bridge thorclient.ThorchainBridge,
	config config.BifrostAttestationGossipConfig,
) (*AttestationGossip, error) {
	cc, err := grpc.NewClient(thornodeBifrostGRPCAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	normalizeConfig(&config)

	grpcClient := ebifrost.NewLocalhostBifrostClient(cc)
	eventClient := NewEventClient(grpcClient)

	pk, err := keys.GetPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("fail to get private key: %w", err)
	}

	s := &AttestationGossip{
		logger:      log.With().Str("module", "attestation_gossip").Logger(),
		host:        host,
		keys:        keys,
		pubKey:      pk.PubKey().Bytes(),
		grpcClient:  grpcClient,
		config:      config,
		bridge:      bridge,
		eventClient: eventClient,

		// Initialize generic maps
		observedTxs:          make(map[txKey]*AttestationState[*common.ObservedTx]),
		networkFees:          make(map[common.NetworkFee]*AttestationState[*common.NetworkFee]),
		solvencies:           make(map[common.TxID]*AttestationState[*common.Solvency]),
		errataTxs:            make(map[common.ErrataTx]*AttestationState[*common.ErrataTx]),
		cachedKeySignParties: make(map[common.PubKey]cachedKeySignParty),
	}
	// Register event handlers
	eventClient.RegisterHandler(ebifrost.EventQuorumTxCommited, s.handleQuorumTxCommited)

	// Register stream handlers
	host.SetStreamHandler(observeTxProtocol, s.handleStreamObservedTx)
	host.SetStreamHandler(attestationStateProtocol, s.handleStreamAttestationState)
	host.SetStreamHandler(networkFeeProtocol, s.handleStreamNetworkFee)
	host.SetStreamHandler(solvencyProtocol, s.handleStreamSolvency)
	host.SetStreamHandler(errataTxProtocol, s.handleStreamErrataTx)

	return s, nil
}

// normalizeConfig ensures that the config has values for all fields.
func normalizeConfig(config *config.BifrostAttestationGossipConfig) {
	if config.ObserveReconcileInterval == 0 {
		config.ObserveReconcileInterval = defaultObserveReconcileInterval
	}
	if config.LateObserveTimeout == 0 {
		config.LateObserveTimeout = defaultLateObserveTimeout
	}
	if config.NonQuorumTimeout == 0 {
		config.NonQuorumTimeout = defaultNonQuorumTimeout
	}
	if config.MinTimeBetweenAttestations == 0 {
		config.MinTimeBetweenAttestations = defaultMinTimeBetweenAttestations
	}
	if config.AskPeers == 0 {
		config.AskPeers = defaultAskPeers
	}
	if config.AskPeersDelay == 0 {
		config.AskPeersDelay = defaultAskPeersDelay
	}
}

// Set the active validators list
func (s *AttestationGossip) setActiveValidators(activeVals common.PubKeys) {
	s.avMu.Lock()
	defer s.avMu.Unlock()
	s.activeVals = activeVals
}

// Get the number of active validators
func (s *AttestationGossip) activeValidatorCount() int {
	s.avMu.Lock()
	defer s.avMu.Unlock()
	return len(s.activeVals)
}

// Check if a public key belongs to an active validator
func (s *AttestationGossip) isActiveValidator(secp256k1PubKey []byte) error {
	bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, &secp256k1.PubKey{Key: secp256k1PubKey})
	if err != nil {
		return fmt.Errorf("fail to convert pubkey to bech32: %w", err)
	}

	s.avMu.Lock()
	defer s.avMu.Unlock()
	for _, val := range s.activeVals {
		if val.Equals(common.PubKey(bech32Pub)) {
			return nil
		}
	}
	return fmt.Errorf("not an active validator: %s", bech32Pub)
}

// Get the keysign party for a specific public key
func (s *AttestationGossip) getKeysignParty(pubKey common.PubKey) (common.PubKeys, error) {
	s.cachedKeySignMu.Lock()
	defer s.cachedKeySignMu.Unlock()

	if cached, ok := s.cachedKeySignParties[pubKey]; ok {
		return cached.keySignParty, nil
	}

	keySignParty, err := s.bridge.GetKeysignParty(pubKey)
	if err != nil {
		return nil, fmt.Errorf("fail to get key sign party: %w", err)
	}

	s.cachedKeySignParties[pubKey] = cachedKeySignParty{
		keySignParty: keySignParty,
		lastUpdated:  time.Now(),
	}

	return keySignParty, nil
}

func (s *AttestationGossip) SetObserverHandleObservedTxCommitted(o *Observer) {
	s.observerHandleObservedTxCommitted = o.handleObservedTxCommitted
}

// Handle a committed quorum transaction event
func (s *AttestationGossip) handleQuorumTxCommited(en *ebifrost.EventNotification) {
	s.logger.Debug().Msg("handling quorum tx committed event")

	if s.observerHandleObservedTxCommitted == nil {
		// nothing to do
		return
	}

	var tx common.QuorumTx
	if err := tx.Unmarshal(en.Payload); err != nil {
		s.logger.Error().Err(err).Msg("fail to unmarshal quorum tx")
		return
	}

	// if our attestation is in the quorum tx, we can remove it from our observer deck.
	for _, att := range tx.Attestations {
		if bytes.Equal(att.PubKey, s.pubKey) {
			// we have attested to this tx, and it has been committed to the chain.
			s.logger.Debug().Msg("our attestation is in the quorum tx, passing to observer to remove from ondeck")
			s.observerHandleObservedTxCommitted(tx.ObsTx)
			return
		}
	}
}

// / Start the attestation gossip service
func (s *AttestationGossip) Start(ctx context.Context) {
	ticker := time.NewTicker(s.config.ObserveReconcileInterval)
	defer ticker.Stop()

	startupDelay := s.config.AskPeersDelay
	delayTimer := time.NewTimer(startupDelay)
	defer delayTimer.Stop()

	for {
		select {
		case <-ticker.C:
			// prune old attestations and check for late ones to send
			s.mu.Lock()

			// Prune observed transactions
			for k, state := range s.observedTxs {
				state.mu.Lock()
				if state.ExpiredAfterQuorum(s.config.LateObserveTimeout, s.config.NonQuorumTimeout) {
					delete(s.observedTxs, k)
				} else if state.ShouldSendLate(s.config.MinTimeBetweenAttestations) {
					s.logger.Info().Msg("sending late observed tx attestations")
					isInbound, ok := state.GetMetadata(metadataKeyInbound)
					if !ok {
						s.logger.Error().Msg("fail to get inbound metadata")
						state.mu.Unlock()
						continue
					}
					inboundBool, ok := isInbound.(bool)
					if !ok {
						s.logger.Error().Msg("fail to cast inbound metadata to bool")
						state.mu.Unlock()
						continue
					}
					allowFutureObs, ok := state.GetMetadata(metadataKeyAllowFutureObservation)
					if !ok {
						s.logger.Error().Msg("fail to get allow future observation metadata")
						state.mu.Unlock()
						continue
					}
					allowFutureObsBool, ok := allowFutureObs.(bool)
					if !ok {
						s.logger.Error().Msg("fail to cast allow future observation metadata to bool")
						state.mu.Unlock()
						continue
					}

					obsTx := state.Item
					s.sendObservedTxAttestationsToThornode(ctx, *obsTx, state, inboundBool, allowFutureObsBool, false)
				}
				state.mu.Unlock()
			}

			// Prune network fees
			for k, state := range s.networkFees {
				state.mu.Lock()
				if state.ExpiredAfterQuorum(s.config.LateObserveTimeout, s.config.NonQuorumTimeout) {
					delete(s.networkFees, k)
				} else if state.ShouldSendLate(s.config.MinTimeBetweenAttestations) {
					s.logger.Info().Msg("sending late network fee attestations")
					s.sendNetworkFeeAttestationsToThornode(ctx, *state.Item, state, false)
				}
				state.mu.Unlock()
			}

			// Prune solvencies
			for k, state := range s.solvencies {
				state.mu.Lock()
				if state.ExpiredAfterQuorum(s.config.LateObserveTimeout, s.config.NonQuorumTimeout) {
					delete(s.solvencies, k)
				} else if state.ShouldSendLate(s.config.MinTimeBetweenAttestations) {
					s.logger.Info().Msg("sending late solvency attestations")
					s.sendSolvencyAttestationsToThornode(ctx, *state.Item, state, false)
				}
				state.mu.Unlock()
			}

			// Prune errata transactions
			for k, state := range s.errataTxs {
				state.mu.Lock()
				if state.ExpiredAfterQuorum(s.config.LateObserveTimeout, s.config.NonQuorumTimeout) {
					delete(s.errataTxs, k)
				} else if state.ShouldSendLate(s.config.MinTimeBetweenAttestations) {
					s.logger.Info().Msg("sending late errata attestations")
					s.sendErrataAttestationsToThornode(ctx, *state.Item, state, false)
				}
				state.mu.Unlock()
			}
			s.mu.Unlock()

			// Prune cached keysign parties
			s.cachedKeySignMu.Lock()
			for pk, cached := range s.cachedKeySignParties {
				if time.Since(cached.lastUpdated) > cachedKeysignPartyTTL {
					delete(s.cachedKeySignParties, pk)
				}
			}
			s.cachedKeySignMu.Unlock()

		case <-delayTimer.C:
			s.eventClient.Start()
			s.logger.Debug().Msg("asking for attestation state")
			s.askForAttestationState(ctx)

		case <-ctx.Done():
			s.eventClient.Stop()
			return
		}
	}
}

func (s *AttestationGossip) ensureAttesterIsActiveNode(att *common.Attestation) error {
	if att == nil {
		return fmt.Errorf("attestation is nil")
	}

	if att.PubKey == nil {
		return fmt.Errorf("attestation pubkey is nil")
	}

	if err := s.isActiveValidator(att.PubKey); err != nil {
		return fmt.Errorf("ignoring attestation: %w", err)
	}

	return nil
}
