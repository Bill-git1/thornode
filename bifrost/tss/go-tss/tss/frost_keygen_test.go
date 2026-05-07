package tss

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	tcommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

type mockFrostSidecarKeygen struct {
	round1Reqs   []FrostSidecarDKGRound1Req
	round2Reqs   []FrostSidecarDKGRound2Req
	round3Reqs   []FrostSidecarDKGRound3Req
	finalizeReqs []FrostSidecarDKGFinalizeReq

	round1Resp   FrostSidecarDKGRound1Resp
	round2Resp   FrostSidecarDKGRound2Resp
	round3Resp   FrostSidecarDKGRound3Resp
	finalizeResp FrostSidecarDKGFinalizeResp

	round1Err   error
	round2Err   error
	round3Err   error
	finalizeErr error
	chainHeight int64
	chainErr    error
}

func (m *mockFrostSidecarKeygen) FrostDKGRound1(req FrostSidecarDKGRound1Req) (FrostSidecarDKGRound1Resp, error) {
	m.round1Reqs = append(m.round1Reqs, req)
	if m.round1Err != nil {
		return FrostSidecarDKGRound1Resp{}, m.round1Err
	}
	return m.round1Resp, nil
}

func (m *mockFrostSidecarKeygen) FrostDKGRound2(req FrostSidecarDKGRound2Req) (FrostSidecarDKGRound2Resp, error) {
	m.round2Reqs = append(m.round2Reqs, req)
	if m.round2Err != nil {
		return FrostSidecarDKGRound2Resp{}, m.round2Err
	}
	return m.round2Resp, nil
}

func (m *mockFrostSidecarKeygen) FrostDKGRound3(req FrostSidecarDKGRound3Req) (FrostSidecarDKGRound3Resp, error) {
	m.round3Reqs = append(m.round3Reqs, req)
	if m.round3Err != nil {
		return FrostSidecarDKGRound3Resp{}, m.round3Err
	}
	return m.round3Resp, nil
}

func (m *mockFrostSidecarKeygen) FrostDKGFinalize(req FrostSidecarDKGFinalizeReq) (FrostSidecarDKGFinalizeResp, error) {
	m.finalizeReqs = append(m.finalizeReqs, req)
	if m.finalizeErr != nil {
		return FrostSidecarDKGFinalizeResp{}, m.finalizeErr
	}
	return m.finalizeResp, nil
}

func (m *mockFrostSidecarKeygen) GetChainHeight() (int64, error) {
	if m.chainErr != nil {
		return 0, m.chainErr
	}
	return m.chainHeight, nil
}

func newTestFrostKeygenCoordinator(localPubKey, msgID string, msgChan chan *p2p.Message, timeout time.Duration) *FrostKeygenCoordinator {
	return &FrostKeygenCoordinator{
		p2pCommunication: &p2p.Communication{BroadcastMsgChan: make(chan *messages.BroadcastMsgChan, 64)},
		localPubKey:      localPubKey,
		msgID:            msgID,
		msgChan:          msgChan,
		timeout:          timeout,
	}
}

func enqueueWrappedPayload(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgType messages.THORChainTSSMessageType,
	msgID string,
	peerPubKey string,
	payload []byte,
) {
	t.Helper()
	peerID, err := conversion.GetPeerIDFromPubKey(peerPubKey)
	if err != nil {
		t.Fatalf("peer id from pubkey: %v", err)
	}
	wrapped, err := json.Marshal(messages.WrappedMessage{
		MessageType: msgType,
		MsgID:       msgID,
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("marshal wrapped message: %v", err)
	}
	msgChan <- &p2p.Message{PeerID: peerID, Payload: wrapped}
}

func enqueueRoundMessage(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgType messages.THORChainTSSMessageType,
	msgID string,
	peerPubKey string,
	payload interface{},
) {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	enqueueWrappedPayload(t, msgChan, msgType, msgID, peerPubKey, payloadBytes)
}

func enqueueVerifyMessage(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgID string,
	peerPubKey string,
	payload FrostVerifyPayload,
) {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal verification payload: %v", err)
	}
	enqueueWrappedPayload(t, msgChan, messages.TSSFrostVerifyMsg, msgID, peerPubKey, payloadBytes)
}

