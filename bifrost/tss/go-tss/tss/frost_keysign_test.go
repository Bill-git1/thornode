package tss

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/blame"
	tcommon "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

var setupBech32PrefixOnce sync.Once

const testExpectedPlanHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func ensureBech32Prefix() {
	setupBech32PrefixOnce.Do(func() {
		conversion.SetupBech32Prefix()
	})
}

func testHashOf(ch rune) string {
	return strings.Repeat(string(ch), 64)
}

func mustSuccessResult(
	t *testing.T,
	msgID string,
	planHash string,
	txHash string,
	txHex string,
	feeAtomic uint64,
) FrostResult {
	t.Helper()
	eventuality, err := BuildFrostEventuality(planHash, txHash, txHex, feeAtomic)
	if err != nil {
		t.Fatalf("build eventuality: %v", err)
	}
	return FrostResult{
		MsgID:       msgID,
		TxHash:      txHash,
		TxHex:       txHex,
		FeeAtomic:   feeAtomic,
		Eventuality: eventuality,
		Status:      uint8(tcommon.Success),
	}
}

func TestFrostKeySignMsgIDDeterministic(t *testing.T) {
	base := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		ToAddress:     "48abc",
		AmountAtomic:  12345,
		Memo:          "OUT:1",
		SignerPubKeys: []string{"thorpub-c", "thorpub-a", "thorpub-b"},
		BlockHeight:   101,
	}

	id1, err := FrostKeySignMsgID(base)
	if err != nil {
		t.Fatalf("msg id 1: %v", err)
	}
	id2, err := FrostKeySignMsgID(base)
	if err != nil {
		t.Fatalf("msg id 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("expected deterministic msg id, got %q != %q", id1, id2)
	}

	reordered := base
	reordered.SignerPubKeys = []string{"thorpub-b", "thorpub-c", "thorpub-a"}
	id3, err := FrostKeySignMsgID(reordered)
	if err != nil {
		t.Fatalf("msg id reordered: %v", err)
	}
	if id1 != id3 {
		t.Fatalf("expected same msg id with reordered signers, got %q != %q", id1, id3)
	}

	activeChanged := base
	activeChanged.ActiveSignerPubKeys = []string{"thorpub-a", "thorpub-b"}
	id4, err := FrostKeySignMsgID(activeChanged)
	if err != nil {
		t.Fatalf("msg id active signers changed: %v", err)
	}
	if id1 != id4 {
		t.Fatalf("expected active signer changes not to affect msg id, got %q != %q", id1, id4)
	}
}

