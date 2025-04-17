package observer

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/libp2p/go-libp2p-core/network"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-core/protocol"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/common"
)

// AttestObservedTx creates and broadcasts an attestation for an observed transaction
func (s *AttestationGossip) AttestObservedTx(ctx context.Context, obsTx *common.ObservedTx, inbound bool) error {
	pk, err := s.keys.GetPrivateKey()
	if err != nil {
		return fmt.Errorf("fail to get private key: %w", err)
	}

	pubBz := pk.PubKey().Bytes()

	if err := s.isActiveValidator(pubBz); err != nil {
		return fmt.Errorf("skipping attest observed tx: %w", err)
	}

	signBz, err := obsTx.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to marshal tx sign payload: %w", err)
	}

	signature, err := pk.Sign(signBz)
	if err != nil {
		return fmt.Errorf("fail to sign tx sign payload: %w", err)
	}

	msg := common.AttestTx{
		ObsTx:   *obsTx,
		Inbound: inbound,
		Attestation: &common.Attestation{
			PubKey:    pubBz,
			Signature: signature,
		},
	}

	// Handle the attestation locally first
	s.logger.Debug().Msg("handling attestation locally")
	s.handleObservedTxAttestation(ctx, msg)

	// Then broadcast to peers
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("fail to marshal tx attestation: %w", err)
	}

	// Send to all connected peers
	peers := s.host.Peerstore().Peers()
	var wg sync.WaitGroup
	for _, peer := range peers {
		if peer == s.host.ID() {
			// Skip ourselves
			continue
		}
		wg.Add(1)
		go s.sendPayloadToPeer(ctx, peer, observeTxProtocol, payload, &wg)
	}
	wg.Wait()

	return nil
}

// AttestNetworkFee creates and broadcasts an attestation for a network fee
func (s *AttestationGossip) AttestNetworkFee(ctx context.Context, networkFee common.NetworkFee) error {
	pk, err := s.keys.GetPrivateKey()
	if err != nil {
		return fmt.Errorf("fail to get private key: %w", err)
	}

	pubBz := pk.PubKey().Bytes()

	if err := s.isActiveValidator(pubBz); err != nil {
		return fmt.Errorf("skipping attest network fee: %w", err)
	}

	signBz, err := networkFee.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to marshal network fee sign payload: %w", err)
	}

	signature, err := pk.Sign(signBz)
	if err != nil {
		return fmt.Errorf("fail to sign network fee sign payload: %w", err)
	}

	msg := common.AttestNetworkFee{
		NetworkFee: &networkFee,
		Attestation: &common.Attestation{
			PubKey:    pubBz,
			Signature: signature,
		},
	}

	// Handle the attestation locally first
	s.logger.Debug().Msg("handling attestation locally")
	s.handleNetworkFeeAttestation(ctx, msg)

	// Then broadcast to peers
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("fail to marshal network fee attestation: %w", err)
	}

	// Send to all connected peers
	peers := s.host.Peerstore().Peers()
	var wg sync.WaitGroup
	for _, peer := range peers {
		if peer == s.host.ID() {
			// Skip ourselves
			continue
		}
		wg.Add(1)
		go s.sendPayloadToPeer(ctx, peer, networkFeeProtocol, payload, &wg)
	}
	wg.Wait()

	return nil
}

// AttestSolvency creates and broadcasts an attestation for a solvency proof
func (s *AttestationGossip) AttestSolvency(ctx context.Context, solvency common.Solvency) error {
	pk, err := s.keys.GetPrivateKey()
	if err != nil {
		return fmt.Errorf("fail to get private key: %w", err)
	}

	pubBz := pk.PubKey().Bytes()

	if err := s.isActiveValidator(pubBz); err != nil {
		return fmt.Errorf("skipping attest solvency: %w", err)
	}

	signBz, err := solvency.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to marshal solvency sign payload: %w", err)
	}

	signature, err := pk.Sign(signBz)
	if err != nil {
		return fmt.Errorf("fail to sign solvency sign payload: %w", err)
	}

	msg := common.AttestSolvency{
		Solvency: &solvency,
		Attestation: &common.Attestation{
			PubKey:    pubBz,
			Signature: signature,
		},
	}

	// Handle the attestation locally first
	s.logger.Debug().Msg("handling attestation locally")
	s.handleSolvencyAttestation(ctx, msg)

	// Then broadcast to peers
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("fail to marshal solvency attestation: %w", err)
	}

	// Send to all connected peers
	peers := s.host.Peerstore().Peers()
	var wg sync.WaitGroup
	for _, peer := range peers {
		if peer == s.host.ID() {
			// Skip ourselves
			continue
		}
		wg.Add(1)
		go s.sendPayloadToPeer(ctx, peer, solvencyProtocol, payload, &wg)
	}
	wg.Wait()

	return nil
}

