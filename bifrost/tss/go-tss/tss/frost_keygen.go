package tss

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	tcommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

const (
	frostKeygenMsgIDDomain = "thorchain-frost-keygen-msgid-v1"
	frostDKGContextDomain  = "thorchain-frost-dkg-context-v1"

	frostDKGRound1 = "frost_dkg_round1"
	frostDKGRound2 = "frost_dkg_round2"
	frostDKGRound3 = "frost_dkg_round3"
	frostDKGResult = "frost_dkg_result"
)

type FrostKeygenCoordinator struct {
	logger           zerolog.Logger
	p2pCommunication *p2p.Communication
	sidecar          FrostSidecarKeygenClient
	localPubKey      string
	msgID            string
	msgChan          chan *p2p.Message
	timeout          time.Duration
}

// minFrostKeygenTimeout is the minimum per-round wait the FROST DKG
// coordinator will use, regardless of what the caller passes in. FROST DKG is
// a 3-round p2p protocol with inter-round libp2p message exchange and no
// retransmits, so the timeout needs to accommodate a full round trip across
// all active peers. 60s is a conservative floor; production configs should
// set this explicitly to something larger.
const minFrostKeygenTimeout = 60 * time.Second

func NewFrostKeygenCoordinator(
	comm *p2p.Communication,
	sidecar FrostSidecarKeygenClient,
	localPubKey string,
	msgID string,
	msgChan chan *p2p.Message,
	timeout time.Duration,
) *FrostKeygenCoordinator {
	if timeout < minFrostKeygenTimeout {
		timeout = minFrostKeygenTimeout
	}
	return &FrostKeygenCoordinator{
		logger:           log.With().Str("module", "frost_keygen").Str("msg_id", msgID).Logger(),
		p2pCommunication: comm,
		sidecar:          sidecar,
		localPubKey:      strings.TrimSpace(localPubKey),
		msgID:            strings.TrimSpace(msgID),
		msgChan:          msgChan,
		timeout:          timeout,
	}
}

func FrostKeygenMsgID(req FrostKeygenRequest) (string, error) {
	vault := strings.TrimSpace(req.VaultPubKey)
	if vault == "" {
		return "", fmt.Errorf("vault pubkey is required")
	}
	members, _, _, err := buildParticipantIndex(req.SignerPubKeys)
	if err != nil {
		return "", err
	}
	membersHash, err := tcommon.MsgToHashString([]byte(strings.Join(members, ",")))
	if err != nil {
		return "", fmt.Errorf("fail to hash signer membership: %w", err)
	}

	canonical := strings.Join([]string{
		"domain=" + frostKeygenMsgIDDomain,
		"vault_pub_key=" + vault,
		"block_height=" + strconv.FormatInt(req.BlockHeight, 10),
		"members_hash=" + membersHash,
		"expected_addr=" + strings.TrimSpace(req.ExpectedAddr),
	}, "\n")
	return tcommon.MsgToHashString([]byte(canonical))
}

func FrostDKGContext(msgID string) (string, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return "", fmt.Errorf("msgID is required")
	}
	return tcommon.MsgToHashString([]byte(frostDKGContextDomain + "\n" + msgID))
}

