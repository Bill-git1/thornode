package observer

import (
	"context"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

// handleObservedTxAttestation processes attestations for observed transactions
func (s *AttestationGossip) handleObservedTxAttestation(ctx context.Context, tx common.AttestTx) {
	// Ensure the attester is an active validator
	if err := s.ensureAttesterIsActiveNode(tx.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to ensure attester for observed tx is active node")
		return
	}

	obsTx := tx.ObsTx

	k := txKey{
		Chain:                  obsTx.Tx.Chain,
		ID:                     obsTx.Tx.ID,
		UniqueHash:             obsTx.Tx.Hash(obsTx.BlockHeight),
		AllowFutureObservation: tx.AllowFutureObservation,
		Finalized:              obsTx.IsFinal(),
	}

	s.mu.Lock()
	state, ok := s.observedTxs[k]
	if !ok {
		// Create a new attestation state
		state = NewAttestationState(&obsTx)
		state.SetMetadata(metadataKeyInbound, tx.Inbound)
		state.SetMetadata(metadataKeyAllowFutureObservation, tx.AllowFutureObservation)
		s.observedTxs[k] = state
	}
	s.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	// Add the attestation
	if err := state.AddAttestation(tx.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to add attestation")
		return
	}

	// Determine the number of validators needed for attestation
	var total int
	if k.AllowFutureObservation {
		keysignParty, err := s.getKeysignParty(obsTx.ObservedPubKey)
		if err != nil {
			s.logger.Error().Err(err).Msg("fail to get key sign party")
			return
		}
		total = len(keysignParty)
	} else {
		total = s.activeValidatorCount()
	}

	hasSuperMajority := types.HasSuperMajority(state.AttestationCount(), total)

	// If we have a supermajority, send to thornode
	if state.quorumAttestationsSent.IsZero() && hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), total)
		// trunk-ignore(golangci-lint/govet): shadow
		inbound, ok := state.GetMetadata(metadataKeyInbound)
		if !ok {
			s.logger.Error().Msg("fail to get inbound metadata")
			return
		}
		// trunk-ignore(golangci-lint/govet): shadow
		inboundBool, ok := inbound.(bool)
		if !ok {
			s.logger.Error().Msg("fail to cast inbound metadata to bool")
			return
		}
		// trunk-ignore(golangci-lint/govet): shadow
		allowFutureObservation, ok := state.GetMetadata(metadataKeyAllowFutureObservation)
		if !ok {
			s.logger.Error().Msg("fail to get allow future observation metadata")
			return
		}
		// trunk-ignore(golangci-lint/govet): shadow
		allowFutureObservationBool, ok := allowFutureObservation.(bool)
		if !ok {
			s.logger.Error().Msg("fail to cast allow future observation metadata to bool")
			return
		}
		s.sendObservedTxAttestationsToThornode(ctx, obsTx, state, inboundBool, allowFutureObservationBool, true)
	} else {
		s.logger.Debug().Msgf("observed tx attestation received - %s - ID: %s - Final: %t - Quorum: %d/%d",
			k.Chain, k.ID, k.Finalized, state.AttestationCount(), total)
	}
}

