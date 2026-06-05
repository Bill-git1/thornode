package tss

import (
	"encoding/hex"
	"encoding/json"
	"errors"
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
	frostMsgIDDomain             = "thorchain-frost-keysign-msgid-v4"
	frostDecoyContextDomain      = "thorchain-frost-decoy-context-v1"
	frostPlanHashDomain          = "thorchain-frost-eventuality-plan-v1"
	frostEventualityIDDomain     = "thorchain-frost-eventuality-id-v1"
	frostEventualityVersion      = uint8(1)
	frostRoundPreprocess         = "frost_preprocess"
	frostRoundShare              = "frost_share"
	frostRoundResult             = "frost_result"
	frostEventualityHashHexBytes = 64
)

var errFrostResultTimeout = errors.New("timeout waiting for frost result")

type FrostSignCoordinator struct {
	logger           zerolog.Logger
	p2pCommunication *p2p.Communication
	sidecar          FrostSidecarClient
	localPubKey      string
	msgID            string
	expectedPlanHash string
	msgChan          chan *p2p.Message
	timeout          time.Duration
}

func NewFrostSignCoordinator(
	comm *p2p.Communication,
	sidecar FrostSidecarClient,
	localPubKey string,
	msgID string,
	expectedPlanHash string,
	msgChan chan *p2p.Message,
	timeout time.Duration,
) *FrostSignCoordinator {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	return &FrostSignCoordinator{
		logger:           log.With().Str("module", "frost_keysign").Str("msg_id", msgID).Logger(),
		p2pCommunication: comm,
		sidecar:          sidecar,
		localPubKey:      strings.TrimSpace(localPubKey),
		msgID:            strings.TrimSpace(msgID),
		expectedPlanHash: strings.TrimSpace(expectedPlanHash),
		msgChan:          msgChan,
		timeout:          timeout,
	}
}

func FrostKeySignMsgID(req FrostKeySignRequest) (string, error) {
	vault := strings.TrimSpace(req.VaultPubKey)
	toAddr := strings.TrimSpace(req.ToAddress)
	if vault == "" {
		return "", fmt.Errorf("vault pubkey is required")
	}
	if toAddr == "" {
		return "", fmt.Errorf("to address is required")
	}

	requiredInputsHash, err := frostRequiredInputsHash(req.RequiredInputs)
	if err != nil {
		return "", fmt.Errorf("fail to hash required inputs: %w", err)
	}

	canonical := strings.Join([]string{
		"domain=" + frostMsgIDDomain,
		"vault_pub_key=" + vault,
		"to_address=" + toAddr,
		"amount_atomic=" + strconv.FormatUint(req.AmountAtomic, 10),
		"memo=" + req.Memo,
		"fee_rate=" + strconv.FormatUint(req.FeeRate, 10),
		"required_inputs_hash=" + requiredInputsHash,
		"max_inputs=" + strconv.FormatInt(req.MaxInputs, 10),
		"max_selected_input_value_atomic=" + strconv.FormatUint(req.MaxSelectedInputValueAtomic, 10),
		"tx_in_hash=" + strings.ToLower(strings.TrimSpace(req.TxInHash)),
		"request_id=" + strings.TrimSpace(req.RequestID),
		"block_height=" + strconv.FormatInt(req.BlockHeight, 10),
	}, "\n")
	return tcommon.MsgToHashString([]byte(canonical))
}

func FrostDecoyContext(msgID string) (string, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return "", fmt.Errorf("msgID is required")
	}
	return tcommon.MsgToHashString([]byte(frostDecoyContextDomain + "\n" + msgID))
}

func frostSignerSetHash(signerPubKeys []string) (string, error) {
	members, _, _, err := buildParticipantIndex(signerPubKeys)
	if err != nil {
		return "", err
	}
	return tcommon.MsgToHashString([]byte(strings.Join(members, ",")))
}

// frostActiveSignerPubKeys returns the filtered FROST signing party when one
// is supplied, otherwise it falls back to full vault membership.
func frostActiveSignerPubKeys(req FrostKeySignRequest) []string {
	if len(req.ActiveSignerPubKeys) > 0 {
		return req.ActiveSignerPubKeys
	}
	return req.SignerPubKeys
}

func frostRequiredInputsHash(inputs []FrostSpentOutputRef) (string, error) {
	normalized := make([]FrostSpentOutputRef, 0, len(inputs))
	for _, input := range inputs {
		txHash := strings.ToLower(strings.TrimSpace(input.TxHash))
		if txHash == "" {
			return "", fmt.Errorf("required input tx_hash is empty")
		}
		normalized = append(normalized, FrostSpentOutputRef{
			TxHash:      txHash,
			OutputIndex: input.OutputIndex,
			KeyImage:    strings.ToLower(strings.TrimSpace(input.KeyImage)),
			SpendTxHash: strings.ToLower(strings.TrimSpace(input.SpendTxHash)),
		})
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].TxHash == normalized[j].TxHash {
			if normalized[i].OutputIndex == normalized[j].OutputIndex {
				if normalized[i].KeyImage == normalized[j].KeyImage {
					return normalized[i].SpendTxHash < normalized[j].SpendTxHash
				}
				return normalized[i].KeyImage < normalized[j].KeyImage
			}
			return normalized[i].OutputIndex < normalized[j].OutputIndex
		}
		return normalized[i].TxHash < normalized[j].TxHash
	})
	buf, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return tcommon.MsgToHashString(buf)
}