func (t *TssServer) FrostKeygen(req FrostKeygenRequest, sidecar FrostSidecarKeygenClient) (FrostKeygenResponse, error) {
	if sidecar == nil {
		return FrostKeygenResponse{}, fmt.Errorf("frost sidecar keygen client is nil")
	}
	if len(req.SignerPubKeys) == 0 {
		return FrostKeygenResponse{}, fmt.Errorf("signer pub keys are empty")
	}

	msgID, err := FrostKeygenMsgID(req)
	if err != nil {
		return FrostKeygenResponse{}, fmt.Errorf("fail to compute frost keygen msgID: %w", err)
	}
	msgChan := make(chan *p2p.Message, 1024)
	sigChan := make(chan string, 2)

	t.p2pCommunication.SetSubscribe(messages.TSSFrostDKGRound1Msg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostDKGRound2Msg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostDKGRound3Msg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostVerifyMsg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostDKGResultMsg, msgID, msgChan)
	defer func() {
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostDKGRound1Msg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostDKGRound2Msg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostDKGRound3Msg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostVerifyMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostDKGResultMsg, msgID)
		t.p2pCommunication.ReleaseStream(msgID)
		t.partyCoordinator.ReleaseStream(msgID)
		close(sigChan)
	}()

	onlinePeers, _, errJoinParty := t.joinParty(
		msgID,
		req.Version,
		req.BlockHeight,
		req.SignerPubKeys,
		frostKeygenJoinThreshold(len(req.SignerPubKeys)),
		sigChan,
	)
	if errJoinParty != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(errJoinParty.Error(), nil),
		}, nil
	}

	activeParticipants, err := conversion.GetPubKeysFromPeerIDs(peerIDsToStrings(onlinePeers))
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("fail to resolve active participants", nil),
		}, nil
	}
	_, _, signerByNormalized, err := buildParticipantIndex(req.SignerPubKeys)
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(err.Error(), nil),
		}, nil
	}
	activeSet, err := buildActiveSet(activeParticipants, signerByNormalized)
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(err.Error(), nil),
		}, nil
	}
	if missing := missingSignersForFullParticipation(activeSet, signerByNormalized); len(missing) > 0 {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost keygen requires all signers online", frostDKGRound1, missing),
		}, nil
	}

	allPeerIDs, peerErr := conversion.GetPeerIDsFromPubKeys(req.SignerPubKeys)
	if peerErr != nil {
		t.logger.Warn().Err(peerErr).Msg("fail to resolve all peer IDs for frost keygen result broadcast, fallback to active peers")
		allPeerIDs = onlinePeers
	}

	coordinator := NewFrostKeygenCoordinator(
		t.p2pCommunication,
		sidecar,
		t.localNodePubKey,
		msgID,
		msgChan,
		t.conf.FrostKeygenTimeout,
	)

	isActive := false
	ourPeerID := t.p2pCommunication.GetLocalPeerID()
	for _, p := range onlinePeers {
		if p.String() == ourPeerID {
			isActive = true
			break
		}
	}

	quorum := len(activeSet.originalByNormalized)
	if !isActive {
		t.logger.Info().Str("msg_id", msgID).Msg("not active FROST keygen member, waiting for result")
		waitResp, waitErr := coordinator.WaitForResult(activeParticipants, quorum, coordinator.timeout+5*time.Second)
		if waitErr != nil {
			return FrostKeygenResponse{
				Status: tcommon.Fail,
				Blame:  blame.NewBlame(waitErr.Error(), nil),
			}, nil
		}
		if waitResp.Status == tcommon.Success {
			t.persistPeerAddressBook()
		}
		return waitResp, nil
	}

	resp := coordinator.Execute(req, activeSet, onlinePeers)
	if resp.Status == tcommon.Success {
		t.persistPeerAddressBook()
		if err := coordinator.BroadcastResult(resp, allPeerIDs); err != nil {
			t.logger.Warn().Err(err).Msg("fail to broadcast frost keygen result")
		}
		return resp, nil
	}

	waitResp, waitErr := coordinator.WaitForResult(activeParticipants, quorum, coordinator.timeout+5*time.Second)
	if waitErr == nil && waitResp.Status == tcommon.Success {
		t.persistPeerAddressBook()
		return waitResp, nil
	}
	if err := coordinator.BroadcastResult(resp, allPeerIDs); err != nil {
		t.logger.Warn().Err(err).Msg("fail to broadcast local frost keygen failure result")
	}
	if waitErr == nil && waitResp.Status == tcommon.Fail && len(waitResp.Blame.BlameNodes) > 0 && len(resp.Blame.BlameNodes) == 0 {
		return waitResp, nil
	}
	return resp, nil
}

func frostKeygenJoinThreshold(participantCount int) int {
	if participantCount <= 0 {
		return 0
	}
	// FROST vault keygen requires every signer to participate, unlike the
	// threshold-based ECDSA/EDDSA join-party path.
	return participantCount - 1
}