func TestFrostKeySignIdentityIncludesSpendConstraints(t *testing.T) {
	base := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		ToAddress:     "48abc",
		AmountAtomic:  12345,
		Memo:          "OUT:1",
		FeeRate:       10,
		SignerPubKeys: []string{"thorpub-c", "thorpub-a", "thorpub-b"},
		BlockHeight:   101,
		RequiredInputs: []FrostSpentOutputRef{
			{TxHash: "BBBB", OutputIndex: 1},
			{TxHash: "aaaa", OutputIndex: 0},
		},
		MaxInputs: 4,
	}

	baseID, err := FrostKeySignMsgID(base)
	if err != nil {
		t.Fatalf("base msg id: %v", err)
	}
	reordered := base
	reordered.RequiredInputs = []FrostSpentOutputRef{
		{TxHash: "aaaa", OutputIndex: 0},
		{TxHash: "BBBB", OutputIndex: 1},
	}
	reorderedID, err := FrostKeySignMsgID(reordered)
	if err != nil {
		t.Fatalf("reordered msg id: %v", err)
	}
	if baseID != reorderedID {
		t.Fatalf("expected same msg id for reordered required inputs, got %q != %q", baseID, reorderedID)
	}

	changedFee := base
	changedFee.FeeRate = 11
	feeID, err := FrostKeySignMsgID(changedFee)
	if err != nil {
		t.Fatalf("fee msg id: %v", err)
	}
	if baseID == feeID {
		t.Fatalf("expected fee rate to affect msg id")
	}

	changedRequired := base
	changedRequired.RequiredInputs = []FrostSpentOutputRef{
		{TxHash: "BBBB", OutputIndex: 2},
		{TxHash: "aaaa", OutputIndex: 0},
	}
	requiredID, err := FrostKeySignMsgID(changedRequired)
	if err != nil {
		t.Fatalf("required input msg id: %v", err)
	}
	if baseID == requiredID {
		t.Fatalf("expected required inputs to affect msg id")
	}

	changedMax := base
	changedMax.MaxInputs = 5
	maxID, err := FrostKeySignMsgID(changedMax)
	if err != nil {
		t.Fatalf("max inputs msg id: %v", err)
	}
	if baseID == maxID {
		t.Fatalf("expected max inputs to affect msg id")
	}

	changedSelectedValue := base
	changedSelectedValue.MaxSelectedInputValueAtomic = 99
	selectedValueID, err := FrostKeySignMsgID(changedSelectedValue)
	if err != nil {
		t.Fatalf("max selected input value msg id: %v", err)
	}
	if baseID == selectedValueID {
		t.Fatalf("expected max selected input value to affect msg id")
	}

	basePlan, err := FrostPlanHash(base, "fixed-msg-id")
	if err != nil {
		t.Fatalf("base plan hash: %v", err)
	}
	feePlan, err := FrostPlanHash(changedFee, "fixed-msg-id")
	if err != nil {
		t.Fatalf("fee plan hash: %v", err)
	}
	if basePlan == feePlan {
		t.Fatalf("expected fee rate to affect plan hash")
	}
	changedActive := base
	changedActive.ActiveSignerPubKeys = []string{"thorpub-a", "thorpub-b"}
	activePlan, err := FrostPlanHash(changedActive, "fixed-msg-id")
	if err != nil {
		t.Fatalf("active signer plan hash: %v", err)
	}
	if basePlan == activePlan {
		t.Fatalf("expected active signer changes to affect plan hash")
	}
	selectedValuePlan, err := FrostPlanHash(changedSelectedValue, "fixed-msg-id")
	if err != nil {
		t.Fatalf("max selected input value plan hash: %v", err)
	}
	if basePlan == selectedValuePlan {
		t.Fatalf("expected max selected input value to affect plan hash")
	}
}

func TestFrostKeySignMsgIDIncludesTxInHashRequestIDAndHeight(t *testing.T) {
	base := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		ToAddress:     "48abc",
		AmountAtomic:  12345,
		Memo:          "OUT:1",
		SignerPubKeys: []string{"thorpub-a", "thorpub-b", "thorpub-c"},
		BlockHeight:   101,
	}

	idHeightA, err := FrostKeySignMsgID(base)
	if err != nil {
		t.Fatalf("msg id height a: %v", err)
	}
	otherHeight := base
	otherHeight.BlockHeight = 102
	idHeightB, err := FrostKeySignMsgID(otherHeight)
	if err != nil {
		t.Fatalf("msg id height b: %v", err)
	}
	if idHeightA == idHeightB {
		t.Fatalf("expected different msg id for different exact heights, got %q", idHeightA)
	}

	withReqA := base
	withReqA.RequestID = "req-1"
	idReqA, err := FrostKeySignMsgID(withReqA)
	if err != nil {
		t.Fatalf("msg id req a: %v", err)
	}
	withReqB := base
	withReqB.RequestID = "req-2"
	idReqB, err := FrostKeySignMsgID(withReqB)
	if err != nil {
		t.Fatalf("msg id req b: %v", err)
	}
	if idReqA == idReqB {
		t.Fatalf("expected different msg id when request id changes, got %q", idReqA)
	}

	withTxIn := base
	withTxIn.RequestID = "req-ignored"
	withTxIn.TxInHash = "ABCDEF"
	idTxInA, err := FrostKeySignMsgID(withTxIn)
	if err != nil {
		t.Fatalf("msg id txin a: %v", err)
	}
	withTxIn.TxInHash = "123456"
	idTxInB, err := FrostKeySignMsgID(withTxIn)
	if err != nil {
		t.Fatalf("msg id txin b: %v", err)
	}
	if idTxInA == idTxInB {
		t.Fatalf("expected different msg id when txInHash changes, got %q", idTxInA)
	}

	sameTxInDifferentRequest := withTxIn
	sameTxInDifferentRequest.TxInHash = "ABCDEF"
	sameTxInDifferentRequest.RequestID = "req-2"
	idDifferentRequest, err := FrostKeySignMsgID(sameTxInDifferentRequest)
	if err != nil {
		t.Fatalf("msg id txin request b: %v", err)
	}
	if idTxInA == idDifferentRequest {
		t.Fatalf("expected different msg id when request id changes with txInHash present, got %q", idTxInA)
	}

	sameTxInDifferentHeight := withTxIn
	sameTxInDifferentHeight.TxInHash = "ABCDEF"
	sameTxInDifferentHeight.BlockHeight = 102
	idDifferentHeight, err := FrostKeySignMsgID(sameTxInDifferentHeight)
	if err != nil {
		t.Fatalf("msg id txin height b: %v", err)
	}
	if idTxInA == idDifferentHeight {
		t.Fatalf("expected different msg id when block height changes with txInHash present, got %q", idTxInA)
	}
}