func TestNewFrostKeygenCoordinatorClampsTimeoutToFloor(t *testing.T) {
	// Any non-zero timeout shorter than the floor must be clamped up.
	// This locks in the guarantee that a misconfigured (or zero-value)
	// FrostKeygenTimeout cannot starve the 3-round p2p DKG exchange.
	cases := []struct {
		name    string
		in      time.Duration
		wantMin time.Duration
	}{
		{name: "zero", in: 0, wantMin: minFrostKeygenTimeout},
		{name: "very short", in: 1 * time.Second, wantMin: minFrostKeygenTimeout},
		{name: "equal to floor", in: minFrostKeygenTimeout, wantMin: minFrostKeygenTimeout},
		{name: "above floor", in: 5 * time.Minute, wantMin: 5 * time.Minute},
	}
	msgChan := make(chan *p2p.Message, 1)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			coord := NewFrostKeygenCoordinator(nil, nil, "local", "msg-id", msgChan, tc.in)
			if coord.timeout < tc.wantMin {
				t.Fatalf("timeout not clamped: got %v, want >= %v", coord.timeout, tc.wantMin)
			}
			if tc.in >= minFrostKeygenTimeout && coord.timeout != tc.in {
				t.Fatalf("timeout above floor was modified: in=%v coord.timeout=%v", tc.in, coord.timeout)
			}
		})
	}
}

func TestFrostKeygenParticipantIndexDeterministic(t *testing.T) {
	membersA := []string{testPubKeys[2], testPubKeys[0], testPubKeys[1]}
	membersB := []string{testPubKeys[1], testPubKeys[2], testPubKeys[0]}

	_, idxA, _, err := buildParticipantIndex(membersA)
	if err != nil {
		t.Fatalf("build participant index A: %v", err)
	}
	_, idxB, _, err := buildParticipantIndex(membersB)
	if err != nil {
		t.Fatalf("build participant index B: %v", err)
	}

	for _, pubKey := range []string{testPubKeys[0], testPubKeys[1], testPubKeys[2]} {
		norm := normalizePubKey(pubKey)
		if idxA[norm] != idxB[norm] {
			t.Fatalf("participant index mismatch for %s: %d != %d", pubKey, idxA[norm], idxB[norm])
		}
	}
}

func TestFrostKeygenRejectsIncompleteParticipantSet(t *testing.T) {
	_, _, signerByNormalized, err := buildParticipantIndex([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]})
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet([]string{testPubKeys[0], testPubKeys[1]}, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}

	missing := missingSignersForFullParticipation(activeSet, signerByNormalized)
	if len(missing) != 1 || missing[0] != testPubKeys[2] {
		t.Fatalf("expected missing signer %s, got %+v", testPubKeys[2], missing)
	}
}

func TestFrostKeygenJoinThresholdRequiresFullParticipation(t *testing.T) {
	if got := frostKeygenJoinThreshold(4); got != 3 {
		t.Fatalf("expected full participation threshold for 4 signers to be 3, got %d", got)
	}
	if got := frostKeygenJoinThreshold(1); got != 0 {
		t.Fatalf("expected single-signer threshold to be 0, got %d", got)
	}
	if got := frostKeygenJoinThreshold(0); got != 0 {
		t.Fatalf("expected empty signer set threshold to be 0, got %d", got)
	}
}

func TestFrostKeygenRound1TimeoutReturnsBlame(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", make(chan *p2p.Message, 2), 30*time.Millisecond)
	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	})
	if failResp == nil {
		t.Fatalf("expected timeout failure response")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if failResp.Blame.Round != frostDKGRound1 {
		t.Fatalf("expected blame round %s, got %s", frostDKGRound1, failResp.Blame.Round)
	}
	if len(failResp.Blame.BlameNodes) != 1 || failResp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected missing signer blame for %s, got %+v", testPubKeys[1], failResp.Blame.BlameNodes)
	}
}