func (c *FrostKeygenCoordinator) Execute(
	req FrostKeygenRequest,
	activeSet *activeSignerSet,
	activePeerIDs []peer.ID,
) FrostKeygenResponse {
	if c.sidecar == nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("frost sidecar client is nil", nil),
		}
	}
	if c.msgID == "" {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("frost keygen msgID is empty", nil),
		}
	}
	_, participantIndexMap, originalByNormalized, err := buildParticipantIndex(req.SignerPubKeys)
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(err.Error(), nil),
		}
	}

	localNorm := normalizePubKey(c.localPubKey)
	localIndex, ok := participantIndexMap[localNorm]
	if !ok {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("local signer is not in participant set", nil),
		}
	}
	if _, ok := activeSet.originalByNormalized[localNorm]; !ok {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("local signer is not in active signer set", nil),
		}
	}

	threshold, err := conversion.GetThreshold(len(activeSet.originalByNormalized))
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("failed to compute threshold: %v", err), nil),
		}
	}
	contextValue, err := FrostDKGContext(c.msgID)
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("failed to compute dkg context: %v", err), nil),
		}
	}

	round1Resp, err := c.sidecar.FrostDKGRound1(FrostSidecarDKGRound1Req{
		VaultPubKey:      req.VaultPubKey,
		Threshold:        uint32(threshold + 1),
		N:                uint32(len(activeSet.originalByNormalized)),
		ParticipantIndex: localIndex,
		Context:          contextValue,
		SessionID:        c.msgID,
	})
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost dkg round1 failed: %v", err), nil),
		}
	}

	commitments, failResp := c.exchangeRound1(
		activeSet,
		participantIndexMap,
		activePeerIDs,
		FrostDKGRound1Payload{
			SenderPubKey:     c.localPubKey,
			ParticipantIndex: localIndex,
			Commitments:      round1Resp.Commitments,
		},
	)
	if failResp != nil {
		return *failResp
	}

	round2Resp, err := c.sidecar.FrostDKGRound2(FrostSidecarDKGRound2Req{
		SessionID:      c.msgID,
		AllCommitments: commitments,
	})
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost dkg round2 failed: %v", err), nil),
		}
	}

	encryptedSharesBySigner, failResp := c.exchangeRound2(
		activeSet,
		participantIndexMap,
		activePeerIDs,
		FrostDKGRound2Payload{
			SenderPubKey:     c.localPubKey,
			ParticipantIndex: localIndex,
			EncryptedShares:  round2Resp.EncryptedShares,
		},
	)
	if failResp != nil {
		return *failResp
	}

	localIndexStr := strconv.FormatUint(uint64(localIndex), 10)
	allShares := make(map[string]string, len(encryptedSharesBySigner))
	for senderIndex, senderShares := range encryptedSharesBySigner {
		if senderIndex == localIndexStr {
			continue
		}
		share := strings.TrimSpace(senderShares[localIndexStr])
		if share == "" {
			pubKey := senderIndex
			for norm, idx := range participantIndexMap {
				if strconv.FormatUint(uint64(idx), 10) == senderIndex {
					pubKey = originalByNormalized[norm]
					break
				}
			}
			return c.failForPubKeys("missing encrypted share for local participant", frostDKGRound2, []string{pubKey})
		}
		allShares[senderIndex] = share
	}

	round3Resp, err := c.sidecar.FrostDKGRound3(FrostSidecarDKGRound3Req{
		SessionID: c.msgID,
		AllShares: allShares,
	})
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost dkg round3 failed: %v", err), nil),
		}
	}

	var localMoneroHeight int64
	if h, hErr := c.sidecar.GetChainHeight(); hErr != nil {
		c.logger.Warn().Err(hErr).Msg("failed to query local Monero chain height for DKG birthday")
	} else {
		localMoneroHeight = h
	}

	address, groupKey, minMoneroHeight, failResp := c.exchangeRound3(
		activeSet,
		participantIndexMap,
		activePeerIDs,
		FrostDKGRound3Payload{
			SenderPubKey:         c.localPubKey,
			ParticipantIndex:     localIndex,
			Address:              round3Resp.Address,
			GroupKey:             round3Resp.GroupKey,
			ObservedMoneroHeight: localMoneroHeight,
		},
	)
	if failResp != nil {
		return *failResp
	}
	if minMoneroHeight <= 0 {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost keygen result has no positive observed monero height", frostDKGRound3, nil),
		}
	}

	expectedAddress := strings.TrimSpace(req.ExpectedAddr)
	if expectedAddress != "" && expectedAddress != address {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost keygen result does not match expected address", frostDKGRound3, nil),
		}
	}

	finalizeResp, err := c.sidecar.FrostDKGFinalize(FrostSidecarDKGFinalizeReq{
		SessionID: c.msgID,
		GroupKey:  groupKey,
		Address:   address,
	})
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost dkg finalize failed: %v", err), nil),
		}
	}
	if !finalizeResp.Success {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost dkg finalize returned unsuccessful response", frostDKGRound3, nil),
		}
	}
	keyBytesHex := strings.TrimSpace(finalizeResp.KeyBytesHex)
	if keyBytesHex == "" {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost dkg finalize returned empty key bytes", frostDKGRound3, nil),
		}
	}
	if _, err := hex.DecodeString(keyBytesHex); err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("frost dkg finalize returned invalid key bytes hex", frostDKGRound3, nil),
		}
	}

	return FrostKeygenResponse{
		Address:              address,
		GroupKey:             groupKey,
		KeyBytesHex:          keyBytesHex,
		ObservedMoneroHeight: minMoneroHeight,
		Status:               tcommon.Success,
		Blame:                blame.Blame{},
	}
}