func FrostPlanHash(req FrostKeySignRequest, msgID string) (string, error) {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return "", fmt.Errorf("msgID is required")
	}
	vault := strings.TrimSpace(req.VaultPubKey)
	toAddress := strings.TrimSpace(req.ToAddress)
	if vault == "" {
		return "", fmt.Errorf("vault pubkey is required")
	}
	if toAddress == "" {
		return "", fmt.Errorf("to address is required")
	}
	signerSetHash, err := frostSignerSetHash(req.SignerPubKeys)
	if err != nil {
		return "", fmt.Errorf("fail to hash signer membership: %w", err)
	}
	activeSignerSetHash, err := frostSignerSetHash(frostActiveSignerPubKeys(req))
	if err != nil {
		return "", fmt.Errorf("fail to hash active signer membership: %w", err)
	}
	requiredInputsHash, err := frostRequiredInputsHash(req.RequiredInputs)
	if err != nil {
		return "", fmt.Errorf("fail to hash required inputs: %w", err)
	}
	canonical := strings.Join([]string{
		"domain=" + frostPlanHashDomain,
		"msg_id=" + msgID,
		"vault_pub_key=" + vault,
		"to_address=" + toAddress,
		"amount_atomic=" + strconv.FormatUint(req.AmountAtomic, 10),
		"memo=" + req.Memo,
		"fee_rate=" + strconv.FormatUint(req.FeeRate, 10),
		"required_inputs_hash=" + requiredInputsHash,
		"max_inputs=" + strconv.FormatInt(req.MaxInputs, 10),
		"max_selected_input_value_atomic=" + strconv.FormatUint(req.MaxSelectedInputValueAtomic, 10),
		"signer_set_hash=" + signerSetHash,
		"active_signer_set_hash=" + activeSignerSetHash,
	}, "\n")
	return tcommon.MsgToHashString([]byte(canonical))
}

func FrostTxHexHash(txHex string) (string, error) {
	txHex = strings.TrimSpace(txHex)
	if txHex == "" {
		return "", fmt.Errorf("tx_hex is required")
	}
	raw, err := hex.DecodeString(txHex)
	if err != nil {
		return "", fmt.Errorf("tx_hex is not valid hex")
	}
	return tcommon.MsgToHashString(raw)
}

func FrostEventualityID(planHash, txHash, txHexHash string, feeAtomic uint64) (string, error) {
	planHash = strings.ToLower(strings.TrimSpace(planHash))
	txHash = strings.ToLower(strings.TrimSpace(txHash))
	txHexHash = strings.ToLower(strings.TrimSpace(txHexHash))
	if err := validateHashHex("plan_hash", planHash); err != nil {
		return "", err
	}
	if err := validateHashHex("tx_hash", txHash); err != nil {
		return "", err
	}
	if err := validateHashHex("tx_hex_hash", txHexHash); err != nil {
		return "", err
	}
	canonical := strings.Join([]string{
		"domain=" + frostEventualityIDDomain,
		"plan_hash=" + planHash,
		"tx_hash=" + txHash,
		"tx_hex_hash=" + txHexHash,
		"fee_atomic=" + strconv.FormatUint(feeAtomic, 10),
	}, "\n")
	return tcommon.MsgToHashString([]byte(canonical))
}

func BuildFrostEventuality(planHash, txHash, txHex string, feeAtomic uint64) (FrostEventuality, error) {
	txHexHash, err := FrostTxHexHash(txHex)
	if err != nil {
		return FrostEventuality{}, err
	}
	eventualityID, err := FrostEventualityID(planHash, txHash, txHexHash, feeAtomic)
	if err != nil {
		return FrostEventuality{}, err
	}
	return FrostEventuality{
		Version:       frostEventualityVersion,
		PlanHash:      strings.ToLower(strings.TrimSpace(planHash)),
		TxHexHash:     strings.ToLower(strings.TrimSpace(txHexHash)),
		EventualityID: strings.ToLower(strings.TrimSpace(eventualityID)),
	}, nil
}

