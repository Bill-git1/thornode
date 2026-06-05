package observer

import (
	"context"
	"encoding/hex"
	"time"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

const priceFeedsUpdateDelay = time.Millisecond * 100

// handleObservedTxAttestation processes attestations for observed transactions
func (s *AttestationGossip) handleObservedTxAttestation(ctx context.Context, tx common.AttestTx) {
	obsTx := tx.ObsTx

	k := observedTxKey(obsTx, tx.Inbound, tx.AllowFutureObservation)

	s.mu.Lock()
	state, ok := s.observedTxs[k]
	if !ok {
		state = s.observedTxsPool.NewAttestationState(&attestableObservedTx{
			ObservedTx: &obsTx,
			inbound:    tx.Inbound,
		})
		s.observedTxs[k] = state
	}

	// note, purposefully locking the single state mutex prior to releasing the global mutex to avoid race in between.
	// This order must be maintained everywhere to avoid deadlock.
	state.mu.Lock()
	s.mu.Unlock()

	attestationCountBefore := state.AttestationCount()

	// Add the attestation
	if err := state.AddAttestation(tx.Attestation); err != nil {
		s.logger.Error().Err(err).Msg("fail to add attestation")
		state.mu.Unlock()
		return
	}
	isNewAttestation := state.AttestationCount() > attestationCountBefore

	// Candidate XMR spent-ref imports are authorized after releasing the state
	// lock. The authorization path may query thornode and the eventual import
	// calls the sidecar, neither of which should run while this mutex is held.
	var maybeApplySpentRefs *common.ObservedTx
	isXMROutboundWithRefs := obsTx.Tx.Chain.Equals(common.XMRChain) && !tx.Inbound &&
		len(obsTx.SpentOutputRefs) > 0 && len(obsTx.SpentOutputRefs) <= common.MaxXMRSpentOutputRefs
	if isXMROutboundWithRefs && isNewAttestation && !state.spentRefsApplied {
		// Do not set spentRefsApplied here. A peer attestation is only a
		// candidate until it passes the XMR-specific authority checks below; an
		// unauthorized peer must not suppress a later authorized import.
		cp := obsTx
		maybeApplySpentRefs = &cp
	}

	// Observation quorum is independent of the keysign committee. Even future
	// outbound observations must match thornode's active-validator quorum check.
	attestationCount := state.AttestationCount()
	total := s.activeValidatorCount()

	hasSuperMajority := types.HasSuperMajority(attestationCount, total)

	// If we have a supermajority, send to thornode
	if hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", attestationCount, total)

		s.sendObservedTxAttestationsToThornode(ctx, obsTx, state, k.Inbound, k.AllowFutureObservation, true)
	} else {
		s.logger.Debug().Msgf("observed tx attestation received - %s, id: %s, inbound: %t, final: %t, quorum: %d/%d",
			k.Chain, k.ID, k.Inbound, k.Finalized, attestationCount, total)
	}
	state.mu.Unlock()

	var applySpentRefs *common.ObservedTx
	if maybeApplySpentRefs != nil && s.observerApplySpentOutputRefs != nil &&
		s.shouldApplyXMRSpentRefsFromAttestation(*maybeApplySpentRefs, tx.Attestation) {
		s.mu.Lock()
		liveState, ok := s.observedTxs[k]
		if ok {
			// Re-acquire locks in the normal global-then-state order. The
			// authorization path above can block on thornode, so the originally
			// captured state may have been pruned and returned to the pool.
			liveState.mu.Lock()
		}
		s.mu.Unlock()

		if ok {
			if !liveState.spentRefsApplied {
				// Only mutate the currently indexed state bucket. If the bucket was
				// pruned while authorization was running, skip the import instead
				// of marking a stale or recycled state object.
				liveState.spentRefsApplied = true
				applySpentRefs = maybeApplySpentRefs
			}
			liveState.mu.Unlock()
		}
	}

	// Apply XMR spent output refs after all authorization and state bookkeeping.
	// This seeds the sidecar only; it does not add a local attestation.
	if applySpentRefs != nil && s.observerApplySpentOutputRefs != nil {
		s.applySpentRefsAsync(*applySpentRefs)
	}
}

