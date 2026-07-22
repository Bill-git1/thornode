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
	thorchaintypes "gitlab.com/thorchain/thornode/v3/x/thorchain/types"
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

func TestFrostKeygenExecuteRejectsPartialActiveSet(t *testing.T) {
	members := []string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}
	_, _, signerByNormalized, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("build participant index: %v", err)
	}
	activeSet, err := buildActiveSet([]string{testPubKeys[0], testPubKeys[1]}, signerByNormalized)
	if err != nil {
		t.Fatalf("build active set: %v", err)
	}

	sidecar := &mockFrostSidecarKeygen{}
	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", make(chan *p2p.Message, 1), time.Second)
	coord.sidecar = sidecar
	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "vault",
		SignerPubKeys: members,
	}, activeSet, nil)

	if resp.Status != tcommon.Fail {
		t.Fatalf("expected failure for partial active set, got %+v", resp)
	}
	if !strings.Contains(resp.Blame.FailReason, "requires all signers online") {
		t.Fatalf("unexpected failure reason: %q", resp.Blame.FailReason)
	}
	if len(sidecar.round1Reqs) != 0 {
		t.Fatalf("sidecar should not be called for partial active set")
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

func TestFrostKeygenRound3RejectsUncertifiedPayloadsOnTimeout(t *testing.T) {
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
	_, _, _, failResp := coord.exchangeRound3(activeSet, participantIndexMap, peerIDs, FrostDKGRound3Payload{
		SenderPubKey:         testPubKeys[0],
		ParticipantIndex:     participantIndexMap[normalizePubKey(testPubKeys[0])],
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
	})
	if failResp == nil {
		t.Fatal("expected uncertified round3 payloads to fail on timeout")
	}
	if failResp.Status != tcommon.Fail || failResp.Blame.Round != frostDKGRound3 {
		t.Fatalf("expected round3 failure, got %+v", failResp)
	}
}

func TestFrostDKGBirthdayUsesLowerMedianAndMargin(t *testing.T) {
	height, err := frostDKGBirthdayHeight(map[string]int64{
		"a": 1_000,
		"b": 1_001,
		"c": 1_002,
		"d": 50_000,
	}, 4)
	if err != nil {
		t.Fatalf("derive birthday: %v", err)
	}
	if height != 991 {
		t.Fatalf("expected lower median 1001 minus margin 10, got %d", height)
	}

	height, err = frostDKGBirthdayHeight(map[string]int64{
		"a": 0,
		"b": 1_000,
		"c": 1_001,
		"d": 1_002,
	}, 4)
	if err != nil {
		t.Fatalf("derive birthday with one abstention: %v", err)
	}
	if height != 991 {
		t.Fatalf("expected positive-height median 1001 minus margin 10, got %d", height)
	}

	if _, err := frostDKGBirthdayHeight(map[string]int64{
		"a": 0,
		"b": 0,
		"c": 1_000,
		"d": 1_001,
	}, 4); err == nil {
		t.Fatal("expected fewer than quorum positive heights to be rejected")
	}
	if _, err := frostDKGBirthdayHeight(map[string]int64{"a": -1}, 1); err == nil {
		t.Fatal("expected a negative height to be rejected")
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
	if _, failure := verifier.recordVerification(testPubKeys[1], targetPayload.SenderPubKey, targetPayload.ParticipantIndex, badHash); failure != nil {
		t.Fatalf("unsigned hash mismatch must not assign blame: %v", failure)
	}
	if verifier.isComplete() {
		t.Fatalf("mismatched verification quorum must not complete the transcript")
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
		SenderPubKey:         testPubKeys[0],
		ParticipantIndex:     localIndex,
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
	}
	remoteRound3 := FrostDKGRound3Payload{
		SenderPubKey:         testPubKeys[1],
		ParticipantIndex:     remoteIndex,
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 455,
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
	if resp.ObservedMoneroHeight != 445 {
		t.Fatalf("expected lower height 455 minus margin 10, got %d", resp.ObservedMoneroHeight)
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

// TestFrostKeygenExecuteBlamesAlmostCompleteFinisherOnLastRoundTimeout pins
// the bifrost half of the chainnet mass-jailing mechanism and its fix. The
// remote peer participates fully through rounds 1 and 2 — payloads and
// transcript verifications — and goes silent only in round 3, after the
// local sidecar has already derived the group key. The DKG was one peer
// message away from completing, and Execute still fails with a round-3
// timeout blame (unchanged: the coordinator cannot know the peers finished).
//
// The healing now happens one level up: when the peers' result broadcasts
// reach the on-chain supermajority threshold, AdoptQuorumSuccess proves the
// local keyshare against the adopted group key via the sidecar finalize and
// converts the failure into the same success the finishers report — same tss
// ID, so the node votes with the quorum instead of filing a permanently
// unreleasable failure (see
// TestXMRMonovaultKeygenFailureSubmitterStaysJailedAfterSuccessConsensus in
// x/thorchain for the on-chain half, which still needs the
// MajorityConsensusBlockHeight fix for reports landing after consensus).
// The tail of this test covers exactly that adoption.
func TestFrostKeygenExecuteBlamesAlmostCompleteFinisherOnLastRoundTimeout(t *testing.T) {
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

	// Rounds 1 and 2: the remote peer is fully present and verifiable.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id", testPubKeys[1], remoteRound1)
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
	// Round 3: nothing from the remote peer — its final message is a moment
	// too late for the local per-round timer.

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

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, 150*time.Millisecond)
	coord.sidecar = sidecar
	resp := coord.Execute(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, activeSet, activePeerIDs)

	// Current behavior: timeout failure blaming the almost-complete peer.
	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d (resp %+v)", resp.Status, resp)
	}
	if resp.Blame.Round != frostDKGRound3 {
		t.Fatalf("expected blame round %s, got %s", frostDKGRound3, resp.Blame.Round)
	}
	if len(resp.Blame.BlameNodes) != 1 || resp.Blame.BlameNodes[0].Pubkey != testPubKeys[1] {
		t.Fatalf("expected round3 timeout blame for %s, got %+v", testPubKeys[1], resp.Blame.BlameNodes)
	}

	// The local sidecar had already derived the group key: the DKG state was
	// one peer message away from done when the node gave up.
	if len(sidecar.round3Reqs) != 1 {
		t.Fatalf("expected local round3 sidecar call to have happened, got %d", len(sidecar.round3Reqs))
	}
	if len(sidecar.finalizeReqs) != 0 {
		t.Fatalf("expected no finalize call after timeout, got %d", len(sidecar.finalizeReqs))
	}

	// The fix: once the finishers' result broadcasts reach quorum, the node
	// adopts their success — the sidecar finalize proves the local session
	// derived exactly the adopted group key and hands back the keyshare, so
	// the node reports the same-ID success instead of the failure above.
	quorumResp := FrostKeygenResponse{
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
		Status:               tcommon.Success,
	}
	adopted, ok := coord.AdoptQuorumSuccess(FrostKeygenRequest{
		VaultPubKey:   "thorpub-vault",
		SignerPubKeys: members,
		ExpectedAddr:  groupAddress,
	}, quorumResp)
	if !ok {
		t.Fatalf("expected quorum success adoption after completed local DKG")
	}
	if adopted.Status != tcommon.Success {
		t.Fatalf("expected adopted success status, got %d", adopted.Status)
	}
	if adopted.Address != groupAddress || adopted.GroupKey != groupKey {
		t.Fatalf("adopted result must carry the quorum vault: %+v", adopted)
	}
	if adopted.ObservedMoneroHeight != 456 {
		t.Fatalf("adopted result must carry the quorum monero height, got %d", adopted.ObservedMoneroHeight)
	}
	if adopted.KeyBytesHex != "abcd" {
		t.Fatalf("adopted result must carry the locally finalized keyshare, got %q", adopted.KeyBytesHex)
	}
	if len(sidecar.finalizeReqs) != 1 {
		t.Fatalf("expected exactly one finalize call for the adoption proof, got %d", len(sidecar.finalizeReqs))
	}
	if sidecar.finalizeReqs[0].GroupKey != groupKey || sidecar.finalizeReqs[0].Address != groupAddress {
		t.Fatalf("adoption must finalize against the adopted vault: %+v", sidecar.finalizeReqs[0])
	}
}

// TestFrostKeygenAdoptQuorumSuccessRequiresKeyshareProof pins the safety of
// the adoption path: a node may only convert a quorum success into its own
// result when the sidecar finalize proves its DKG session derived exactly
// the adopted vault. Every failure of that proof keeps the local failure.
func TestFrostKeygenAdoptQuorumSuccessRequiresKeyshareProof(t *testing.T) {
	ensureBech32Prefix()

	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)
	req := FrostKeygenRequest{VaultPubKey: "thorpub-vault", ExpectedAddr: groupAddress}
	quorumResp := FrostKeygenResponse{
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
		Status:               tcommon.Success,
	}

	// No completed local DKG session: the sidecar rejects the finalize and the
	// adoption must be refused.
	sidecar := &mockFrostSidecarKeygen{finalizeErr: errors.New("DKG session keys not found")}
	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", make(chan *p2p.Message, 1), time.Second)
	coord.sidecar = sidecar
	if _, ok := coord.AdoptQuorumSuccess(req, quorumResp); ok {
		t.Fatalf("expected adoption refusal without a finalizable local session")
	}

	// Address not matching the expected vault address: refuse before ever
	// touching the sidecar.
	sidecar = &mockFrostSidecarKeygen{finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "abcd"}}
	coord.sidecar = sidecar
	mismatched := quorumResp
	mismatched.Address = "48otheraddress"
	if _, ok := coord.AdoptQuorumSuccess(req, mismatched); ok {
		t.Fatalf("expected adoption refusal on expected-address mismatch")
	}
	if len(sidecar.finalizeReqs) != 0 {
		t.Fatalf("expected no finalize call on address mismatch, got %d", len(sidecar.finalizeReqs))
	}

	// Finalize succeeding without key bytes cannot prove keyshare possession.
	sidecar = &mockFrostSidecarKeygen{finalizeResp: FrostSidecarDKGFinalizeResp{Success: true}}
	coord.sidecar = sidecar
	if _, ok := coord.AdoptQuorumSuccess(req, quorumResp); ok {
		t.Fatalf("expected adoption refusal on empty keyshare bytes")
	}

	// A quorum response without a positive monero height cannot build a valid
	// XMR keygen success message.
	sidecar = &mockFrostSidecarKeygen{finalizeResp: FrostSidecarDKGFinalizeResp{Success: true, KeyBytesHex: "abcd"}}
	coord.sidecar = sidecar
	noHeight := quorumResp
	noHeight.ObservedMoneroHeight = 0
	if _, ok := coord.AdoptQuorumSuccess(req, noHeight); ok {
		t.Fatalf("expected adoption refusal without observed monero height")
	}
}

// TestFrostKeygenAdoptionQuorumMatchesOnChainSupermajority keeps the bifrost
// adoption threshold aligned with x/thorchain/types.HasSuperMajority: adopt
// exactly when THORChain will finalize the vault. It asserts against the
// on-chain rule itself rather than hardcoded literals, so a change to
// SuperMajorityFactor is caught here instead of silently diverging from the
// bifrost copy.
func TestFrostKeygenAdoptionQuorumMatchesOnChainSupermajority(t *testing.T) {
	if got := frostKeygenAdoptionQuorum(0); got != 0 {
		t.Fatalf("adoption quorum for 0 participants: expected 0, got %d", got)
	}
	for total := 1; total <= 128; total++ {
		q := frostKeygenAdoptionQuorum(total)
		// q must be the exact on-chain supermajority threshold for `total`
		// members: q reaches it, q-1 does not.
		if !thorchaintypes.HasSuperMajority(q, total) {
			t.Fatalf("adoption quorum %d for %d participants is below the on-chain supermajority", q, total)
		}
		if thorchaintypes.HasSuperMajority(q-1, total) {
			t.Fatalf("adoption quorum %d for %d participants exceeds the on-chain supermajority (q-1 already reaches it)", q, total)
		}
	}
}

// TestFrostKeygenWaitForResultSurvivesInvalidResultMessages pins the result
// waiter's noise tolerance: the keygen msg ID is derivable by any connected
// peer, so a single malformed or unauthorized DKG result message arriving
// before the genuine quorum must be skipped, not end the wait — otherwise one
// peer could veto the quorum-success adoption and every non-active waiter.
// (This mirrors the keysign result waiter's behavior.)
func TestFrostKeygenWaitForResultSurvivesInvalidResultMessages(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}
	groupAddress := "48xmrvaultaddress"
	groupKey := strings.Repeat("ab", 32)
	success := FrostDKGResult{
		Address:              groupAddress,
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
		Status:               uint8(tcommon.Success),
	}

	msgChan := make(chan *p2p.Message, 16)

	// 1. Unparseable result payload.
	enqueueWrappedPayload(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], []byte("{not-json"))
	// 2. Empty sender pubkey.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], FrostDKGResultPayload{
		SenderPubKey: "",
		Response:     success,
	})
	// 3. Sender outside the active participant set.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[3], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[3],
		Response:     success,
	})
	// 4. Sender/peer binding mismatch: claims to be member 1 but arrives from
	// member 2's peer.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[2], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     success,
	})
	// 5. Structurally invalid result (success without an address).
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[1],
		Response: FrostDKGResult{
			GroupKey:             groupKey,
			ObservedMoneroHeight: 456,
			Status:               uint8(tcommon.Success),
		},
	})
	// The genuine quorum arrives after all the noise.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     success,
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[2], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     success,
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	resp, err := coord.WaitForResult(members, 2, 2*time.Second)
	if err != nil {
		t.Fatalf("expected quorum success despite invalid result noise, got error: %v", err)
	}
	if resp.Status != tcommon.Success {
		t.Fatalf("expected success status, got %d (blame %+v)", resp.Status, resp.Blame)
	}
	if resp.Address != groupAddress || resp.GroupKey != groupKey {
		t.Fatalf("unexpected adopted result: %+v", resp)
	}
}