func ValidateFrostEventuality(
	req FrostKeySignRequest,
	msgID string,
	txHash string,
	txHex string,
	feeAtomic uint64,
	eventuality FrostEventuality,
) error {
	msgID = strings.TrimSpace(msgID)
	if msgID == "" {
		return fmt.Errorf("msg_id is required")
	}
	expectedPlanHash, err := FrostPlanHash(req, msgID)
	if err != nil {
		return fmt.Errorf("fail to derive plan hash: %w", err)
	}
	if eventuality.Version != frostEventualityVersion {
		return fmt.Errorf("unexpected eventuality version %d", eventuality.Version)
	}
	if !strings.EqualFold(strings.TrimSpace(eventuality.PlanHash), expectedPlanHash) {
		return fmt.Errorf("eventuality plan_hash mismatch")
	}
	expectedTxHexHash, err := FrostTxHexHash(txHex)
	if err != nil {
		return fmt.Errorf("fail to derive tx_hex_hash: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(eventuality.TxHexHash), expectedTxHexHash) {
		return fmt.Errorf("eventuality tx_hex_hash mismatch")
	}
	expectedEventualityID, err := FrostEventualityID(expectedPlanHash, txHash, expectedTxHexHash, feeAtomic)
	if err != nil {
		return fmt.Errorf("fail to derive eventuality_id: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(eventuality.EventualityID), expectedEventualityID) {
		return fmt.Errorf("eventuality_id mismatch")
	}
	return nil
}

func validateHashHex(label, value string) error {
	if len(value) != frostEventualityHashHexBytes {
		return fmt.Errorf("%s must be %d hex chars", label, frostEventualityHashHexBytes)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%s is not valid hex", label)
	}
	return nil
}

func (t *TssServer) FrostKeySign(req FrostKeySignRequest, sidecar FrostSidecarClient) (FrostKeySignResponse, error) {
	if sidecar == nil {
		return FrostKeySignResponse{}, fmt.Errorf("frost sidecar client is nil")
	}
	if len(req.SignerPubKeys) == 0 {
		return FrostKeySignResponse{}, fmt.Errorf("signer pub keys are empty")
	}
	activeSignerPubKeys := frostActiveSignerPubKeys(req)
	if len(activeSignerPubKeys) == 0 {
		return FrostKeySignResponse{}, fmt.Errorf("active signer pub keys are empty")
	}
	msgID, err := FrostKeySignMsgID(req)
	if err != nil {
		return FrostKeySignResponse{}, fmt.Errorf("fail to compute frost msgID: %w", err)
	}
	planHash, err := FrostPlanHash(req, msgID)
	if err != nil {
		return FrostKeySignResponse{}, fmt.Errorf("fail to compute frost plan hash: %w", err)
	}
	threshold, err := conversion.GetThreshold(len(req.SignerPubKeys))
	if err != nil {
		return FrostKeySignResponse{}, fmt.Errorf("fail to compute frost threshold: %w", err)
	}
	if len(activeSignerPubKeys) <= threshold {
		return FrostKeySignResponse{}, fmt.Errorf(
			"not enough eligible frost signers: need %d of %d, got %d",
			threshold+1,
			len(req.SignerPubKeys),
			len(activeSignerPubKeys),
		)
	}

	msgChan := make(chan *p2p.Message, 1024)
	sigChan := make(chan string, 2)

	t.p2pCommunication.SetSubscribe(messages.TSSFrostPreprocessMsg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostShareMsg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostVerifyMsg, msgID, msgChan)
	t.p2pCommunication.SetSubscribe(messages.TSSFrostResultMsg, msgID, msgChan)
	defer func() {
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostPreprocessMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostShareMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostVerifyMsg, msgID)
		t.p2pCommunication.CancelSubscribe(messages.TSSFrostResultMsg, msgID)
		t.p2pCommunication.ReleaseStream(msgID)
		t.partyCoordinator.ReleaseStream(msgID)
		close(sigChan)
	}()

	coordinator := NewFrostSignCoordinator(
		t.p2pCommunication,
		sidecar,
		t.localNodePubKey,
		msgID,
		planHash,
		msgChan,
		t.conf.KeySignTimeout,
	)

	// Nodes outside the filtered active set must not join the FROST ceremony,
	// but they can still import a quorum-verified result broadcast by signers.
	if !containsNormalizedPubKey(activeSignerPubKeys, t.localNodePubKey) {
		t.logger.Info().Str("msg_id", msgID).Msg("not eligible FROST signer, waiting for result")
		quorum := threshold + 1
		waitResp, waitErr := coordinator.WaitForResult(activeSignerPubKeys, quorum, t.conf.KeySignTimeout+5*time.Second)
		if waitErr != nil {
			if !errors.Is(waitErr, errFrostResultTimeout) {
				return FrostKeySignResponse{}, fmt.Errorf("fail to wait for FROST result as non-eligible signer: %w", waitErr)
			}
			t.logger.Info().Err(waitErr).Str("msg_id", msgID).Msg("timed out waiting for FROST result as non-eligible signer; standing down")
			return FrostKeySignResponse{
				Status: tcommon.NA,
				MsgID:  msgID,
			}, nil
		}
		if waitResp.Status == tcommon.Success {
			t.persistPeerAddressBook()
		}
		return waitResp, nil
	}

	onlinePeers, _, errJoinParty := t.joinParty(
		msgID,
		req.Version,
		req.BlockHeight,
		activeSignerPubKeys,
		threshold,
		sigChan,
	)
	if errJoinParty != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(errJoinParty.Error(), nil),
		}, nil
	}

	activeParticipants, err := conversion.GetPubKeysFromPeerIDs(peerIDsToStrings(onlinePeers))
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("fail to resolve active participants", nil),
		}, nil
	}
	allPeerIDs, peerErr := conversion.GetPeerIDsFromPubKeys(req.SignerPubKeys)
	if peerErr != nil {
		t.logger.Warn().Err(peerErr).Msg("fail to resolve all peer IDs for frost result broadcast, fallback to active peers")
		allPeerIDs = onlinePeers
	}

	isActive := false
	ourPeerID := t.p2pCommunication.GetLocalPeerID()
	for _, p := range onlinePeers {
		if p.String() == ourPeerID {
			isActive = true
			break
		}
	}

	if !isActive {
		t.logger.Info().Str("msg_id", msgID).Msg("not active FROST signer, waiting for result")
		quorum := threshold + 1
		waitResp, waitErr := coordinator.WaitForResult(activeParticipants, quorum, t.conf.KeySignTimeout+5*time.Second)
		if waitErr != nil {
			return FrostKeySignResponse{
				Status: tcommon.Fail,
				Blame:  blame.NewBlame(waitErr.Error(), nil),
			}, nil
		}
		if waitResp.Status == tcommon.Success {
			t.persistPeerAddressBook()
		}
		return waitResp, nil
	}

	resp := coordinator.Execute(req, activeParticipants, onlinePeers)
	if resp.Status == tcommon.Success {
		t.persistPeerAddressBook()
		if err := coordinator.BroadcastResult(resp, allPeerIDs); err != nil {
			t.logger.Warn().Err(err).Msg("fail to broadcast frost result")
		}
		return resp, nil
	}

	quorum := threshold + 1
	waitResp, waitErr := coordinator.WaitForResult(activeParticipants, quorum, t.conf.KeySignTimeout+5*time.Second)
	if waitErr == nil && waitResp.Status == tcommon.Success {
		t.persistPeerAddressBook()
		return waitResp, nil
	}

	if err := coordinator.BroadcastResult(resp, allPeerIDs); err != nil {
		t.logger.Warn().Err(err).Msg("fail to broadcast local frost failure result")
	}
	if waitErr == nil && waitResp.Status == tcommon.Fail && len(waitResp.Blame.BlameNodes) > 0 && len(resp.Blame.BlameNodes) == 0 {
		return waitResp, nil
	}
	return resp, nil
}