func (c *FrostKeygenCoordinator) exchangeRound1(
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
	activePeerIDs []peer.ID,
	local FrostDKGRound1Payload,
) (map[string]string, *FrostKeygenResponse) {
	payload, err := json.Marshal(local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to marshal round1 payload: %v", err), frostDKGRound1, nil)
		return nil, &resp
	}
	if err := c.broadcast(messages.TSSFrostDKGRound1Msg, payload, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round1 payload: %v", err), frostDKGRound1, nil)
		return nil, &resp
	}
	localHash, err := FrostTranscriptDKGRound1Hash(c.msgID, local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to hash local round1 payload: %v", err), frostDKGRound1, []string{local.SenderPubKey})
		return nil, &resp
	}
	verifier, err := newFrostTranscriptVerifier(frostDKGRound1, activeSet, participantIndexMap)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to initialize round1 transcript verifier: %v", err), frostDKGRound1, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordPayload(local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound1, failure.blamePubKey)
		return nil, &resp
	}
	if err := c.broadcastTranscriptVerification(frostDKGRound1, local.SenderPubKey, local.ParticipantIndex, localHash, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round1 verification: %v", err), frostDKGRound1, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordVerification(c.localPubKey, local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound1, failure.blamePubKey)
		return nil, &resp
	}

	values := map[string]string{
		strconv.FormatUint(uint64(local.ParticipantIndex), 10): strings.TrimSpace(local.Commitments),
	}
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()

	for !verifier.isComplete() {
		select {
		case msg := <-c.msgChan:
			wrapped, ok := parseWrappedMessage(msg.Payload)
			if !ok {
				continue
			}
			if strings.TrimSpace(wrapped.MsgID) != c.msgID {
				continue
			}
			switch wrapped.MessageType {
			case messages.TSSFrostDKGRound1Msg:
				var incoming FrostDKGRound1Payload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid round1 payload", frostDKGRound1)
					return nil, &resp
				}
				senderNorm := normalizePubKey(incoming.SenderPubKey)
				if senderNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in round1 payload", frostDKGRound1)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[senderNorm]; !ok {
					resp := c.failForPubKeys("round1 sender is not in active participant set", frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				expectedIndex := participantIndexMap[senderNorm]
				if incoming.ParticipantIndex != expectedIndex {
					resp := c.failForPubKeys("participant index mismatch in round1 payload", frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if strings.TrimSpace(incoming.Commitments) == "" {
					resp := c.failForPubKeys("empty commitments in round1 payload", frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				payloadHash, err := FrostTranscriptDKGRound1Hash(c.msgID, incoming)
				if err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to hash round1 payload: %v", err), frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				isNewPayload, failure := verifier.recordPayload(incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash)
				if failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound1, failure.blamePubKey)
					return nil, &resp
				}
				if !isNewPayload {
					continue
				}
				values[strconv.FormatUint(uint64(incoming.ParticipantIndex), 10)] = incoming.Commitments
				if err := c.broadcastTranscriptVerification(frostDKGRound1, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash, activePeerIDs); err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round1 verification: %v", err), frostDKGRound1, nil)
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(c.localPubKey, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound1, failure.blamePubKey)
					return nil, &resp
				}
			case messages.TSSFrostVerifyMsg:
				var incoming FrostVerifyPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid transcript verification payload", frostDKGRound1)
					return nil, &resp
				}
				if strings.TrimSpace(incoming.Round) != frostDKGRound1 {
					continue
				}
				attesterNorm := normalizePubKey(incoming.SenderPubKey)
				if attesterNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in transcript verification payload", frostDKGRound1)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[attesterNorm]; !ok {
					resp := c.failForPubKeys("verification attester is not in active participant set", frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound1, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(
					incoming.SenderPubKey,
					incoming.TargetSenderPubKey,
					incoming.TargetParticipantIndex,
					incoming.PayloadHash,
				); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound1, failure.blamePubKey)
					return nil, &resp
				}
			}
		case <-timer.C:
			missingPayloads := verifier.missingPayloadPubKeys()
			missingVerifiers := verifier.missingVerifierPubKeys()
			missing := mergePubKeyLists(missingPayloads, missingVerifiers)
			resp := c.failForPubKeys(
				transcriptTimeoutReason("round1 commitments", missingPayloads, missingVerifiers),
				frostDKGRound1,
				missing,
			)
			return nil, &resp
		}
	}

	return values, nil
}