func TestFrostDecoyContextDeterministic(t *testing.T) {
	ctxA, err := FrostDecoyContext("msg-a")
	if err != nil {
		t.Fatalf("decoy context a: %v", err)
	}
	ctxB, err := FrostDecoyContext("msg-a")
	if err != nil {
		t.Fatalf("decoy context b: %v", err)
	}
	if ctxA != ctxB {
		t.Fatalf("expected deterministic decoy context, got %q != %q", ctxA, ctxB)
	}
	ctxC, err := FrostDecoyContext("msg-b")
	if err != nil {
		t.Fatalf("decoy context c: %v", err)
	}
	if ctxA == ctxC {
		t.Fatalf("expected different decoy context for different msg ids, got %q", ctxA)
	}
}

func TestFrostExchangePreprocessAcceptsTranscriptVerification(t *testing.T) {
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

	localPayload := FrostPreprocessPayload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		PreprocessHex:    "aa",
	}
	remotePayload := FrostPreprocessPayload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		PreprocessHex:    "bb",
	}
	localHash, err := FrostTranscriptPreprocessHash("msg-id", localPayload)
	if err != nil {
		t.Fatalf("local hash: %v", err)
	}
	remoteHash, err := FrostTranscriptPreprocessHash("msg-id", remotePayload)
	if err != nil {
		t.Fatalf("remote hash: %v", err)
	}

	msgChan := make(chan *p2p.Message, 8)
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostPreprocessMsg, "msg-id", testPubKeys[1], remotePayload)
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostRoundPreprocess,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remotePayload.ParticipantIndex,
		PayloadHash:            remoteHash,
	})
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  frostRoundPreprocess,
		TargetSenderPubKey:     testPubKeys[0],
		TargetParticipantIndex: localPayload.ParticipantIndex,
		PayloadHash:            localHash,
	})

	coord := newTestFrostSignCoordinator(testPubKeys[0], "msg-id", msgChan, time.Second)
	values, failResp := coord.exchangePreprocess(activeSet, participantIndexMap, peerIDs, localPayload)
	if failResp != nil {
		t.Fatalf("expected success, got fail response: %+v", failResp)
	}
	if len(values) != 2 {
		t.Fatalf("expected two preprocess payloads, got %d", len(values))
	}
}