func (c *FrostSignCoordinator) Execute(
	req FrostKeySignRequest,
	activeParticipants []string,
	activePeerIDs []peer.ID,
) FrostKeySignResponse {
	if c.sidecar == nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("frost sidecar client is nil", nil),
		}
	}
	if c.msgID == "" {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("frost msgID is empty", nil),
		}
	}
	_, participantIndexMap, originalByNormalized, err := buildParticipantIndex(req.SignerPubKeys)
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(err.Error(), nil),
		}
	}
	activeSet, err := buildActiveSet(activeParticipants, originalByNormalized)
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(err.Error(), nil),
		}
	}
	localNorm := normalizePubKey(c.localPubKey)
	localIndex, ok := participantIndexMap[localNorm]
	if !ok {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("local signer is not in participant set", nil),
		}
	}
	if _, ok := activeSet.originalByNormalized[localNorm]; !ok {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame("local signer is not in active signer set", nil),
		}
	}
	decoyContext, err := FrostDecoyContext(c.msgID)
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("fail to derive decoy context: %v", err), nil),
		}
	}

	prepareResp, err := c.sidecar.FrostSignPrepare(FrostSidecarPrepareReq{
		MsgID:                       c.msgID,
		VaultPubKey:                 req.VaultPubKey,
		ToAddress:                   req.ToAddress,
		AmountAtomic:                req.AmountAtomic,
		Memo:                        req.Memo,
		FeeRate:                     req.FeeRate,
		DecoyContext:                decoyContext,
		RequiredInputs:              req.RequiredInputs,
		MaxInputs:                   req.MaxInputs,
		DustThreshold:               req.DustThreshold,
		MaxSelectedInputValueAtomic: req.MaxSelectedInputValueAtomic,
	})
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost prepare failed: %v", err), nil),
		}
	}

	preprocesses, failResp := c.exchangePreprocess(
		activeSet,
		participantIndexMap,
		activePeerIDs,
		FrostPreprocessPayload{
			SenderPubKey:     c.localPubKey,
			ParticipantIndex: localIndex,
			PreprocessHex:    prepareResp.Preprocess,
		},
	)
	if failResp != nil {
		return *failResp
	}

	partialResp, err := c.sidecar.FrostSignPartial(FrostSidecarPartialReq{
		MsgID:           c.msgID,
		SessionID:       prepareResp.SessionID,
		AllPreprocesses: preprocesses,
	})
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost partial failed: %v", err), nil),
		}
	}

	shares, failResp := c.exchangeShares(
		activeSet,
		participantIndexMap,
		activePeerIDs,
		FrostSharePayload{
			SenderPubKey:     c.localPubKey,
			ParticipantIndex: localIndex,
			ShareHex:         partialResp.SignatureShare,
		},
	)
	if failResp != nil {
		return *failResp
	}

	combineResp, err := c.sidecar.FrostSignCombine(FrostSidecarCombineReq{
		MsgID:     c.msgID,
		SessionID: prepareResp.SessionID,
		AllShares: shares,
	})
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("frost combine failed: %v", err), nil),
		}
	}
	eventuality, err := BuildFrostEventuality(c.expectedPlanHash, combineResp.TxHash, combineResp.TxHex, combineResp.FeeAtomic)
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("failed to derive eventuality: %v", err), nil),
		}
	}

	return FrostKeySignResponse{
		TxHash:          combineResp.TxHash,
		TxHex:           combineResp.TxHex,
		FeeAtomic:       combineResp.FeeAtomic,
		MsgID:           c.msgID,
		Eventuality:     eventuality,
		SpentOutputRefs: combineResp.SpentOutputRefs,
		Status:          tcommon.Success,
		Blame:           blame.Blame{},
	}
}

func (c *FrostSignCoordinator) exchangePreprocess(
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
	activePeerIDs []peer.ID,
	local FrostPreprocessPayload,
) (map[string]string, *FrostKeySignResponse) {
	payload, err := json.Marshal(local)
	if err != nil {
		resp := FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("fail to marshal preprocess payload: %v", err), nil),
		}
		return nil, &resp
	}
	if err := c.broadcast(messages.TSSFrostPreprocessMsg, payload, activePeerIDs); err != nil {
		resp := FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("fail to broadcast preprocess payload: %v", err), nil),
		}
		return nil, &resp
	}
	localHash, err := FrostTranscriptPreprocessHash(c.msgID, local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to hash local preprocess payload: %v", err), frostRoundPreprocess, []string{local.SenderPubKey})
		return nil, &resp
	}
	verifier, err := newFrostTranscriptVerifier(frostRoundPreprocess, activeSet, participantIndexMap)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to initialize preprocess transcript verifier: %v", err), frostRoundPreprocess, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordPayload(local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostRoundPreprocess, failure.blamePubKey)
		return nil, &resp
	}
	if err := c.broadcastTranscriptVerification(frostRoundPreprocess, local.SenderPubKey, local.ParticipantIndex, localHash, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast preprocess verification: %v", err), frostRoundPreprocess, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordVerification(c.localPubKey, local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostRoundPreprocess, failure.blamePubKey)
		return nil, &resp
	}

	values := map[string]string{
		strconv.FormatUint(uint64(local.ParticipantIndex), 10): local.PreprocessHex,
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
			case messages.TSSFrostPreprocessMsg:
				var incoming FrostPreprocessPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid preprocess payload", frostRoundPreprocess)
					return nil, &resp
				}
				senderNorm := normalizePubKey(incoming.SenderPubKey)
				if senderNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in preprocess payload", frostRoundPreprocess)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[senderNorm]; !ok {
					resp := c.failForPubKeys("preprocess sender is not in active participant set", frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				expectedIndex := participantIndexMap[senderNorm]
				if incoming.ParticipantIndex != expectedIndex {
					resp := c.failForPubKeys("participant index mismatch in preprocess payload", frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if strings.TrimSpace(incoming.PreprocessHex) == "" {
					resp := c.failForPubKeys("empty preprocess payload", frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				payloadHash, err := FrostTranscriptPreprocessHash(c.msgID, incoming)
				if err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to hash preprocess payload: %v", err), frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				isNewPayload, failure := verifier.recordPayload(incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash)
				if failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundPreprocess, failure.blamePubKey)
					return nil, &resp
				}
				if !isNewPayload {
					continue
				}
				values[strconv.FormatUint(uint64(incoming.ParticipantIndex), 10)] = incoming.PreprocessHex
				if err := c.broadcastTranscriptVerification(frostRoundPreprocess, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash, activePeerIDs); err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast preprocess verification: %v", err), frostRoundPreprocess, nil)
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(c.localPubKey, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundPreprocess, failure.blamePubKey)
					return nil, &resp
				}
			case messages.TSSFrostVerifyMsg:
				var incoming FrostVerifyPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid transcript verification payload", frostRoundPreprocess)
					return nil, &resp
				}
				if strings.TrimSpace(incoming.Round) != frostRoundPreprocess {
					continue
				}
				attesterNorm := normalizePubKey(incoming.SenderPubKey)
				if attesterNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in transcript verification payload", frostRoundPreprocess)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[attesterNorm]; !ok {
					resp := c.failForPubKeys("verification attester is not in active participant set", frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostRoundPreprocess, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(
					incoming.SenderPubKey,
					incoming.TargetSenderPubKey,
					incoming.TargetParticipantIndex,
					incoming.PayloadHash,
				); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundPreprocess, failure.blamePubKey)
					return nil, &resp
				}
			}
		case <-timer.C:
			missingPayloads := verifier.missingPayloadPubKeys()
			missingVerifiers := verifier.missingVerifierPubKeys()
			missing := mergePubKeyLists(missingPayloads, missingVerifiers)
			resp := c.failForPubKeys(
				transcriptTimeoutReason("preprocess payloads", missingPayloads, missingVerifiers),
				frostRoundPreprocess,
				missing,
			)
			return nil, &resp
		}
	}

	return values, nil
}