func TestFrostKeygenRound1RejectsMalformedPayload(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	msgChan := make(chan *p2p.Message, 2)
	enqueueWrappedPayload(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], []byte("{bad-json"))
	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)

	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	})
	if failResp == nil {
		t.Fatalf("expected malformed payload failure response")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if len(failResp.Blame.BlameNodes) != 1 || failResp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected malformed sender blame for %s, got %+v", testPubKeys[1], failResp.Blame.BlameNodes)
	}
}

func TestFrostKeygenRound1RejectsSenderBindingMismatch(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	msgChan := make(chan *p2p.Message, 2)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[2], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment",
	})
	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)

	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	})
	if failResp == nil {
		t.Fatalf("expected sender-binding failure response")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if len(failResp.Blame.BlameNodes) != 1 || failResp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected sender binding blame for %s, got %+v", testPubKeys[1], failResp.Blame.BlameNodes)
	}
}

func TestFrostKeygenRound3RejectsResultMismatch(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}
	msgChan := make(chan *p2p.Message, 2)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound3Msg, "msg-id", testPubKeys[1], FrostDKGRound3Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Address:          "48addr",
		GroupKey:         strings.Repeat("cd", 32),
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	_, _, _, failResp := coord.exchangeRound3(activeSet, participantIndexMap, peerIDs, FrostDKGRound3Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Address:          "48addr",
		GroupKey:         strings.Repeat("ab", 32),
	})
	if failResp == nil {
		t.Fatalf("expected mismatch failure response")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if len(failResp.Blame.BlameNodes) != 1 || failResp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected mismatch blame for %s, got %+v", testPubKeys[1], failResp.Blame.BlameNodes)
	}
}

func TestFrostKeygenRound3AcceptsMatchingPayloadsWithoutVerifications(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	groupAddress := "48addr"
	groupKey := strings.Repeat("ab", 32)
	msgChan := make(chan *p2p.Message, 2)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound3Msg, "msg-id", testPubKeys[1], FrostDKGRound3Payload{
		SenderPubKey:         testPubKeys[1],
		ParticipantIndex:     participantIndexMap[normalizePubKey(testPubKeys[1])],
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 123,
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, 30*time.Millisecond)
	address, gotGroupKey, birthday, failResp := coord.exchangeRound3(activeSet, participantIndexMap, peerIDs, FrostDKGRound3Payload{
		SenderPubKey:         testPubKeys[0],
		ParticipantIndex:     participantIndexMap[normalizePubKey(testPubKeys[0])],
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
	})
	if failResp != nil {
		t.Fatalf("expected round3 timeout fallback success, got %+v", failResp)
	}
	if address != groupAddress {
		t.Fatalf("expected address %s, got %s", groupAddress, address)
	}
	if gotGroupKey != groupKey {
		t.Fatalf("expected group key %s, got %s", groupKey, gotGroupKey)
	}
	if birthday != 123 {
		t.Fatalf("expected min birthday 123, got %d", birthday)
	}
}

func TestFrostKeygenRound1AcceptsTranscriptVerification(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	localPayload := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	}
	remotePayload := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment",
	}
	localHash, err := FrostTranscriptDKGRound1Hash("msg-id", localPayload)
	if err != nil {
		t.Fatalf("local hash: %v", err)
	}
	remoteHash, err := FrostTranscriptDKGRound1Hash("msg-id", remotePayload)
	if err != nil {
		t.Fatalf("remote hash: %v", err)
	}

	msgChan := make(chan *p2p.Message, 8)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], remotePayload)
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remotePayload.ParticipantIndex,
		PayloadHash:            remoteHash,
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localPayload.ParticipantIndex,
		PayloadHash:            localHash,
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	values, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, localPayload)
	if failResp != nil {
		t.Fatalf("expected success, got fail response: %+v", failResp)
	}
	if len(values) != 2 {
		t.Fatalf("expected two commitment payloads, got %d", len(values))
	}
}