// AttestErrata creates and broadcasts an attestation for an errata transaction
func (s *AttestationGossip) AttestErrata(ctx context.Context, errata common.ErrataTx) error {
	// First remove any observed transactions for this chain/tx ID
	s.mu.Lock()
	for k := range s.observedTxs {
		if k.Chain.Equals(errata.Chain) && k.ID.Equals(errata.Id) {
			// Remove this tx from the list as we've observed an error
			delete(s.observedTxs, k)
		}
	}
	s.mu.Unlock()

	pk, err := s.keys.GetPrivateKey()
	if err != nil {
		return fmt.Errorf("fail to get private key: %w", err)
	}

	pubBz := pk.PubKey().Bytes()

	if err := s.isActiveValidator(pubBz); err != nil {
		return fmt.Errorf("skipping attest errata tx: %w", err)
	}

	signBz, err := errata.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to marshal errata sign payload: %w", err)
	}

	signature, err := pk.Sign(signBz)
	if err != nil {
		return fmt.Errorf("fail to sign errata sign payload: %w", err)
	}

	msg := common.AttestErrataTx{
		ErrataTx: &errata,
		Attestation: &common.Attestation{
			PubKey:    pubBz,
			Signature: signature,
		},
	}

	// Handle the attestation locally first
	s.logger.Debug().Msg("handling attestation locally")
	s.handleErrataAttestation(ctx, msg)

	// Then broadcast to peers
	payload, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("fail to marshal errata attestation: %w", err)
	}

	// Send to all connected peers
	peers := s.host.Peerstore().Peers()
	var wg sync.WaitGroup
	for _, peer := range peers {
		if peer == s.host.ID() {
			// Skip ourselves
			continue
		}
		wg.Add(1)
		go s.sendPayloadToPeer(ctx, peer, errataTxProtocol, payload, &wg)
	}
	wg.Wait()

	return nil
}

// Send an attestation to a peer
func (s *AttestationGossip) sendPayloadToPeer(ctx context.Context, peer peer.ID, protocol protocol.ID, payload []byte, wg *sync.WaitGroup) {
	defer wg.Done()

	stream, err := s.host.NewStream(ctx, peer, protocol)
	if err != nil {
		s.logger.Error().Err(err).Msgf("fail to create stream to peer: %s", peer)
		return
	}

	s.sendPayloadToStream(stream, payload)
}

func (s *AttestationGossip) sendPayloadToStream(stream network.Stream, payload []byte) {
	peer := stream.Conn().RemotePeer()

	defer func() {
		if err := stream.Close(); err != nil {
			s.logger.Error().Err(err).Msgf("fail to close stream to peer: %s", peer)
		}
	}()

	if err := p2p.WriteStreamWithBuffer(payload, stream); err != nil {
		s.logger.Error().Err(err).Msgf("fail to write payload to peer: %s", peer)
		return
	}

	// Wait for acknowledgment
	reply, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		s.logger.Error().Err(err).Msgf("fail to read reply from peer: %s", peer)
		return
	}

	if string(reply) != p2p.StreamMsgDone {
		s.logger.Error().Msgf("unexpected reply from peer: %s", peer)
		return
	}

	s.logger.Debug().Msgf("attestation sent to peer: %s", peer)
}

// handleStreamAttestationState handles incoming observed transaction streams
func (s *AttestationGossip) handleStreamAttestationState(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	logger := s.logger.With().Str("remote peer", remotePeer.String()).Logger()
	logger.Debug().Msg("reading attestation gossip message")

	// Read and process the message
	data, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		if err != io.EOF {
			logger.Error().Err(err).Msg("fail to read payload from stream")
		}
		return
	}

	// Check message type and handle accordingly
	if len(data) == 0 {
		logger.Error().Msg("empty payload")
		return
	}

	// Handle based on prefix
	switch {
	case data[0] == prefixSendState[0]:
		// Send state request
		if len(data) != 1 {
			logger.Error().Msg("unexpected payload length for send state request")
			return
		}
		logger.Debug().Msg("handling send state request")
		s.sendAttestationState(stream)

	case data[0] == prefixBatchBegin[0]:
		// Batched state transmission
		logger.Debug().Msg("handling batched attestation state")
		err := s.receiveBatchedAttestationState(stream, data[1:])
		if err != nil {
			logger.Error().Err(err).Msg("failed to receive batched attestation state")
		}

	default:
		logger.Error().Msgf("unknown message type: %d", data[0])
		err := p2p.WriteStreamWithBuffer([]byte("error: unknown message type"), stream)
		if err != nil {
			logger.Error().Err(err).Msgf("fail to write error reply to peer: %s", remotePeer)
		}
	}
}