func (c *FrostSignCoordinator) exchangeShares(
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
	activePeerIDs []peer.ID,
	local FrostSharePayload,
) (map[string]string, *FrostKeySignResponse) {
	payload, err := json.Marshal(local)
	if err != nil {
		resp := FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("fail to marshal share payload: %v", err), nil),
		}
		return nil, &resp
	}
	if err := c.broadcast(messages.TSSFrostShareMsg, payload, activePeerIDs); err != nil {
		resp := FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  blame.NewBlame(fmt.Sprintf("fail to broadcast share payload: %v", err), nil),
		}
		return nil, &resp
	}
	localHash, err := FrostTranscriptShareHash(c.msgID, local)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to hash local share payload: %v", err), frostRoundShare, []string{local.SenderPubKey})
		return nil, &resp
	}
	verifier, err := newFrostTranscriptVerifier(frostRoundShare, activeSet, participantIndexMap)
	if err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to initialize share transcript verifier: %v", err), frostRoundShare, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordPayload(local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostRoundShare, failure.blamePubKey)
		return nil, &resp
	}
	if err := c.broadcastTranscriptVerification(frostRoundShare, local.SenderPubKey, local.ParticipantIndex, localHash, activePeerIDs); err != nil {
		resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast share verification: %v", err), frostRoundShare, nil)
		return nil, &resp
	}
	if _, failure := verifier.recordVerification(c.localPubKey, local.SenderPubKey, local.ParticipantIndex, localHash); failure != nil {
		resp := c.failForPubKeys(failure.reason, frostRoundShare, failure.blamePubKey)
		return nil, &resp
	}

	values := map[string]string{
		strconv.FormatUint(uint64(local.ParticipantIndex), 10): local.ShareHex,
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
			case messages.TSSFrostShareMsg:
				var incoming FrostSharePayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid share payload", frostRoundShare)
					return nil, &resp
				}
				senderNorm := normalizePubKey(incoming.SenderPubKey)
				if senderNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in share payload", frostRoundShare)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[senderNorm]; !ok {
					resp := c.failForPubKeys("share sender is not in active participant set", frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				expectedIndex := participantIndexMap[senderNorm]
				if incoming.ParticipantIndex != expectedIndex {
					resp := c.failForPubKeys("participant index mismatch in share payload", frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if strings.TrimSpace(incoming.ShareHex) == "" {
					resp := c.failForPubKeys("empty share payload", frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				payloadHash, err := FrostTranscriptShareHash(c.msgID, incoming)
				if err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to hash share payload: %v", err), frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				isNewPayload, failure := verifier.recordPayload(incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash)
				if failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundShare, failure.blamePubKey)
					return nil, &resp
				}
				if !isNewPayload {
					continue
				}
				values[strconv.FormatUint(uint64(incoming.ParticipantIndex), 10)] = incoming.ShareHex
				if err := c.broadcastTranscriptVerification(frostRoundShare, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash, activePeerIDs); err != nil {
					resp := c.failForPubKeys(fmt.Sprintf("failed to broadcast share verification: %v", err), frostRoundShare, nil)
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(c.localPubKey, incoming.SenderPubKey, incoming.ParticipantIndex, payloadHash); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundShare, failure.blamePubKey)
					return nil, &resp
				}
			case messages.TSSFrostVerifyMsg:
				var incoming FrostVerifyPayload
				if err := json.Unmarshal(wrapped.Payload, &incoming); err != nil {
					resp := c.failForMalformed(msg.PeerID, "invalid transcript verification payload", frostRoundShare)
					return nil, &resp
				}
				if strings.TrimSpace(incoming.Round) != frostRoundShare {
					continue
				}
				attesterNorm := normalizePubKey(incoming.SenderPubKey)
				if attesterNorm == "" {
					resp := c.failForMalformed(msg.PeerID, "empty sender pubkey in transcript verification payload", frostRoundShare)
					return nil, &resp
				}
				if _, ok := activeSet.originalByNormalized[attesterNorm]; !ok {
					resp := c.failForPubKeys("verification attester is not in active participant set", frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if err := c.validateSenderPeerBinding(msg.PeerID, incoming.SenderPubKey); err != nil {
					resp := c.failForPubKeys(err.Error(), frostRoundShare, []string{incoming.SenderPubKey})
					return nil, &resp
				}
				if _, failure := verifier.recordVerification(
					incoming.SenderPubKey,
					incoming.TargetSenderPubKey,
					incoming.TargetParticipantIndex,
					incoming.PayloadHash,
				); failure != nil {
					resp := c.failForPubKeys(failure.reason, frostRoundShare, failure.blamePubKey)
					return nil, &resp
				}
			}
		case <-timer.C:
			missingPayloads := verifier.missingPayloadPubKeys()
			missingVerifiers := verifier.missingVerifierPubKeys()
			missing := mergePubKeyLists(missingPayloads, missingVerifiers)
			resp := c.failForPubKeys(
				transcriptTimeoutReason("share payloads", missingPayloads, missingVerifiers),
				frostRoundShare,
				missing,
			)
			return nil, &resp
		}
	}

	return values, nil
}