func TestFrostKeygenRound1RejectsSenderEquivocation(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	msgChan := make(chan *p2p.Message, 8)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment-a",
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment-b",
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	})
	if failResp == nil {
		t.Fatalf("expected sender equivocation failure")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
}

func TestFrostKeygenRound1RejectsVerifierEquivocation(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	localPayload := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	}
	localHash, err := FrostTranscriptDKGRound1Hash("msg-id", localPayload)
	if err != nil {
		t.Fatalf("local hash: %v", err)
	}

	msgChan := make(chan *p2p.Message, 8)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment",
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localPayload.ParticipantIndex,
		PayloadHash:            localHash,
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localPayload.ParticipantIndex,
		PayloadHash:            strings.Repeat("f", 64),
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, localPayload)
	if failResp == nil {
		t.Fatalf("expected verifier equivocation failure")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
}

func TestFrostKeygenRound1TimeoutMissingVerifications(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	msgChan := make(chan *p2p.Message, 4)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment",
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, 30*time.Millisecond)
	_, failResp := coord.exchangeRound1(activeSet, participantIndexMap, peerIDs, FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		Commitments:      "local-commitment",
	})
	if failResp == nil {
		t.Fatalf("expected timeout failure")
	}
	if failResp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", failResp.Status)
	}
	if len(failResp.Blame.BlameNodes) != 1 || failResp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected missing verifier blame for %s, got %+v", testPubKeys[1], failResp.Blame.BlameNodes)
	}
}

func TestFrostTranscriptVerifierRejectsUnanimousHashMismatch(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	verifier, err := newFrostTranscriptVerifier(frostDKGRound1, activeSet, participantIndexMap)
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}

	targetPayload := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		Commitments:      "remote-commitment",
	}
	targetHash, err := FrostTranscriptDKGRound1Hash("msg-id", targetPayload)
	if err != nil {
		t.Fatalf("target hash: %v", err)
	}
	if _, failure := verifier.recordPayload(targetPayload.SenderPubKey, targetPayload.ParticipantIndex, targetHash); failure != nil {
		t.Fatalf("record payload failed: %v", failure)
	}

	badHash := strings.Repeat("f", 64)
	if _, failure := verifier.recordVerification(testPubKeys[0], targetPayload.SenderPubKey, targetPayload.ParticipantIndex, badHash); failure != nil {
		t.Fatalf("first mismatched verification should not fail before quorum: %v", failure)
	}
	if _, failure := verifier.recordVerification(testPubKeys[1], targetPayload.SenderPubKey, targetPayload.ParticipantIndex, badHash); failure == nil {
		t.Fatalf("expected unanimous hash mismatch failure")
	}
}

func TestFrostTranscriptDKGRound2HashDeterministicAcrossMapOrder(t *testing.T) {
	payloadA := FrostDKGRound2Payload{
		SenderPubKey:     "sender",
		ParticipantIndex: 1,
		EncryptedShares: map[string]string{
			"2": "value-b",
			"1": "value-a",
			"3": "value-c",
		},
	}
	payloadB := FrostDKGRound2Payload{
		SenderPubKey:     "sender",
		ParticipantIndex: 1,
		EncryptedShares: map[string]string{
			"3": "value-c",
			"1": "value-a",
			"2": "value-b",
		},
	}
	hashA, err := FrostTranscriptDKGRound2Hash("msg-id", payloadA)
	if err != nil {
		t.Fatalf("hashA: %v", err)
	}
	hashB, err := FrostTranscriptDKGRound2Hash("msg-id", payloadB)
	if err != nil {
		t.Fatalf("hashB: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected deterministic map-order hash, got %s vs %s", hashA, hashB)
	}
}

func TestFrostTranscriptDKGRound2HashAllowsEmptyShares(t *testing.T) {
	payload := FrostDKGRound2Payload{
		SenderPubKey:     "sender",
		ParticipantIndex: 1,
		EncryptedShares:  map[string]string{},
	}

	hashA, err := FrostTranscriptDKGRound2Hash("msg-id", payload)
	if err != nil {
		t.Fatalf("hashA: %v", err)
	}
	hashB, err := FrostTranscriptDKGRound2Hash("msg-id", payload)
	if err != nil {
		t.Fatalf("hashB: %v", err)
	}
	if hashA != hashB {
		t.Fatalf("expected deterministic empty-share hash, got %s vs %s", hashA, hashB)
	}
}