func (c *FrostKeygenCoordinator) exchangeRound2(
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
	activePeerIDs []peer.ID,
	local FrostDKGRound2Payload,
) (map[string]map[string]string, *FrostKeygenResponse) {
	payload, err := json.Marshal(local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to marshal round2 payload: %v", err), frostDKGRound2, nil)
		return nil, &resp
	}
	if err := c.broadcast(messages.TSSFrostDKGRound2Msg, payload, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round2 payload: %v", err), frostDKGRound2, nil)
		return nil, &resp
	}
	localHash, err := FrostTranscriptDKGRound2Hash(c.msgID, local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to hash local round2 payload: %v", err), frostDKGRound2, []string{local.SenderPubKey})
		return nil, &resp
	}
	verifier, err := newFrostTranscriptVerifier(frostDKGRound2, activeSet, participantIndexMap)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to initialize round2 transcript verifier: %v", err), frostDKGRound2, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordPayload(local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound2, failure.blamePubKey)
		return nil, &resp
	}
	if err := c.broadcastTranscriptVerification(frostDKGRound2, local.SenderPubKey, local.ParticipantIndex, localHash, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round2 verification: %v", err), frostDKGRound2, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordVerification(c.localPubKey, local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound2, failure.blamePubKey)
		return nil, &resp
	}

	localSenderIndex := strconv.FormatUint(uint64(local.ParticipantIndex), 10)
	values := map[string]map[string]string{localSenderIndex: local.EncryptedShares}
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()

	for !verifier.isComplete() {
		select {
		case msg := <-c.msgChan:
			wrapped, ok := parseWrappedMessage(msg.Payload)
			if !ok {
				continue
			}
			if strings.TrimSpace(wrapped.MsgID) != c.msgID {
				continue
			}
			switch wrapped.MessageType {
			case messages.TSSFrostDKGRound2Msg:
				var incoming FrostDKGRound2Payload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid round2 payload", frostDKGRound2)
					return nil, &resp
				}
				senderNorm := normalizePubKey(incoming.SenderPubKey)
				if senderNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in round2 payload", frostDKGRound2)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[senderNorm]; !ok {
					resp := c.failForPubKeys("round2 sender is not in active participant set", frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				expectedIndex := participantIndexMap[senderNorm]
				if incoming.ParticipantIndex != expectedIndex {
					resp := c.failForPubKeys("participant index mismatch in round2 payload", frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if len(incoming.EncryptedShares) == 0 && len(activeSet.originalByNormalized) > 1 {
					resp := c.failForPubKeys("empty encrypted shares in round2 payload", frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				for norm, idx := range participantIndexMap {
					if _, ok := activeSet.originalByNormalized[norm]; !ok {
						continue
					}
					if idx == incoming.ParticipantIndex {
						continue
					}
					idxKey := strconv.FormatUint(uint64(idx), 10)
					if strings.TrimSpace(incoming.EncryptedShares[idxKey]) == "" {
						resp := c.failForPubKeys("missing encrypted share target in round2 payload", frostDKGRound2, []string{incoming.SenderPubKey})
						return nil, &resp
					}
				}
				payloadHash, err := FrostTranscriptDKGRound2Hash(c.msgID, incoming)
				if err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to hash round2 payload: %v", err), frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				isNewPayload, failure := verifier.recordPayload(incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash)
				if failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound2, failure.blamePubKey)
					return nil, &resp
				}
				if !isNewPayload {
					continue
				}
				senderIndex := strconv.FormatUint(uint64(incoming.ParticipantIndex), 10)
				values[senderIndex] = incoming.EncryptedShares
				if err := c.broadcastTranscriptVerification(frostDKGRound2, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash, activePeerIDs); err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round2 verification: %v", err), frostDKGRound2, nil)
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(c.localPubKey, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound2, failure.blamePubKey)
					return nil, &resp
				}
			case messages.TSSFrostVerifyMsg:
				var incoming FrostVerifyPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid transcript verification payload", frostDKGRound2)
					return nil, &resp
				}
				if strings.TrimSpace(incoming.Round) != frostDKGRound2 {
					continue
				}
				attesterNorm := normalizePubKey(incoming.SenderPubKey)
				if attesterNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in transcript verification payload", frostDKGRound2)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[attesterNorm]; !ok {
					resp := c.failForPubKeys("verification attester is not in active participant set", frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound2, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(
					incoming.SenderPubKey,
					incoming.TargetSenderPubKey,
					incoming.TargetParticipantIndex,
					incoming.PayloadHash,
				); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound2, failure.blamePubKey)
					return nil, &resp
				}
			}
		case <-timer.C:
			missingPayloads := verifier.missingPayloadPubKeys()
			missingVerifiers := verifier.missingVerifierPubKeys()
			missing := mergePubKeyLists(missingPayloads, missingVerifiers)
			resp := c.failForPubKeys(
				transcriptTimeoutReason("round2 encrypted shares", missingPayloads, missingVerifiers),
				frostDKGRound2,
				missing,
			)
			return nil, &resp
		}
	}

	return values, nil
}