func (c *FrostSignCoordinator) WaitForResult(activeParticipants []string, quorum int, timeout time.Duration) (FrostKeySignResponse, error) {
	_, _, activeByNormalized, err := buildParticipantIndex(activeParticipants)
	if err != nil {
		return FrostKeySignResponse{}, err
	}
	if quorum <= 0 {
		threshold, thresholdErr := conversion.GetThreshold(len(activeByNormalized))
		if thresholdErr != nil {
			return FrostKeySignResponse{}, thresholdErr
		}
		quorum = threshold + 1
	}
	if quorum <= 0 {
		return FrostKeySignResponse{}, fmt.Errorf("invalid quorum %d", quorum)
	}
	if quorum > len(activeByNormalized) {
		return FrostKeySignResponse{}, fmt.Errorf(
			"invalid quorum %d: only %d active participants",
			quorum,
			len(activeByNormalized),
		)
	}
	if timeout <= 0 {
		timeout = c.timeout
	}

	buckets := make(map[string]*resultVoteBucket)
	malformedBuckets := make(map[string]*malformedResultVoteBucket)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case msg := <-c.msgChan:
			wrapped, ok := parseWrappedMessage(msg.Payload)
			if !ok || wrapped.MessageType != messages.TSSFrostResultMsg {
				continue
			}
			if strings.TrimSpace(wrapped.MsgID) != c.msgID {
				continue
			}
			var payload FrostResultPayload
			if err := json.Unmarshal(wrapped.Payload, &payload); err != nil {
				c.logger.Warn().Err(err).Str("peer", msg.PeerID.String()).Msg("skipping malformed frost result payload")
				continue
			}
			senderNorm := normalizePubKey(payload.SenderPubKey)
			if senderNorm == "" {
				c.logger.Warn().Str("peer", msg.PeerID.String()).Msg("skipping frost result with empty sender pubkey")
				continue
			}
			if _, ok := activeByNormalized[senderNorm]; !ok {
				c.logger.Warn().Str("sender", payload.SenderPubKey).Msg("skipping frost result from non-active signer")
				continue
			}
			if err := c.validateSenderPeerBinding(msg.PeerID, payload.SenderPubKey); err != nil {
				c.logger.Warn().Err(err).Str("sender", payload.SenderPubKey).Msg("skipping frost result with peer binding mismatch")
				continue
			}

			normalized, matchKey, err := normalizeResultPayload(payload.Response, c.msgID, c.expectedPlanHash)
			if err != nil {
				reason := err.Error()
				c.logger.Warn().Err(err).Str("sender", payload.SenderPubKey).Msg("skipping malformed frost result")
				bucket := malformedBuckets[reason]
				if bucket == nil {
					bucket = &malformedResultVoteBucket{
						reason: reason,
						voters: make(map[string]string),
					}
					malformedBuckets[reason] = bucket
				}
				bucket.voters[senderNorm] = activeByNormalized[senderNorm]
				if len(bucket.voters) >= quorum {
					resp := c.failForPubKeys(reason, frostRoundResult, bucket.pubKeys())
					return resp, nil
				}
				continue
			}

			bucket := buckets[matchKey]
			if bucket == nil {
				bucket = &resultVoteBucket{
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
			return FrostKeySignResponse{}, errFrostResultTimeout
		}
	}
}

func (c *FrostSignCoordinator) BroadcastResult(resp FrostKeySignResponse, peers []peer.ID) error {
	result := FrostResultPayload{
		SenderPubKey: c.localPubKey,
		Response: FrostResult{
			MsgID:           resp.MsgID,
			TxHash:          resp.TxHash,
			TxHex:           resp.TxHex,
			FeeAtomic:       resp.FeeAtomic,
			Eventuality:     resp.Eventuality,
			SpentOutputRefs: resp.SpentOutputRefs,
			Status:          uint8(resp.Status),
			Blame:           resp.Blame,
		},
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("fail to marshal frost result payload: %w", err)
	}
	return c.broadcast(messages.TSSFrostResultMsg, payload, peers)
}

func (c *FrostSignCoordinator) broadcast(msgType messages.THORChainTSSMessageType, payload []byte, peers []peer.ID) error {
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

func (c *FrostSignCoordinator) broadcastTranscriptVerification(
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

func (c *FrostSignCoordinator) validateSenderPeerBinding(msgPeer peer.ID, senderPubKey string) error {
	expectedPeerID, err := conversion.GetPeerIDFromPubKey(senderPubKey)
	if err != nil {
		return fmt.Errorf("failed to parse sender pubkey")
	}
	if expectedPeerID != msgPeer {
		return fmt.Errorf("sender peer binding mismatch")
	}
	return nil
}

func (c *FrostSignCoordinator) failForMalformed(msgPeer peer.ID, reason, round string) FrostKeySignResponse {
	pubKey, err := conversion.GetPubKeyFromPeerID(msgPeer.String())
	if err != nil {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame(reason, round, nil),
		}
	}
	return c.failForPubKeys(reason, round, []string{pubKey})
}

func (c *FrostSignCoordinator) failForPubKeys(reason, round string, pubKeys []string) FrostKeySignResponse {
	return FrostKeySignResponse{
		Status: tcommon.Fail,
		Blame:  buildBlame(reason, round, pubKeys),
	}
}

func parseWrappedMessage(raw []byte) (messages.WrappedMessage, bool) {
	var wrapped messages.WrappedMessage
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return messages.WrappedMessage{}, false
	}
	return wrapped, true
}