func TestFrostKeygenExecuteSuccess(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	activePeerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	localIndex := participantIndexMap[normalizePubKey(testPubKeys[0])]
	remoteIndex := participantIndexMap[normalizePubKey(testPubKeys[1])]
	localIndexKey := strconv.FormatUint(uint64(localIndex), 10)
	remoteIndexKey := strconv.FormatUint(uint64(remoteIndex), 10)

	msgChan := make(chan *p2p.Message, 32)
	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)
	localRound1 := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: localIndex,
		Commitments:      "local-commitment",
	}
	remoteRound1 := FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: remoteIndex,
		Commitments:      "remote-commitment",
	}
	localRound1Hash, err := FrostTranscriptDKGRound1Hash("msg-id", localRound1)
	if err != nil {
		t.Fatalf("local round1 hash: %v", err)
	}
	remoteRound1Hash, err := FrostTranscriptDKGRound1Hash("msg-id", remoteRound1)
	if err != nil {
		t.Fatalf("remote round1 hash: %v", err)
	}

	localRound2 := FrostDKGRound2Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: localIndex,
		EncryptedShares: map[string]string{
			remoteIndexKey: "local-to-remote",
		},
	}
	remoteRound2 := FrostDKGRound2Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: remoteIndex,
		EncryptedShares: map[string]string{
			localIndexKey: "remote-to-local",
		},
	}
	localRound2Hash, err := FrostTranscriptDKGRound2Hash("msg-id", localRound2)
	if err != nil {
		t.Fatalf("local round2 hash: %v", err)
	}
	remoteRound2Hash, err := FrostTranscriptDKGRound2Hash("msg-id", remoteRound2)
	if err != nil {
		t.Fatalf("remote round2 hash: %v", err)
	}

	localRound3 := FrostDKGRound3Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: localIndex,
		Address:          groupAddress,
		GroupKey:         groupKey,
	}
	remoteRound3 := FrostDKGRound3Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: remoteIndex,
		Address:          groupAddress,
		GroupKey:         groupKey,
	}
	localRound3Hash, err := FrostTranscriptDKGRound3Hash("msg-id", localRound3)
	if err != nil {
		t.Fatalf("local round3 hash: %v", err)
	}
	remoteRound3Hash, err := FrostTranscriptDKGRound3Hash("msg-id", remoteRound3)
	if err != nil {
		t.Fatalf("remote round3 hash: %v", err)
	}

	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: remoteIndex,
		Commitments:      "remote-commitment",
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remoteIndex,
		PayloadHash:            remoteRound1Hash,
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound1,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localIndex,
		PayloadHash:            localRound1Hash,
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound2Msg, "msg-id", testPubKeys[1], remoteRound2)
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound2,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remoteIndex,
		PayloadHash:            remoteRound2Hash,
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound2,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localIndex,
		PayloadHash:            localRound2Hash,
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound3Msg, "msg-id", testPubKeys[1], remoteRound3)
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound3,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remoteIndex,
		PayloadHash:            remoteRound3Hash,
	})
	enqueueVerifyMessage(t, msgChan, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostDKGRound3,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localIndex,
		PayloadHash:            localRound3Hash,
	})

	sidecar := &mockFrostSidecarKeygen{
		round1Resp: FrostSidecarDKGRound1Resp{Commitments: "local-commitment"},
		round2Resp: FrostSidecarDKGRound2Resp{EncryptedShares: localRound2.EncryptedShares},
		round3Resp: FrostSidecarDKGRound3Resp{
			Address:  groupAddress,
			GroupKey: groupKey,
		},
		finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "abcd"},
		chainHeight:  456,
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	coord.sidecar = sidecar
	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, activePeerIDs)
	if resp.Status != tcommon.Success {
		t.Fatalf("expected success status, got %d with blame %+v", resp.Status, resp.Blame)
	}
	if resp.Address != groupAddress || resp.GroupKey != groupKey {
		t.Fatalf("unexpected keygen response: %+v", resp)
	}

	if len(sidecar.round1Reqs) != 1 {
		t.Fatalf("expected one round1 call, got %d", len(sidecar.round1Reqs))
	}
	if sidecar.round1Reqs[0].ParticipantIndex != localIndex {
		t.Fatalf("expected local participant index %d, got %d", localIndex, sidecar.round1Reqs[0].ParticipantIndex)
	}
	if sidecar.round1Reqs[0].N != 2 || sidecar.round1Reqs[0].Threshold != 2 {
		t.Fatalf("unexpected round1 N/threshold: %+v", sidecar.round1Reqs[0])
	}
	if sidecar.round1Reqs[0].VaultPubKey != "thorpub-vault" {
		t.Fatalf("unexpected round1 vault pubkey: %+v", sidecar.round1Reqs[0])
	}
	if len(sidecar.finalizeReqs) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(sidecar.finalizeReqs))
	}
	if len(sidecar.round3Reqs) != 1 {
		t.Fatalf("expected one round3 call, got %d", len(sidecar.round3Reqs))
	}
	if len(sidecar.round3Reqs[0].AllShares) != 1 || sidecar.round3Reqs[0].AllShares[remoteIndexKey] != "remote-to-local" {
		t.Fatalf("unexpected round3 shares: %+v", sidecar.round3Reqs[0].AllShares)
	}
	if sidecar.finalizeReqs[0].SessionID != "msg-id" {
		t.Fatalf("unexpected finalize session id: %+v", sidecar.finalizeReqs[0])
	}
	if sidecar.finalizeReqs[0].GroupKey != groupKey || sidecar.finalizeReqs[0].Address != groupAddress {
		t.Fatalf("unexpected finalize payload: %+v", sidecar.finalizeReqs[0])
	}
}