// handleStreamObservedTx handles incoming observed transaction streams
func (s *AttestationGossip) handleStreamObservedTx(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	logger := s.logger.With().Str("remote peer", remotePeer.String()).Logger()
	logger.Debug().Msg("reading attestation gossip message")

	// Read and process the message
	data, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		if err != io.EOF {
			logger.Error().Err(err).Msg("fail to read payload from stream")
		}
		return
	}

	// Check message type and handle accordingly
	if len(data) == 0 {
		logger.Error().Msg("empty payload")
		return
	}

	// Send acknowledgment
	if err := p2p.WriteStreamWithBuffer([]byte(p2p.StreamMsgDone), stream); err != nil {
		logger.Error().Err(err).Msgf("fail to write reply to peer: %s", remotePeer)
		return
	}

	// Handle attestation
	var msg common.AttestTx
	if err := msg.Unmarshal(data); err != nil {
		logger.Error().Err(err).Msg("fail to unmarshal attestation")
		return
	}
	logger.Debug().Msg("handling incoming attestation from peer")
	s.handleObservedTxAttestation(context.Background(), msg)
}

// handleStreamNetworkFee handles incoming network fee streams
func (s *AttestationGossip) handleStreamNetworkFee(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	logger := s.logger.With().Str("remote peer", remotePeer.String()).Logger()
	logger.Debug().Msg("reading network fee attestation message")

	payload, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		if err != io.EOF {
			logger.Error().Err(err).Msg("fail to read payload from stream")
		}
		return
	}

	// Send acknowledgment
	err = p2p.WriteStreamWithBuffer([]byte(p2p.StreamMsgDone), stream)
	if err != nil {
		logger.Error().Err(err).Msgf("fail to write reply to peer: %s", remotePeer)
		return
	}

	if len(payload) == 0 {
		logger.Error().Msg("empty payload")
		return
	}

	var msg common.AttestNetworkFee
	if err := msg.Unmarshal(payload); err != nil {
		logger.Error().Err(err).Msg("fail to unmarshal network fee attestation")
		return
	}

	logger.Debug().Msg("handling incoming network fee attestation from peer")
	s.handleNetworkFeeAttestation(context.Background(), msg)
}

// handleStreamSolvency handles incoming solvency streams
func (s *AttestationGossip) handleStreamSolvency(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	logger := s.logger.With().Str("remote peer", remotePeer.String()).Logger()
	logger.Debug().Msg("reading solvency attestation message")

	payload, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		if err != io.EOF {
			logger.Error().Err(err).Msg("fail to read payload from stream")
		}
		return
	}

	// Send acknowledgment
	err = p2p.WriteStreamWithBuffer([]byte(p2p.StreamMsgDone), stream)
	if err != nil {
		logger.Error().Err(err).Msgf("fail to write reply to peer: %s", remotePeer)
		return
	}

	if len(payload) == 0 {
		logger.Error().Msg("empty payload")
		return
	}

	var msg common.AttestSolvency
	if err := msg.Unmarshal(payload); err != nil {
		logger.Error().Err(err).Msg("fail to unmarshal solvency attestation")
		return
	}

	logger.Debug().Msg("handling incoming solvency attestation from peer")
	s.handleSolvencyAttestation(context.Background(), msg)
}

// handleStreamErrataTx handles incoming errata transaction streams
func (s *AttestationGossip) handleStreamErrataTx(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	logger := s.logger.With().Str("remote peer", remotePeer.String()).Logger()
	logger.Debug().Msg("reading errata tx attestation message")

	payload, err := p2p.ReadStreamWithBuffer(stream)
	if err != nil {
		if err != io.EOF {
			logger.Error().Err(err).Msg("fail to read payload from stream")
		}
		return
	}

	// Send acknowledgment
	err = p2p.WriteStreamWithBuffer([]byte(p2p.StreamMsgDone), stream)
	if err != nil {
		logger.Error().Err(err).Msgf("fail to write reply to peer: %s", remotePeer)
		return
	}

	if len(payload) == 0 {
		logger.Error().Msg("empty payload")
		return
	}

	var msg common.AttestErrataTx
	if err := msg.Unmarshal(payload); err != nil {
		logger.Error().Err(err).Msg("fail to unmarshal errata attestation")
		return
	}

	logger.Debug().Msg("handling incoming errata attestation from peer")
	s.handleErrataAttestation(context.Background(), msg)
}