type resultVoteBucket struct {
	response FrostKeySignResponse
	voters   map[string]struct{}
}

type malformedResultVoteBucket struct {
	reason string
	voters map[string]string
}

func (b *malformedResultVoteBucket) pubKeys() []string {
	pubKeys := make([]string, 0, len(b.voters))
	for _, pubKey := range b.voters {
		pubKeys = append(pubKeys, pubKey)
	}
	sort.Strings(pubKeys)
	return pubKeys
}

func normalizeResultPayload(result FrostResult, expectedMsgID, expectedPlanHash string) (FrostKeySignResponse, string, error) {
	expectedMsgID = strings.TrimSpace(expectedMsgID)
	expectedPlanHash = strings.ToLower(strings.TrimSpace(expectedPlanHash))
	status := tcommon.Status(result.Status)
	if status != tcommon.Success && status != tcommon.Fail {
		return FrostKeySignResponse{}, "", fmt.Errorf("invalid frost result status")
	}

	normalized := FrostKeySignResponse{
		MsgID:     expectedMsgID,
		Status:    status,
		FeeAtomic: result.FeeAtomic,
	}

	switch status {
	case tcommon.Success:
		payloadMsgID := strings.TrimSpace(result.MsgID)
		if payloadMsgID == "" {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result has empty msg_id")
		}
		if expectedMsgID != "" && payloadMsgID != expectedMsgID {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result msg_id mismatch")
		}
		txHash := strings.ToLower(strings.TrimSpace(result.TxHash))
		txHex := strings.ToLower(strings.TrimSpace(result.TxHex))
		if txHash == "" {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result has empty tx_hash")
		}
		if txHex == "" {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result has empty tx_hex")
		}
		if _, err := hex.DecodeString(txHex); err != nil {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result tx_hex is not valid hex")
		}
		if err := validateHashHex("tx_hash", txHash); err != nil {
			return FrostKeySignResponse{}, "", err
		}
		if expectedPlanHash == "" {
			return FrostKeySignResponse{}, "", fmt.Errorf("expected plan hash is empty")
		}
		eventuality := FrostEventuality{
			Version:       result.Eventuality.Version,
			PlanHash:      strings.ToLower(strings.TrimSpace(result.Eventuality.PlanHash)),
			TxHexHash:     strings.ToLower(strings.TrimSpace(result.Eventuality.TxHexHash)),
			EventualityID: strings.ToLower(strings.TrimSpace(result.Eventuality.EventualityID)),
		}
		if eventuality.Version != frostEventualityVersion {
			return FrostKeySignResponse{}, "", fmt.Errorf("invalid frost eventuality version")
		}
		if eventuality.PlanHash != expectedPlanHash {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result plan_hash mismatch")
		}
		if err := validateHashHex("tx_hex_hash", eventuality.TxHexHash); err != nil {
			return FrostKeySignResponse{}, "", err
		}
		if err := validateHashHex("eventuality_id", eventuality.EventualityID); err != nil {
			return FrostKeySignResponse{}, "", err
		}
		expectedTxHexHash, err := FrostTxHexHash(txHex)
		if err != nil {
			return FrostKeySignResponse{}, "", err
		}
		if eventuality.TxHexHash != strings.ToLower(expectedTxHexHash) {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result tx_hex_hash mismatch")
		}
		expectedEventualityID, err := FrostEventualityID(expectedPlanHash, txHash, expectedTxHexHash, result.FeeAtomic)
		if err != nil {
			return FrostKeySignResponse{}, "", err
		}
		if eventuality.EventualityID != strings.ToLower(expectedEventualityID) {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost success result eventuality_id mismatch")
		}
		normalizedRefs := make([]FrostSpentOutputRef, len(result.SpentOutputRefs))
		for i, ref := range result.SpentOutputRefs {
			normalizedRefs[i] = FrostSpentOutputRef{
				TxHash:      strings.ToLower(strings.TrimSpace(ref.TxHash)),
				OutputIndex: ref.OutputIndex,
				KeyImage:    strings.ToLower(strings.TrimSpace(ref.KeyImage)),
				SpendTxHash: strings.ToLower(strings.TrimSpace(ref.SpendTxHash)),
			}
		}
		normalized.MsgID = payloadMsgID
		normalized.TxHash = txHash
		normalized.TxHex = txHex
		normalized.Eventuality = eventuality
		normalized.SpentOutputRefs = normalizedRefs
	case tcommon.Fail:
		payloadMsgID := strings.TrimSpace(result.MsgID)
		if payloadMsgID != "" && expectedMsgID != "" && payloadMsgID != expectedMsgID {
			return FrostKeySignResponse{}, "", fmt.Errorf("frost fail result msg_id mismatch")
		}
		normalizedBlame, err := normalizeBlameForResult(result.Blame)
		if err != nil {
			return FrostKeySignResponse{}, "", err
		}
		normalized.Blame = normalizedBlame
	}

	keyBytes, err := json.Marshal(struct {
		Status          tcommon.Status        `json:"status"`
		MsgID           string                `json:"msg_id"`
		TxHash          string                `json:"tx_hash"`
		TxHex           string                `json:"tx_hex"`
		FeeAtomic       uint64                `json:"fee_atomic"`
		Eventuality     FrostEventuality      `json:"eventuality"`
		SpentOutputRefs []FrostSpentOutputRef `json:"spent_output_refs"`
		Blame           blame.Blame           `json:"blame"`
	}{
		Status:          normalized.Status,
		MsgID:           normalized.MsgID,
		TxHash:          normalized.TxHash,
		TxHex:           normalized.TxHex,
		FeeAtomic:       normalized.FeeAtomic,
		Eventuality:     normalized.Eventuality,
		SpentOutputRefs: normalized.SpentOutputRefs,
		Blame:           normalized.Blame,
	})
	if err != nil {
		return FrostKeySignResponse{}, "", fmt.Errorf("failed to encode frost result comparison key")
	}

	return normalized, string(keyBytes), nil
}