func TestFrostKeygenExecuteSuccessSingleSigner(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}
	activePeerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	localIndex := participantIndexMap[normalizePubKey(testPubKeys[0])]
	groupAddress := "48xmrsinglesigner"
	groupKey := strings.Repeat("cd", 32)

	sidecar := &mockFrostSidecarKeygen{
		round1Resp: FrostSidecarDKGRound1Resp{Commitments: "single-commitment"},
		round2Resp: FrostSidecarDKGRound2Resp{EncryptedShares: map[string]string{}},
		round3Resp: FrostSidecarDKGRound3Resp{
			Address:  groupAddress,
			GroupKey: groupKey,
		},
		finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "abcd"},
		chainHeight:  123,
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id-single", make(chan *p2p.Message, 4), time.Second)
	coord.sidecar = sidecar
	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-single",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, activePeerIDs)
	if resp.Status != tcommon.Success {
		t.Fatalf("expected success status, got %d with blame %+v", resp.Status, resp.Blame)
	}
	if resp.Address != groupAddress || resp.GroupKey != groupKey {
		t.Fatalf("unexpected single-signer keygen response: %+v", resp)
	}
	if len(sidecar.round1Reqs) != 1 {
		t.Fatalf("expected one round1 call, got %d", len(sidecar.round1Reqs))
	}
	if sidecar.round1Reqs[0].ParticipantIndex != localIndex {
		t.Fatalf("expected local participant index %d, got %d", localIndex, sidecar.round1Reqs[0].ParticipantIndex)
	}
	if sidecar.round1Reqs[0].N != 1 || sidecar.round1Reqs[0].Threshold != 1 {
		t.Fatalf("unexpected single-signer round1 N/threshold: %+v", sidecar.round1Reqs[0])
	}
	if len(sidecar.round2Reqs) != 1 {
		t.Fatalf("expected one round2 call, got %d", len(sidecar.round2Reqs))
	}
	if len(sidecar.round2Reqs[0].AllCommitments) != 1 {
		t.Fatalf("expected only local commitment in single-signer round2, got %+v", sidecar.round2Reqs[0].AllCommitments)
	}
	if len(sidecar.round3Reqs) != 1 {
		t.Fatalf("expected one round3 call, got %d", len(sidecar.round3Reqs))
	}
	if len(sidecar.round3Reqs[0].AllShares) != 0 {
		t.Fatalf("expected no remote shares in single-signer round3, got %+v", sidecar.round3Reqs[0].AllShares)
	}
	if len(sidecar.finalizeReqs) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(sidecar.finalizeReqs))
	}
}