// sendObservedTxAttestationsToThornode sends attestations to thornode via gRPC
func (s *AttestationGossip) sendObservedTxAttestationsToThornode(ctx context.Context, tx common.ObservedTx, state *AttestationState[*common.ObservedTx], inbound, allowFutureObservation, isQuorum bool) {
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumTx(ctx, &common.QuorumTx{
		ObsTx:                  tx,
		Attestations:           state.UnsentAttestations(),
		Inbound:                inbound,
		AllowFutureObservation: allowFutureObservation,
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum tx")
		return
	}

	s.logger.Info().Msgf("sent quorum tx to thornode - %s - ID: %s - Final: %t", tx.Tx.Chain, tx.Tx.ID, tx.IsFinal())

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleNetworkFeeAttestation processes attestations for network fees
func (s *AttestationGossip) handleNetworkFeeAttestation(ctx context.Context, anf common.AttestNetworkFee) {
	// Ensure the attester is an active validator
	if err := s.ensureAttesterIsActiveNode(anf.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to ensure attester for network fee is active node")
		return
	}

	// Use the network fee as the map key
	k := *anf.NetworkFee

	s.mu.Lock()
	state, ok := s.networkFees[k]
	if !ok {
		// Create a new attestation state
		state = NewAttestationState(anf.NetworkFee)
		s.networkFees[k] = state
	}
	s.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	// Add the attestation
	if err := state.AddAttestation(anf.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to add attestation")
		return
	}

	// Get the active validator count
	activeValCount := s.activeValidatorCount()
	hasSuperMajority := types.HasSuperMajority(state.AttestationCount(), activeValCount)

	// If we have a supermajority, send to thornode
	if state.quorumAttestationsSent.IsZero() && hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendNetworkFeeAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("network fee attestation received - %s - Height: %d - Quorum: %d/%d",
			k.Chain, k.Height, state.AttestationCount(), activeValCount)
	}
}

// sendNetworkFeeAttestationsToThornode sends network fee attestations to thornode via gRPC
func (s *AttestationGossip) sendNetworkFeeAttestationsToThornode(ctx context.Context, networkFee common.NetworkFee, state *AttestationState[*common.NetworkFee], isQuorum bool) {
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumNetworkFee(ctx, &common.QuorumNetworkFee{
		NetworkFee:   &networkFee,
		Attestations: state.UnsentAttestations(),
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum network fee")
		return
	}

	s.logger.Info().Msgf("sent quorum network fee to thornode - %s - height: %d", networkFee.Chain, networkFee.Height)

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleSolvencyAttestation processes attestations for solvency proofs
func (s *AttestationGossip) handleSolvencyAttestation(ctx context.Context, ats common.AttestSolvency) {
	// Ensure the attester is an active validator
	if err := s.ensureAttesterIsActiveNode(ats.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to ensure attester for solvency is active node")
		return
	}

	// Calculate the hash for the solvency to use as key
	k, err := ats.Solvency.Hash()
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to hash solvency")
		return
	}

	s.mu.Lock()
	state, ok := s.solvencies[k]
	if !ok {
		// Create a new attestation state
		state = NewAttestationState(ats.Solvency)
		s.solvencies[k] = state
	}
	s.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	// Add the attestation
	if err := state.AddAttestation(ats.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to add attestation")
		return
	}

	// Get the active validator count
	activeValCount := s.activeValidatorCount()
	hasSuperMajority := types.HasSuperMajority(state.AttestationCount(), activeValCount)

	// If we have a supermajority, send to thornode
	if state.quorumAttestationsSent.IsZero() && hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendSolvencyAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("solvency attestation received - %s - Height: %d - Quorum: %d/%d",
			ats.Solvency.Chain, ats.Solvency.Height, state.AttestationCount(), activeValCount)
	}
}

// sendSolvencyAttestationsToThornode sends solvency attestations to thornode via gRPC
func (s *AttestationGossip) sendSolvencyAttestationsToThornode(ctx context.Context, solvency common.Solvency, state *AttestationState[*common.Solvency], isQuorum bool) {
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumSolvency(ctx, &common.QuorumSolvency{
		Solvency:     &solvency,
		Attestations: state.UnsentAttestations(),
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum solvency")
		return
	}

	s.logger.Info().Msgf("sent quorum solvency to thornode - %s - height: %d", solvency.Chain, solvency.Height)

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleErrataAttestation processes attestations for errata transactions
func (s *AttestationGossip) handleErrataAttestation(ctx context.Context, aet common.AttestErrataTx) {
	// Ensure the attester is an active validator
	if err := s.ensureAttesterIsActiveNode(aet.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to ensure attester for errata tx is active node")
		return
	}

	// Use the errata tx as the map key
	k := *aet.ErrataTx

	s.mu.Lock()
	state, ok := s.errataTxs[k]
	if !ok {
		// Create a new attestation state
		state = NewAttestationState(aet.ErrataTx)
		s.errataTxs[k] = state
	}
	s.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	// Add the attestation
	if err := state.AddAttestation(aet.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to add attestation")
		return
	}

	// Get the active validator count
	activeValCount := s.activeValidatorCount()
	hasSuperMajority := types.HasSuperMajority(state.AttestationCount(), activeValCount)

	// If we have a supermajority, send to thornode
	if state.quorumAttestationsSent.IsZero() && hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendErrataAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("errata attestation received - %s - ID: %s - Quorum: %d/%d",
			k.Chain, k.Id, state.AttestationCount(), activeValCount)
	}
}

// sendErrataAttestationsToThornode sends errata attestations to thornode via gRPC
func (s *AttestationGossip) sendErrataAttestationsToThornode(ctx context.Context, errata common.ErrataTx, state *AttestationState[*common.ErrataTx], isQuorum bool) {
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumErrataTx(ctx, &common.QuorumErrataTx{
		ErrataTx:     &errata,
		Attestations: state.UnsentAttestations(),
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum errata")
		return
	}

	s.logger.Info().Msgf("sent quorum errata to thornode - %s - ID: %s", errata.Chain, errata.Id)

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}