func normalizeBlameForResult(in blame.Blame) (blame.Blame, error) {
	reason := strings.TrimSpace(in.FailReason)
	if reason == "" {
		return blame.Blame{}, fmt.Errorf("frost fail result missing blame reason")
	}
	if len(in.BlameNodes) == 0 {
		return blame.Blame{}, fmt.Errorf("frost fail result missing blame nodes")
	}

	nodes := make([]blame.Node, 0, len(in.BlameNodes))
	seen := make(map[string]struct{}, len(in.BlameNodes))
	for _, node := range in.BlameNodes {
		pubKey := strings.TrimSpace(node.Pubkey)
		if pubKey == "" {
			return blame.Blame{}, fmt.Errorf("frost fail result contains empty blame node pubkey")
		}
		key := strings.Join(
			[]string{
				strings.ToLower(pubKey),
				hex.EncodeToString(node.BlameData),
				hex.EncodeToString(node.BlameSignature),
			},
			"|",
		)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		nodes = append(nodes, blame.Node{
			Pubkey:         pubKey,
			BlameData:      node.BlameData,
			BlameSignature: node.BlameSignature,
		})
	}
	if len(nodes) == 0 {
		return blame.Blame{}, fmt.Errorf("frost fail result missing blame nodes")
	}
	sort.Slice(nodes, func(i, j int) bool {
		iKey := strings.ToLower(nodes[i].Pubkey) + "|" + hex.EncodeToString(nodes[i].BlameData) + "|" + hex.EncodeToString(nodes[i].BlameSignature)
		jKey := strings.ToLower(nodes[j].Pubkey) + "|" + hex.EncodeToString(nodes[j].BlameData) + "|" + hex.EncodeToString(nodes[j].BlameSignature)
		return iKey < jKey
	})

	normalized := blame.NewBlame(reason, nodes)
	normalized.IsUnicast = in.IsUnicast
	normalized.Round = strings.TrimSpace(in.Round)
	return normalized, nil
}

type activeSignerSet struct {
	originalByNormalized map[string]string
}

func (a *activeSignerSet) missingPubKeys(seen map[string]struct{}) []string {
	missing := make([]string, 0)
	for norm, original := range a.originalByNormalized {
		if _, ok := seen[norm]; ok {
			continue
		}
		missing = append(missing, original)
	}
	sort.Strings(missing)
	return missing
}

func buildActiveSet(activeParticipants []string, known map[string]string) (*activeSignerSet, error) {
	set := make(map[string]string, len(activeParticipants))
	for _, participant := range activeParticipants {
		norm := normalizePubKey(participant)
		if norm == "" {
			continue
		}
		original, ok := known[norm]
		if !ok {
			return nil, fmt.Errorf("active participant %s is not in signer set", participant)
		}
		set[norm] = original
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("active signer set is empty")
	}
	return &activeSignerSet{originalByNormalized: set}, nil
}

func buildBlame(reason, round string, pubKeys []string) blame.Blame {
	nodes := make([]blame.Node, 0, len(pubKeys))
	seen := make(map[string]struct{}, len(pubKeys))
	for _, pubKey := range pubKeys {
		pubKey = strings.TrimSpace(pubKey)
		if pubKey == "" {
			continue
		}
		if _, ok := seen[pubKey]; ok {
			continue
		}
		seen[pubKey] = struct{}{}
		nodes = append(nodes, blame.NewNode(pubKey, nil, nil))
	}
	b := blame.NewBlame(reason, nodes)
	b.Round = round
	return b
}

func peerIDsToStrings(pIDs []peer.ID) []string {
	out := make([]string, 0, len(pIDs))
	for _, id := range pIDs {
		out = append(out, id.String())
	}
	return out
}

func buildParticipantIndex(members []string) ([]string, map[string]uint32, map[string]string, error) {
	originalByNormalized := make(map[string]string, len(members))
	normalized := make([]string, 0, len(members))
	for _, member := range members {
		norm := normalizePubKey(member)
		if norm == "" {
			continue
		}
		if _, exists := originalByNormalized[norm]; exists {
			return nil, nil, nil, fmt.Errorf("duplicate signer pubkey %s", member)
		}
		originalByNormalized[norm] = strings.TrimSpace(member)
		normalized = append(normalized, norm)
	}
	if len(normalized) == 0 {
		return nil, nil, nil, fmt.Errorf("signer pub keys are empty")
	}
	sort.Strings(normalized)
	indexMap := make(map[string]uint32, len(normalized))
	for i, norm := range normalized {
		indexMap[norm] = uint32(i + 1)
	}
	return normalized, indexMap, originalByNormalized, nil
}

// normalizePubKey canonicalizes pubkey strings for set membership checks.
func normalizePubKey(pubKey string) string {
	return strings.ToLower(strings.TrimSpace(pubKey))
}

// containsNormalizedPubKey reports whether pubKeys contains pubKey after the
// same trimming and case-folding used in FROST peer validation.
func containsNormalizedPubKey(pubKeys []string, pubKey string) bool {
	target := normalizePubKey(pubKey)
	if target == "" {
		return false
	}
	for _, candidate := range pubKeys {
		if normalizePubKey(candidate) == target {
			return true
		}
	}
	return false
}