// TestFrostKeygenWaitForResultLastVoteWinsPerSender pins the per-sender vote
// accounting: a sender re-voting with a different result moves between vote
// buckets, so one member cannot hold votes in two buckets at once (or count
// toward a quorum with a result it has since replaced).
func TestFrostKeygenWaitForResultLastVoteWinsPerSender(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}
	groupKey := strings.Repeat("ab", 32)
	resultX := FrostDKGResult{
		Address:              "48addressX",
		GroupKey:             groupKey,
		ObservedMoneroHeight: 456,
		Status:               uint8(tcommon.Success),
	}
	resultY := FrostDKGResult{
		Address:              "48addressY",
		GroupKey:             strings.Repeat("cd", 32),
		ObservedMoneroHeight: 456,
		Status:               uint8(tcommon.Success),
	}

	msgChan := make(chan *p2p.Message, 8)
	// Member 1 votes X, then replaces it with Y; member 2 votes X. No result
	// may reach quorum 2: X holds only member 2 after the replacement.
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     resultX,
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[1], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     resultY,
	})
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGResultMsg, "msg-id", testPubKeys[2], FrostDKGResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     resultX,
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	if _, err := coord.WaitForResult(members, 2, 500*time.Millisecond); err == nil {
		t.Fatalf("expected timeout: the replaced vote must not count toward quorum")
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
	localIndexKey := strconv.FormatUint(uint64(localIndex), 10)
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

	msgChan := make(chan *p2p.Message, 4)
	enqueueRoundMessage(t, msgChan, messages.TSSFrostDKGRound1Msg, "msg-id-single", testPubKeys[0], FrostDKGRound1Payload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: localIndex,
		Commitments:      "stale-self-commitment",
	})

	coord := newTestFrostKeygenCoordinator(testPubKeys[0], "msg-id-single", msgChan, time.Second)
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
	if resp.ObservedMoneroHeight != sidecar.chainHeight-frostDKGReorgMargin {
		t.Fatalf("expected single-signer birthday %d, got %d", sidecar.chainHeight-frostDKGReorgMargin, resp.ObservedMoneroHeight)
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
	if len(sidecar.round2Reqs[0].AllCommitments) != 1 || sidecar.round2Reqs[0].AllCommitments[localIndexKey] != "single-commitment" {
		t.Fatalf("expected only local commitment in single-signer round2, got %+v", sidecar.round2Reqs[0].AllCommitments)
	}
	if len(coord.p2pCommunication.BroadcastMsgChan) != 0 {
		t.Fatalf("expected single-signer keygen to skip p2p transcript exchange, broadcast %d messages", len(coord.p2pCommunication.BroadcastMsgChan))
	}
	if len(msgChan) != 1 {
		t.Fatalf("expected queued p2p noise to remain unread for single-signer keygen, got %d queued messages", len(msgChan))
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