// sendObservedTxAttestationsToThornode sends attestations to thornode via gRPC
func (s *AttestationGossip) sendObservedTxAttestationsToThornode(
	ctx context.Context,
	tx common.ObservedTx,
	state *AttestationState[*attestableObservedTx],
	inbound, allowFutureObservation, isQuorum bool,
) {
	unsent := state.UnsentAttestations()
	if len(unsent) == 0 {
		s.logger.Debug().Msg("no unsent observed tx attestations")
		return
	}
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumTx(ctx, &common.QuorumTx{
		ObsTx:                  tx,
		Attestations:           unsent,
		Inbound:                inbound,
		AllowFutureObservation: allowFutureObservation,
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum tx")
		return
	}

	s.logger.Info().Msgf("sent quorum tx to thornode - %s, id: %s, inbound: %t, final: %t, attestations: %s",
		tx.Tx.Chain, tx.Tx.ID, inbound, tx.IsFinal(), state.State())

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleNetworkFeeAttestation processes attestations for network fees
func (s *AttestationGossip) handleNetworkFeeAttestation(ctx context.Context, anf common.AttestNetworkFee) {
	// Use the network fee as the map key
	k := *anf.NetworkFee

	s.mu.Lock()
	state, ok := s.networkFees[k]
	if !ok {
		// Create a new attestation state
		state = s.networkFeesPool.NewAttestationState(anf.NetworkFee)
		s.networkFees[k] = state
	}

	// note, purposefully locking the single state mutex prior to releasing the global mutex to avoid race in between.
	// This order must be maintained everywhere to avoid deadlock.
	state.mu.Lock()
	s.mu.Unlock()

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
	if hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendNetworkFeeAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("network fee attestation received - %s, height: %d, quorum: %d/%d",
			k.Chain, k.Height, state.AttestationCount(), activeValCount)
	}
}

// sendNetworkFeeAttestationsToThornode sends network fee attestations to thornode via gRPC
func (s *AttestationGossip) sendNetworkFeeAttestationsToThornode(ctx context.Context, networkFee common.NetworkFee, state *AttestationState[*common.NetworkFee], isQuorum bool) {
	unsent := state.UnsentAttestations()
	if len(unsent) == 0 {
		s.logger.Debug().Msg("no unsent network fee attestations")
		return
	}
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumNetworkFee(ctx, &common.QuorumNetworkFee{
		NetworkFee:   &networkFee,
		Attestations: unsent,
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum network fee")
		return
	}

	s.logger.Info().Msgf("sent quorum network fee to thornode - %s, height: %d, attestations: %s",
		networkFee.Chain, networkFee.Height, state.State())

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleSolvencyAttestation processes attestations for solvency proofs
func (s *AttestationGossip) handleSolvencyAttestation(ctx context.Context, ats common.AttestSolvency) {
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
		state = s.solvenciesPool.NewAttestationState(ats.Solvency)
		s.solvencies[k] = state
	}

	// note, purposefully locking the single state mutex prior to releasing the global mutex to avoid race in between.
	// This order must be maintained everywhere to avoid deadlock.
	state.mu.Lock()
	s.mu.Unlock()

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
	if hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendSolvencyAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("solvency attestation received - %s, height: %d, quorum: %d/%d",
			ats.Solvency.Chain, ats.Solvency.Height, state.AttestationCount(), activeValCount)
	}
}

// sendSolvencyAttestationsToThornode sends solvency attestations to thornode via gRPC
func (s *AttestationGossip) sendSolvencyAttestationsToThornode(ctx context.Context, solvency common.Solvency, state *AttestationState[*common.Solvency], isQuorum bool) {
	unsent := state.UnsentAttestations()
	if len(unsent) == 0 {
		s.logger.Debug().Msg("no unsent solvency attestations")
		return
	}
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumSolvency(ctx, &common.QuorumSolvency{
		Solvency:     &solvency,
		Attestations: unsent,
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum solvency")
		return
	}

	s.logger.Info().Msgf("sent quorum solvency to thornode - %s, height: %d, coins: %s, pubkey: %s, attestations: %s",
		solvency.Chain, solvency.Height, solvency.Coins.String(), solvency.PubKey.String(), state.State())

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handleErrataAttestation processes attestations for errata transactions
func (s *AttestationGossip) handleErrataAttestation(ctx context.Context, aet common.AttestErrataTx) {
	// Use the errata tx as the map key
	k := *aet.ErrataTx

	s.mu.Lock()
	state, ok := s.errataTxs[k]
	if !ok {
		// Create a new attestation state
		state = s.errataTxsPool.NewAttestationState(aet.ErrataTx)
		s.errataTxs[k] = state
	}

	// note, purposefully locking the single state mutex prior to releasing the global mutex to avoid race in between.
	// This order must be maintained everywhere to avoid deadlock.
	state.mu.Lock()
	s.mu.Unlock()

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
	if hasSuperMajority {
		s.logger.Debug().Msgf("has supermajority: %d/%d", state.AttestationCount(), activeValCount)
		s.sendErrataAttestationsToThornode(ctx, *state.Item, state, true)
	} else {
		s.logger.Debug().Msgf("errata attestation received - %s, id: %s, quorum: %d/%d",
			k.Chain, k.Id, state.AttestationCount(), activeValCount)
	}
}

// sendErrataAttestationsToThornode sends errata attestations to thornode via gRPC
func (s *AttestationGossip) sendErrataAttestationsToThornode(ctx context.Context, errata common.ErrataTx, state *AttestationState[*common.ErrataTx], isQuorum bool) {
	unsent := state.UnsentAttestations()
	if len(unsent) == 0 {
		s.logger.Debug().Msg("no unsent errata attestations")
		return
	}
	// Send via gRPC to thornode
	if _, err := s.grpcClient.SendQuorumErrataTx(ctx, &common.QuorumErrataTx{
		ErrataTx:     &errata,
		Attestations: unsent,
	}); err != nil {
		s.logger.Error().Err(err).Msg("fail to send quorum errata")
		return
	}

	s.logger.Info().Msgf("sent quorum errata to thornode - %s - ID: %s - attestations: %s", errata.Chain, errata.Id, state.State())

	// Mark attestations as sent
	state.MarkAttestationsSent(isQuorum)
}

// handlePriceFeedAttestation processes attestations for price feeds
func (s *AttestationGossip) handlePriceFeedAttestation(ctx context.Context, apf common.AttestPriceFeed) {
	// Use the pubkey as key
	k := hex.EncodeToString(apf.Attestation.PubKey)

	// Get the marshaled data for signature verification
	signBz, err := apf.PriceFeed.GetSignablePayload()
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to get signable payload")
		return
	}

	// Verify the signature
	att := apf.Attestation
	err = verifySignature(signBz, att.Signature, att.PubKey)
	if err != nil {
		s.logger.Error().Err(err).Msgf("signature verification failed for %x", att.PubKey)
		return
	}

	// We don't collect multiple attestations for price feeds, rather want
	// to store the most recent price we received for every node
	s.mu.Lock()
	defer s.mu.Unlock()

	state, ok := s.priceFeeds[k]
	if !ok || state.Item.Time < apf.PriceFeed.Time {
		// Set new attestation state
		state = s.priceFeedsPool.NewAttestationState(apf.PriceFeed)
		state.attestations = []attestationSentState{
			{attestation: apf.Attestation, sent: false},
		}
		s.priceFeeds[k] = state

		// we received an updated price feed, start delay to maybe collect
		// more updated values before sending them to thornode
		if s.priceFeedsDelay.IsRunning() {
			return
		}

		s.priceFeedsDelay.Start()
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(priceFeedsUpdateDelay):
				s.sendPriceFeedAttestationsToThornode(ctx)
				s.priceFeedsDelay.Done()
			}
		}()
	}
}

// sendPriceFeedAttestationsToThornode sends price feed attestations
// to thornode via gRPC
//
// To avoid calling the endpoint hundred times per update, all
// (quorum) price feeds are batched into a single request
func (s *AttestationGossip) sendPriceFeedAttestationsToThornode(
	ctx context.Context,
) {
	qpfb := common.QuorumPriceFeedBatch{
		QuorumPriceFeeds: []*common.QuorumPriceFeed{},
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, state := range s.priceFeeds {
		state.mu.Lock()
		defer state.mu.Unlock()

		qpfb.QuorumPriceFeeds = append(qpfb.QuorumPriceFeeds, &common.QuorumPriceFeed{
			PriceFeed:    state.Item,
			Attestations: []*common.Attestation{state.attestations[0].attestation},
		})
	}

	if len(qpfb.QuorumPriceFeeds) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	_, err := s.grpcClient.SendQuorumPriceFeedBatch(ctx, &qpfb)
	if err != nil {
		s.logger.Error().Err(err).Msg("fail to send price feed")
		return
	}

	// delete price feeds
	for k := range s.priceFeeds {
		delete(s.priceFeeds, k)
	}
}