func TestFrostExchangePreprocessTimeoutMissingVerifications(t *testing.T) {
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
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostPreprocessMsg, "msg-id", testPubKeys[1], FrostPreprocessPayload{
		SenderPubKey:     testPubKeys[1],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[1])],
		PreprocessHex:    "bb",
	})

	coord := newTestFrostSignCoordinator(testPubKeys[0], "msg-id", msgChan, 30*time.Millisecond)
	_, failResp := coord.exchangePreprocess(activeSet, participantIndexMap, peerIDs, FrostPreprocessPayload{
		SenderPubKey:     testPubKeys[0],
		ParticipantIndex: participantIndexMap[normalizePubKey(testPubKeys[0])],
		PreprocessHex:    "aa",
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

func TestFrostWaitForResultRequiresQuorumMatch(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 2)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
	}

	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('a'), "deadbeef", 10),
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected quorum timeout, got nil error")
	}
}

func TestFrostWaitForResultTimeoutUsesSentinel(t *testing.T) {
	ensureBech32Prefix()

	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          make(chan *p2p.Message, 1),
		timeout:          100 * time.Millisecond,
	}

	_, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1]}, 1, 10*time.Millisecond)
	if !errors.Is(err, errFrostResultTimeout) {
		t.Fatalf("expected frost result timeout sentinel, got %v", err)
	}
}

func TestFrostWaitForResultSetupErrorIsNotTimeout(t *testing.T) {
	ensureBech32Prefix()

	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          make(chan *p2p.Message, 1),
		timeout:          100 * time.Millisecond,
	}

	_, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[0]}, 1, time.Second)
	if err == nil {
		t.Fatalf("expected setup error for duplicate active signer")
	}
	if errors.Is(err, errFrostResultTimeout) {
		t.Fatalf("setup error must not be classified as frost result timeout")
	}
}

func TestFrostWaitForResultAcceptsThresholdIdenticalSuccess(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 2)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	result := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('b'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     result,
	})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], testPubKeys[2], FrostResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     result,
	})

	resp, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.Status != tcommon.Success {
		t.Fatalf("expected success status, got %d", resp.Status)
	}
	if resp.TxHash != testHashOf('b') || resp.TxHex != "deadbeef" || resp.FeeAtomic != 10 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.MsgID != "msg-id" {
		t.Fatalf("expected msg_id propagation, got %q", resp.MsgID)
	}
}

func TestFrostWaitForResultRejectsSingleSuccessWithoutQuorum(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	result := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('1'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     result,
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected timeout waiting for success quorum, got nil error")
	}
}

func TestFrostWaitForResultRequiresFailureQuorum(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
	}

	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response: FrostResult{
			MsgID:  "msg-id",
			Status: uint8(tcommon.Fail),
			Blame: blame.NewBlame("single failure should not import", []blame.Node{
				blame.NewNode(testPubKeys[1], nil, nil),
			}),
		},
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected timeout waiting for failure quorum, got nil error")
	}
}

func TestFrostWaitForResultContinuesAfterSingleMalformedResult(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 3)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
		logger:           zerolog.Nop(),
	}

	malformed := mustSuccessResult(t, "msg-id", testHashOf('9'), testHashOf('7'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     malformed,
	})
	valid := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('8'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], testPubKeys[2], FrostResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     valid,
	})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[3], testPubKeys[3], FrostResultPayload{
		SenderPubKey: testPubKeys[3],
		Response:     valid,
	})

	resp, err := coord.WaitForResult([]string{testPubKeys[1], testPubKeys[2], testPubKeys[3]}, 2, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.Status != tcommon.Success {
		t.Fatalf("expected success status, got %d", resp.Status)
	}
	if resp.TxHash != testHashOf('8') {
		t.Fatalf("expected valid quorum result, got %+v", resp)
	}
}

func TestFrostWaitForResultRejectsConflictingSuccessPayloads(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 2)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
	}

	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('c'), "deadbeef", 10),
	})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], testPubKeys[2], FrostResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('d'), "deadbeef", 10),
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected quorum timeout for conflicting results, got nil error")
	}
}