func (c *FrostKeygenCoordinator) exchangeRound3(
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
	activePeerIDs []peer.ID,
	local FrostDKGRound3Payload,
) (string, string, int64, *FrostKeygenResponse) {
	payload, err := json.Marshal(local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to marshal round3 payload: %v", err), frostDKGRound3, nil)
		return "", "", 0, &resp
	}
	if err := c.broadcast(messages.TSSFrostDKGRound3Msg, payload, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round3 payload: %v", err), frostDKGRound3, nil)
		return "", "", 0, &resp
	}

	groupAddress := strings.TrimSpace(local.Address)
	groupKey := strings.TrimSpace(local.GroupKey)
	minMoneroHeight := local.ObservedMoneroHeight
	if groupAddress == "" || groupKey == "" {
		resp := c.failForPubKeys("empty address/group key in local round3 payload", frostDKGRound3, []string{local.SenderPubKey})
		return "", "", 0, &resp
	}
	if strings.TrimSpace(groupAddress) == "" {
		resp := c.failForPubKeys("empty DKG address", frostDKGRound3, []string{local.SenderPubKey})
		return "", "", 0, &resp
	}
	localHash, err := FrostTranscriptDKGRound3Hash(c.msgID, local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to hash local round3 payload: %v", err), frostDKGRound3, []string{local.SenderPubKey})
		return "", "", 0, &resp
	}
	verifier, err := newFrostTranscriptVerifier(frostDKGRound3, activeSet, participantIndexMap)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to initialize round3 transcript verifier: %v", err), frostDKGRound3, nil)
		return "", "", 0, &resp
	}
	if _, failure := verifier.recordPayload(local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound3, failure.blamePubKey)
		return "", "", 0, &resp
	}
	if err := c.broadcastTranscriptVerification(frostDKGRound3, local.SenderPubKey, local.ParticipantIndex, localHash, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round3 verification: %v", err), frostDKGRound3, nil)
		return "", "", 0, &resp
	}
	if _, failure := verifier.recordVerification(c.localPubKey, local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostDKGRound3, failure.blamePubKey)
		return "", "", 0, &resp
	}
	timer := time.NewTimer(c.timeout)
	defer timer.Stop()

	for !verifier.isComplete() {
		select {
		case msg := <-c.msgChan:
			wrapped, ok := parseWrappedMessage(msg.Payload)
			if !ok {
				continue
			}
			if strings.TrimSpace(wrapped.MsgID) != c.msgID {
				continue
			}
			switch wrapped.MessageType {
			case messages.TSSFrostDKGRound3Msg:
				var incoming FrostDKGRound3Payload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid round3 payload", frostDKGRound3)
					return "", "", 0, &resp
				}
				senderNorm := normalizePubKey(incoming.SenderPubKey)
				if senderNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in round3 payload", frostDKGRound3)
					return "", "", 0, &resp
				}
				if _, ok := activeSet.originalByNormalized[senderNorm]; !ok {
					resp := c.failForPubKeys("round3 sender is not in active participant set", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				expectedIndex := participantIndexMap[senderNorm]
				if incoming.ParticipantIndex != expectedIndex {
					resp := c.failForPubKeys("participant index mismatch in round3 payload", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}

				inAddress := strings.TrimSpace(incoming.Address)
				inGroupKey := strings.TrimSpace(incoming.GroupKey)
				if inAddress == "" || inGroupKey == "" {
					resp := c.failForPubKeys("empty address/group key in round3 payload", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if groupAddress != inAddress {
					resp := c.failForPubKeys("DKG address mismatch across participants", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if !strings.EqualFold(groupKey, inGroupKey) {
					resp := c.failForPubKeys("DKG group key mismatch across participants", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if h := incoming.ObservedMoneroHeight; h > 0 && (minMoneroHeight <= 0 || h < minMoneroHeight) {
					minMoneroHeight = h
				}
				payloadHash, err := FrostTranscriptDKGRound3Hash(c.msgID, incoming)
				if err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to hash round3 payload: %v", err), frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				isNewPayload, failure := verifier.recordPayload(incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash)
				if failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound3, failure.blamePubKey)
					return "", "", 0, &resp
				}
				if !isNewPayload {
					continue
				}
				if err := c.broadcastTranscriptVerification(frostDKGRound3, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash, activePeerIDs); err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast round3 verification: %v", err), frostDKGRound3, nil)
					return "", "", 0, &resp
				}
				if _, failure := verifier.recordVerification(c.localPubKey, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound3, failure.blamePubKey)
					return "", "", 0, &resp
				}
			case messages.TSSFrostVerifyMsg:
				var incoming FrostVerifyPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid transcript verification payload", frostDKGRound3)
					return "", "", 0, &resp
				}
				if strings.TrimSpace(incoming.Round) != frostDKGRound3 {
					continue
				}
				attesterNorm := normalizePubKey(incoming.SenderPubKey)
				if attesterNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in transcript verification payload", frostDKGRound3)
					return "", "", 0, &resp
				}
				if _, ok := activeSet.originalByNormalized[attesterNorm]; !ok {
					resp := c.failForPubKeys("verification attester is not in active participant set", frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostDKGRound3, []string{incoming.SenderPubKey})
					return "", "", 0, &resp
				}
				if _, failure := verifier.recordVerification(
					incoming.SenderPubKey,
					incoming.TargetSenderPubKey,
					incoming.TargetParticipantIndex,
					incoming.PayloadHash,
				); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostDKGRound3, failure.blamePubKey)
					return "", "", 0, &resp
				}
			}
		case <-timer.C:
			missingPayloads := verifier.missingPayloadPubKeys()
			missingVerifiers := verifier.missingVerifierPubKeys()
			if len(missingPayloads) == 0 && len(missingVerifiers) > 0 {
				c.logger.Warn().
					Strs("missing_verifiers", missingVerifiers).
					Msg("accepting matching round3 payloads after transcript verification timeout")
				return groupAddress, groupKey, minMoneroHeight, nil
			}
			missing := mergePubKeyLists(missingPayloads, missingVerifiers)
			resp := c.failForPubKeys(
				transcriptTimeoutReason("round3 results", missingPayloads, missingVerifiers),
				frostDKGRound3,
				missing,
			)
			return "", "", 0, &resp
		}
	}

	return groupAddress, groupKey, minMoneroHeight, nil
}