func TestFrostKeygenExecuteFailsWhenFinalizeUnsuccessful(t *testing.T) {
	members := []string{testPubKeys[0]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}

	localIndex := participantIndexMap[normalizePubKey(testPubKeys[0])]
	localIndexKey := strconv.FormatUint(uint64(localIndex), 10)
	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)

	sidecar := &mockFrostSidecarKeygen{
		round1Resp: FrostSidecarDKGRound1Resp{Commitments: "local-commitment"},
		round2Resp: FrostSidecarDKGRound2Resp{
			EncryptedShares: map[string]string{
				localIndexKey: "local-share",
			},
		},
		round3Resp: FrostSidecarDKGRound3Resp{
			Address:  groupAddress,
			GroupKey: groupKey,
		},
		finalizeResp: FrostSidecarDKGFinalizeResp{Success: false},
		chainHeight:  123,
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id-finalize-fail", make(chan *p2p.Message, 4), time.Second)
	coord.sidecar = sidecar

	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, nil)

	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status when finalize is unsuccessful, got %d", resp.Status)
	}
	if len(sidecar.finalizeReqs) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(sidecar.finalizeReqs))
	}
}

func TestFrostKeygenExecuteFailsWithoutPositiveMoneroHeight(t *testing.T) {
	members := []string{testPubKeys[0]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}

	localIndex := participantIndexMap[normalizePubKey(testPubKeys[0])]
	localIndexKey := strconv.FormatUint(uint64(localIndex), 10)
	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)

	sidecar := &mockFrostSidecarKeygen{
		round1Resp: FrostSidecarDKGRound1Resp{Commitments: "local-commitment"},
		round2Resp: FrostSidecarDKGRound2Resp{
			EncryptedShares: map[string]string{
				localIndexKey: "local-share",
			},
		},
		round3Resp: FrostSidecarDKGRound3Resp{
			Address:  groupAddress,
			GroupKey: groupKey,
		},
		finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "abcd"},
		chainErr:     errors.New("height unavailable"),
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id-height-fail", make(chan *p2p.Message, 4), time.Second)
	coord.sidecar = sidecar

	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, nil)

	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status without positive height, got %d", resp.Status)
	}
	if len(sidecar.finalizeReqs) != 0 {
		t.Fatalf("expected finalize not to run without positive height, got %d calls", len(sidecar.finalizeReqs))
	}
}

func TestFrostKeygenExecuteFailsWithInvalidFinalizeKeyBytes(t *testing.T) {
	members := []string{testPubKeys[0]}
	_, participantIndexMap, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet(members, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}

	localIndex := participantIndexMap[normalizePubKey(testPubKeys[0])]
	localIndexKey := strconv.FormatUint(uint64(localIndex), 10)
	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)

	sidecar := &mockFrostSidecarKeygen{
		round1Resp: FrostSidecarDKGRound1Resp{Commitments: "local-commitment"},
		round2Resp: FrostSidecarDKGRound2Resp{
			EncryptedShares: map[string]string{
				localIndexKey: "local-share",
			},
		},
		round3Resp: FrostSidecarDKGRound3Resp{
			Address:  groupAddress,
			GroupKey: groupKey,
		},
		finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "not-hex"},
		chainHeight:  123,
	}

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id-keybytes-fail", make(chan *p2p.Message, 4), time.Second)
	coord.sidecar = sidecar

	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, nil)

	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status for invalid key bytes, got %d", resp.Status)
	}
	if len(sidecar.finalizeReqs) != 1 {
		t.Fatalf("expected one finalize call, got %d", len(sidecar.finalizeReqs))
	}
}