func TestFrostWaitForResultSkipsPeerBindingMismatch(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
		logger:           zerolog.Nop(),
	}

	senderPubKey := testPubKeys[1]
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], senderPubKey, FrostResultPayload{
		SenderPubKey: senderPubKey,
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('e'), "deadbeef", 10),
	})

	_, err := coord.WaitForResult([]string{testPubKeys[0], senderPubKey, testPubKeys[2]}, 2, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout after skipping invalid message, got: %v", err)
	}
}

func TestFrostWaitForResultSkipsSenderOutsideActiveSet(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
		logger:           zerolog.Nop(),
	}

	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[3], testPubKeys[3], FrostResultPayload{
		SenderPubKey: testPubKeys[3],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('f'), "deadbeef", 10),
	})

	_, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout after skipping invalid message, got: %v", err)
	}
}

func TestFrostWaitForResultIgnoresMismatchedMsgID(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
	}

	enqueueResultMessage(t, msgChan, "different-msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('0'), "deadbeef", 10),
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1]}, 1, 150*time.Millisecond); err == nil {
		t.Fatalf("expected timeout due to msg id mismatch, got nil error")
	}
}

func TestFrostWaitForResultRejectsPlanHashMismatch(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	bad := mustSuccessResult(t, "msg-id", testHashOf('9'), testHashOf('7'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     bad,
	})

	resp, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1]}, 1, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", resp.Status)
	}
}

func TestFrostWaitForResultRejectsEventualityIDMismatch(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	bad := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('8'), "deadbeef", 10)
	bad.Eventuality.EventualityID = testHashOf('1')
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     bad,
	})

	resp, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1]}, 1, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", resp.Status)
	}
}

func TestFrostWaitForResultRejectsUnstructuredFailure(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response: FrostResult{
			Status: uint8(tcommon.Fail),
			Blame:  blame.Blame{},
		},
	})

	resp, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1]}, 1, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", resp.Status)
	}
}

func enqueueResultMessage(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgID string,
	peerPubKey string,
	senderPubKey string,
	payload FrostResultPayload,
) {
	t.Helper()
	peerID, err := conversion.GetPeerIDFromPubKey(peerPubKey)
	if err != nil {
		t.Fatalf("peer id from pubkey: %v", err)
	}
	if payload.SenderPubKey == "" {
		payload.SenderPubKey = senderPubKey
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	wrappedBytes, err := json.Marshal(messages.WrappedMessage{
		MessageType: messages.TSSFrostResultMsg,
		MsgID:       msgID,
		Payload:     payloadBytes,
	})
	if err != nil {
		t.Fatalf("marshal wrapped payload: %v", err)
	}
	msgChan <- &p2p.Message{
		PeerID:  peerID,
		Payload: wrappedBytes,
	}
}

func newTestFrostSignCoordinator(localPubKey, msgID string, msgChan chan *p2p.Message, timeout time.Duration) *FrostSignCoordinator {
	return &FrostSignCoordinator{
		p2pCommunication: &p2p.Communication{BroadcastMsgChan: make(chan *messages.BroadcastMsgChan, 64)},
		localPubKey:      localPubKey,
		msgID:            msgID,
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          timeout,
	}
}

func enqueueKeySignMessage(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgType messages.THORChainTSSMessageType,
	msgID string,
	peerPubKey string,
	payload interface{},
) {
	t.Helper()
	peerID, err := conversion.GetPeerIDFromPubKey(peerPubKey)
	if err != nil {
		t.Fatalf("peer id from pubkey: %v", err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	wrappedBytes, err := json.Marshal(messages.WrappedMessage{
		MessageType: msgType,
		MsgID:       msgID,
		Payload:     payloadBytes,
	})
	if err != nil {
		t.Fatalf("marshal wrapped payload: %v", err)
	}
	msgChan <- &p2p.Message{
		PeerID:  peerID,
		Payload: wrappedBytes,
	}
}