func (c *FrostKeygenCoordinator) WaitForResult(activeParticipants []string, quorum int, timeout time.Duration) (FrostKeygenResponse, error) {
	_, _, activeByNormalized, err := buildParticipantIndex(activeParticipants)
	if err != nil {
		return FrostKeygenResponse{}, err
	}
	if quorum <= 0 {
		quorum = len(activeByNormalized)
	}
	if quorum <= 0 {
		return FrostKeygenResponse{}, fmt.Errorf("invalid quorum %d", quorum)
	}
	if quorum > len(activeByNormalized) {
		return FrostKeygenResponse{}, fmt.Errorf("invalid quorum %d: only %d active participants", quorum, len(activeByNormalized))
	}
	if timeout <= 0 {
		timeout = c.timeout
	}

	buckets := make(map[string]*dkgResultVoteBucket)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case msg := <-c.msgChan:
			wrapped, ok := parseWrappedMessage(msg.Payload)
			if !ok || wrapped.MessageType != messages.TSSFrostDKGResultMsg {
				continue
			}
			if strings.TrimSpace(wrapped.MsgID) != c.msgID {
				continue
			}
			var payload FrostDKGResultPayload
			if err := json.Unmarshal(wrapped.Payload, &payload); err != nil {
				resp := c.failForMalformed(msg.PeerID, "invalid frost dkg result payload", frostDKGResult)
				return resp, nil
			}

			senderNorm := normalizePubKey(payload.SenderPubKey)
			if senderNorm == "" {
				resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in frost dkg result", frostDKGResult)
				return resp, nil
			}
			if _, ok := activeByNormalized[senderNorm]; !ok {
				resp := c.failForPubKeys("result sender is not in active signer set", frostDKGResult, []string{payload.SenderPubKey})
				return resp, nil
			}
			if err := c.validateSenderPeerBinding(msg.PeerID, payload.SenderPubKey); err != nil {
				resp := c.failForPubKeys(err.Error(), frostDKGResult, []string{payload.SenderPubKey})
				return resp, nil
			}

			normalized, matchKey, err := normalizeDKGResultPayload(payload.Response)
			if err != nil {
				resp := c.failForPubKeys(err.Error(), frostDKGResult, []string{payload.SenderPubKey})
				return resp, nil
			}
			bucket := buckets[matchKey]
			if bucket == nil {
				bucket = &dkgResultVoteBucket{
					response: normalized,
					voters:   make(map[string]struct{}),
				}
				buckets[matchKey] = bucket
			}
			bucket.voters[senderNorm] = struct{}{}
			if len(bucket.voters) >= quorum {
				return bucket.response, nil
			}
		case <-timer.C:
			return FrostKeygenResponse{}, fmt.Errorf("timeout waiting for frost dkg result")
		}
	}
}

func (c *FrostKeygenCoordinator) BroadcastResult(resp FrostKeygenResponse, peers []peer.ID) error {
	result := FrostDKGResultPayload{
		SenderPubKey: c.localPubKey,
		Response: FrostDKGResult{
			Address:              resp.Address,
			GroupKey:             resp.GroupKey,
			ObservedMoneroHeight: resp.ObservedMoneroHeight,
			Status:               uint8(resp.Status),
			Blame:                resp.Blame,
		},
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal frost dkg result payload: %w", err)
	}
	return c.broadcast(messages.TSSFrostDKGResultMsg, payload, peers)
}

func (c *FrostKeygenCoordinator) broadcast(msgType messages.THORChainTSSMessageType, payload []byte, peers []peer.ID) error {
	c.p2pCommunication.BroadcastMsgChan <- &messages.BroadcastMsgChan{
		WrappedMessage: messages.WrappedMessage{
			MessageType: msgType,
			MsgID:       c.msgID,
			Payload:     payload,
		},
		PeersID: peers,
	}
	return nil
}

func (c *FrostKeygenCoordinator) broadcastTranscriptVerification(
	round string,
	targetSenderPubKey string,
	targetParticipantIndex uint32,
	payloadHash string,
	peers []peer.ID,
) error {
	payload := FrostVerifyPayload{
		SenderPubKey:           c.localPubKey,
		Round:                  round,
		TargetSenderPubKey:     targetSenderPubKey,
		TargetParticipantIndex: targetParticipantIndex,
		PayloadHash:            payloadHash,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal transcript verification payload: %w", err)
	}
	return c.broadcast(messages.TSSFrostVerifyMsg, buf, peers)
}

func (c *FrostKeygenCoordinator) validateSenderPeerBinding(msgPeer peer.ID, senderPubKey string) error {
	expectedPeerID, err := conversion.GetPeerIDFromPubKey(senderPubKey)
	if err != nil {
		return fmt.Errorf("failed to parse sender pubkey")
	}
	if expectedPeerID != msgPeer {
		return fmt.Errorf("sender peer binding mismatch")
	}
	return nil
}

func (c *FrostKeygenCoordinator) failForMalformed(msgPeer peer.ID, reason, round string) FrostKeygenResponse {
	pubKey, err := conversion.GetPubKeyFromPeerID(msgPeer.String())
	if err != nil {
		return FrostKeygenResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame(reason, round, nil),
		}
	}
	return c.failForPubKeys(reason, round, []string{pubKey})
}

func (c *FrostKeygenCoordinator) failForPubKeys(reason, round string, pubKeys []string) FrostKeygenResponse {
	return FrostKeygenResponse{
		Status: tcommon.Fail,
		Blame:  buildBlame(reason, round, pubKeys),
	}
}

type dkgResultVoteBucket struct {
	response FrostKeygenResponse
	voters   map[string]struct{}
}

func normalizeDKGResultPayload(result FrostDKGResult) (FrostKeygenResponse, string, error) {
	status := tcommon.Status(result.Status)
	if status != tcommon.Success && status != tcommon.Fail {
		return FrostKeygenResponse{}, "", fmt.Errorf("invalid frost dkg result status")
	}

	normalized := FrostKeygenResponse{
		Status: status,
	}
	switch status {
	case tcommon.Success:
		address := strings.TrimSpace(result.Address)
		groupKey := strings.TrimSpace(result.GroupKey)
		if address == "" {
			return FrostKeygenResponse{}, "", fmt.Errorf("frost dkg success result has empty address")
		}
		if groupKey == "" {
			return FrostKeygenResponse{}, "", fmt.Errorf("frost dkg success result has empty group key")
		}
		if result.ObservedMoneroHeight <= 0 {
			return FrostKeygenResponse{}, "", fmt.Errorf("frost dkg success result has no positive observed monero height")
		}
		normalized.Address = address
		normalized.GroupKey = strings.ToLower(groupKey)
		normalized.ObservedMoneroHeight = result.ObservedMoneroHeight
	case tcommon.Fail:
		normalizedBlame, err := normalizeBlameForResult(result.Blame)
		if err != nil {
			return FrostKeygenResponse{}, "", err
		}
		normalized.Blame = normalizedBlame
	}

	keyBytes, err := json.Marshal(struct {
		Status   tcommon.Status `json:"status"`
		Address  string         `json:"address"`
		GroupKey string         `json:"group_key"`
		Height   int64          `json:"observed_monero_height"`
		Blame    blame.Blame    `json:"blame"`
	}{
		Status:   normalized.Status,
		Address:  normalized.Address,
		GroupKey: normalized.GroupKey,
		Height:   normalized.ObservedMoneroHeight,
		Blame:    normalized.Blame,
	})
	if err != nil {
		return FrostKeygenResponse{}, "", fmt.Errorf("failed to encode frost dkg result comparison key")
	}
	return normalized, string(keyBytes), nil
}

func missingSignersForFullParticipation(activeSet *activeSignerSet, signerByNormalized map[string]string) []string {
	if activeSet == nil {
		missing := make([]string, 0, len(signerByNormalized))
		for _, pubKey := range signerByNormalized {
			missing = append(missing, pubKey)
		}
		sort.Strings(missing)
		return missing
	}

	missing := make([]string, 0)
	for norm, original := range signerByNormalized {
		if _, ok := activeSet.originalByNormalized[norm]; ok {
			continue
		}
		missing = append(missing, original)
	}
	sort.Strings(missing)
	return missing
}
