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

const (
	testExpectedPlanHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testTxKey            = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

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
	eventuality, err := BuildFrostEventuality(planHash, txHash, txHex, testTxKey, feeAtomic)
	if err != nil {
		t.Fatalf("build eventuality: %v", err)
	}
	return FrostResult{
		MsgID:       msgID,
		TxHash:      txHash,
		TxHex:       txHex,
		TxKey:       testTxKey,
		FeeAtomic:   feeAtomic,
		Eventuality: eventuality,
		SpentOutputRefs: []FrostSpentOutputRef{{
			TxHash:      testHashOf('c'),
			OutputIndex: 1,
			KeyImage:    testHashOf('d'),
			SpendTxHash: txHash,
		}},
		Status: uint8(tcommon.Success),
	}
}

func TestFrostSigningClaimHooksAreLocalAndFailClosed(t *testing.T) {
	req := FrostKeySignRequest{
		VaultPubKey:   "vault-a",
		ToAddress:     "destination-a",
		AmountAtomic:  10,
		SignerPubKeys: []string{testPubKeys[0], testPubKeys[1]},
	}
	msgIDBefore, err := FrostKeySignMsgID(req)
	if err != nil {
		t.Fatalf("derive msg id: %v", err)
	}
	calls := 0
	req.ReserveSigningClaim = func() error { calls++; return nil }
	if err = reserveFrostSigningClaim(req); err != nil || calls != 1 {
		t.Fatalf("reserve hook calls=%d err=%v", calls, err)
	}
	msgIDAfter, err := FrostKeySignMsgID(req)
	if err != nil || msgIDAfter != msgIDBefore {
		t.Fatalf("local hook changed canonical request: before=%s after=%s err=%v", msgIDBefore, msgIDAfter, err)
	}
	reserveErr := errors.New("claim store unavailable")
	req.ReserveSigningClaim = func() error { return reserveErr }
	if err = reserveFrostSigningClaim(req); !errors.Is(err, reserveErr) {
		t.Fatalf("expected reserve failure to propagate, got %v", err)
	}
}

func TestFrostTxKeyCanonicalValidation(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	if _, err := FrostTxKeyHash(valid); err != nil {
		t.Fatalf("valid tx key rejected: %v", err)
	}
	for _, invalid := range []string{
		strings.ToUpper(valid),
		" " + valid,
		valid + " ",
		strings.Repeat("0", 64),
		valid[:62],
	} {
		if _, err := FrostTxKeyHash(invalid); err == nil {
			t.Fatalf("invalid tx key accepted: %q", invalid)
		}
	}
}

func TestFrostSignCoordinatorReleasesClaimOnlyBeforeShareRPC(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0]}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}
	req := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		ToAddress:     "48abc",
		AmountAtomic:  1,
		SignerPubKeys: members,
	}
	newCoordinator := func(sidecar FrostSidecarClient) *FrostSignCoordinator {
		return &FrostSignCoordinator{
			p2pCommunication: &p2p.Communication{BroadcastMsgChan: make(chan *messages.BroadcastMsgChan, 16)},
			sidecar:          sidecar,
			localPubKey:      members[0],
			msgID:            strings.Repeat("a", 64),
			msgChan:          make(chan *p2p.Message, 1),
			timeout:          time.Second,
			logger:           zerolog.Nop(),
		}
	}
	beforeShare := newCoordinator(&mockFrostSidecarSign{prepareErr: errors.New("prepare unavailable")}).Execute(req, members, peerIDs)
	if beforeShare.Status != tcommon.Fail || !beforeShare.releaseSigningClaim {
		t.Fatalf("pre-share failure must release exact reservation: %+v", beforeShare)
	}
	afterShareStarted := newCoordinator(&mockFrostSidecarSign{
		prepareResp: FrostSidecarPrepareResp{SessionID: "session", Preprocess: "aa"},
		partialErr:  errors.New("ambiguous partial response"),
	}).Execute(req, members, peerIDs)
	if afterShareStarted.Status != tcommon.Fail || afterShareStarted.releaseSigningClaim {
		t.Fatalf("partial RPC ambiguity must retain reservation: %+v", afterShareStarted)
	}
	if afterShareStarted.Blame.Round != messages.FROSTKEYSIGNSHARE {
		t.Fatalf("partial RPC ambiguity must be terminal at %s, got %q", messages.FROSTKEYSIGNSHARE, afterShareStarted.Blame.Round)
	}
	afterSharesExchanged := newCoordinator(&mockFrostSidecarSign{
		prepareResp: FrostSidecarPrepareResp{SessionID: "session", Preprocess: "aa"},
		partialResp: FrostSidecarPartialResp{SignatureShare: "cc"},
		combineErr:  errors.New("combine response lost"),
	}).Execute(req, members, peerIDs)
	if afterSharesExchanged.Status != tcommon.Fail || afterSharesExchanged.releaseSigningClaim {
		t.Fatalf("combine RPC ambiguity must retain reservation: %+v", afterSharesExchanged)
	}
	if afterSharesExchanged.Blame.Round != messages.FROSTKEYSIGNRESULT {
		t.Fatalf("combine RPC ambiguity must be terminal at %s, got %q", messages.FROSTKEYSIGNRESULT, afterSharesExchanged.Blame.Round)
	}
}

// preserveFrostExposureRound expresses the pre-rename helper's contract in terms
// of the current shouldAdoptFrostFailureResult predicate: adopt the quorum
// candidate only when it does not erase a local signature-exposure round.
func preserveFrostExposureRound(local, candidate FrostKeySignResponse) FrostKeySignResponse {
	if shouldAdoptFrostFailureResult(local, candidate) {
		return candidate
	}
	return local
}

func TestPreserveFrostExposureRoundAcrossQuorumFailure(t *testing.T) {
	local := FrostKeySignResponse{
		Status: tcommon.Fail,
		Blame:  buildBlame("partial response lost", messages.FROSTKEYSIGNSHARE, nil),
	}
	peerAttributed := FrostKeySignResponse{
		Status: tcommon.Fail,
		Blame:  buildBlame("peer failed earlier", messages.FROSTKEYSIGNPREPROCESS, []string{testPubKeys[1]}),
	}
	got := preserveFrostExposureRound(local, peerAttributed)
	if got.Blame.Round != messages.FROSTKEYSIGNSHARE {
		t.Fatalf("quorum failure erased local share exposure: got %q", got.Blame.Round)
	}
	if len(got.Blame.BlameNodes) != 0 {
		t.Fatalf("must not graft preprocess blame onto a local terminal round, got %+v", got.Blame.BlameNodes)
	}
	if got.Blame.FailReason != local.Blame.FailReason {
		t.Fatalf("expected local terminal failure reason, got %q", got.Blame.FailReason)
	}

	peerResult := peerAttributed
	peerResult.Blame.Round = messages.FROSTKEYSIGNRESULT
	got = preserveFrostExposureRound(local, peerResult)
	if got.Blame.Round != messages.FROSTKEYSIGNRESULT {
		t.Fatalf("must preserve an existing terminal quorum result round, got %q", got.Blame.Round)
	}
}

type mockFrostSidecarSign struct {
	prepareResp FrostSidecarPrepareResp
	partialResp FrostSidecarPartialResp
	combineResp FrostSidecarCombineResp
	prepareErr  error
	partialErr  error
	combineErr  error
}

func (m *mockFrostSidecarSign) FrostSignPrepare(req FrostSidecarPrepareReq) (FrostSidecarPrepareResp, error) {
	return m.prepareResp, m.prepareErr
}

func (m *mockFrostSidecarSign) FrostSignPartial(req FrostSidecarPartialReq) (FrostSidecarPartialResp, error) {
	return m.partialResp, m.partialErr
}

func (m *mockFrostSidecarSign) FrostSignCombine(req FrostSidecarCombineReq) (FrostSidecarCombineResp, error) {
	return m.combineResp, m.combineErr
}

func TestFrostKeySignMsgIDDeterministic(t *testing.T) {
	base := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		Network:       "mainnet",
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

	otherNetwork := base
	otherNetwork.Network = "stagenet"
	otherNetworkID, err := FrostKeySignMsgID(otherNetwork)
	if err != nil {
		t.Fatalf("msg id other network: %v", err)
	}
	if id1 == otherNetworkID {
		t.Fatal("deployment network must affect FROST session identity")
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

	// MaxGas must be part of the request identity because it changes the fee
	// ceiling and migration sweep amount the sidecar is allowed to construct.
	changedMaxGas := base
	changedMaxGas.MaxGasAtomic = 123
	maxGasID, err := FrostKeySignMsgID(changedMaxGas)
	if err != nil {
		t.Fatalf("max gas msg id: %v", err)
	}
	if baseID == maxGasID {
		t.Fatalf("expected max gas to affect msg id")
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
	maxGasPlan, err := FrostPlanHash(changedMaxGas, "fixed-msg-id")
	if err != nil {
		t.Fatalf("max gas plan hash: %v", err)
	}
	if basePlan == maxGasPlan {
		t.Fatalf("expected max gas to affect plan hash")
	}
}

func TestFrostKeySignIdentityBindsDustThreshold(t *testing.T) {
	base := FrostKeySignRequest{
		VaultPubKey:   "vault",
		ToAddress:     "48destination",
		AmountAtomic:  100,
		SignerPubKeys: []string{"a", "b"},
		DustThreshold: 10,
	}
	firstID, err := FrostKeySignMsgID(base)
	if err != nil {
		t.Fatalf("first msg id: %v", err)
	}
	firstPlan, err := FrostPlanHash(base, firstID)
	if err != nil {
		t.Fatalf("first plan: %v", err)
	}
	changed := base
	changed.DustThreshold++
	secondID, err := FrostKeySignMsgID(changed)
	if err != nil {
		t.Fatalf("second msg id: %v", err)
	}
	secondPlan, err := FrostPlanHash(changed, secondID)
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if firstID == secondID || firstPlan == secondPlan {
		t.Fatalf("dust threshold must change both commitments")
	}
}

func TestValidateFrostPreparedInputs(t *testing.T) {
	txHash := testHashOf('f')
	selected := []FrostSpentOutputRef{
		{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('c')},
		{TxHash: testHashOf('b'), OutputIndex: 2},
	}
	spent := []FrostSpentOutputRef{
		{TxHash: testHashOf('b'), OutputIndex: 2, KeyImage: testHashOf('d'), SpendTxHash: txHash},
		{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: txHash},
	}

	if err := validateFrostPreparedInputs(selected, spent, txHash); err != nil {
		t.Fatalf("valid reordered inputs rejected: %v", err)
	}

	tests := []struct {
		name     string
		selected []FrostSpentOutputRef
		spent    []FrostSpentOutputRef
		txHash   string
	}{
		{name: "empty prepared set", spent: spent, txHash: txHash},
		{name: "missing signed input", selected: selected, spent: spent[:1], txHash: txHash},
		{
			name:     "unprepared input",
			selected: selected,
			spent: []FrostSpentOutputRef{
				spent[0],
				{TxHash: testHashOf('e'), OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: txHash},
			},
			txHash: txHash,
		},
		{
			name:     "changed known key image",
			selected: selected,
			spent: []FrostSpentOutputRef{
				spent[0],
				{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('e'), SpendTxHash: txHash},
			},
			txHash: txHash,
		},
		{
			name:     "duplicate final key image",
			selected: selected,
			spent: []FrostSpentOutputRef{
				{TxHash: testHashOf('b'), OutputIndex: 2, KeyImage: testHashOf('c'), SpendTxHash: txHash},
				spent[1],
			},
			txHash: txHash,
		},
		{
			name:     "different spend transaction",
			selected: selected,
			spent: []FrostSpentOutputRef{
				spent[0],
				{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: testHashOf('e')},
			},
			txHash: txHash,
		},
		{
			name: "duplicate prepared ref",
			selected: []FrostSpentOutputRef{
				selected[0],
				selected[0],
			},
			spent:  spent,
			txHash: txHash,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateFrostPreparedInputs(tc.selected, tc.spent, tc.txHash); err == nil {
				t.Fatal("expected input binding validation to fail")
			}
		})
	}
}

func TestNormalizeFrostResultSpentRefs(t *testing.T) {
	txHash := testHashOf('f')
	valid := []FrostSpentOutputRef{
		{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: txHash},
		{TxHash: testHashOf('b'), OutputIndex: 2, KeyImage: testHashOf('d'), SpendTxHash: txHash},
	}
	normalized, err := normalizeFrostResultSpentRefs(valid, txHash)
	if err != nil {
		t.Fatalf("valid spent refs rejected: %v", err)
	}
	if len(normalized) != len(valid) {
		t.Fatalf("unexpected normalized ref count: %d", len(normalized))
	}

	tests := []struct {
		name string
		refs []FrostSpentOutputRef
	}{
		{name: "missing refs"},
		{name: "malformed funding hash", refs: []FrostSpentOutputRef{{TxHash: "bad", OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: txHash}}},
		{name: "missing key image", refs: []FrostSpentOutputRef{{TxHash: testHashOf('a'), OutputIndex: 1, SpendTxHash: txHash}}},
		{name: "wrong spend transaction", refs: []FrostSpentOutputRef{{TxHash: testHashOf('a'), OutputIndex: 1, KeyImage: testHashOf('c'), SpendTxHash: testHashOf('e')}}},
		{name: "duplicate input", refs: []FrostSpentOutputRef{valid[0], valid[0]}},
		{name: "duplicate key image", refs: []FrostSpentOutputRef{valid[0], {TxHash: testHashOf('b'), OutputIndex: 2, KeyImage: valid[0].KeyImage, SpendTxHash: txHash}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeFrostResultSpentRefs(tc.refs, txHash); err == nil {
				t.Fatal("expected spent-ref validation to fail")
			}
		})
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

func TestFrostSignCoordinatorRejectsMaxGasExceeded(t *testing.T) {
	ensureBech32Prefix()

	members := []string{testPubKeys[0], testPubKeys[1]}
	req := FrostKeySignRequest{
		VaultPubKey:   "thorpub-vault-1",
		ToAddress:     "48abc",
		AmountAtomic:  12345,
		Memo:          "MIGRATE:100",
		SignerPubKeys: members,
		BlockHeight:   101,
		MaxGasAtomic:  100,
	}
	msgID, err := FrostKeySignMsgID(req)
	if err != nil {
		t.Fatalf("msg id: %v", err)
	}
	planHash, err := FrostPlanHash(req, msgID)
	if err != nil {
		t.Fatalf("plan hash: %v", err)
	}
	peerIDs, err := conversion.GetPeerIDsFromPubKeys(members)
	if err != nil {
		t.Fatalf("peer ids: %v", err)
	}

	msgChan := make(chan *p2p.Message, 8)
	enqueueFrostExecuteTranscriptMessages(t, msgChan, msgID, members, "aa", "bb", "cc", "dd")
	coord := &FrostSignCoordinator{
		p2pCommunication: &p2p.Communication{BroadcastMsgChan: make(chan *messages.BroadcastMsgChan, 64)},
		sidecar: &mockFrostSidecarSign{
			prepareResp: FrostSidecarPrepareResp{
				SessionID:  "session-1",
				Preprocess: "aa",
			},
			partialResp: FrostSidecarPartialResp{
				SignatureShare: "cc",
			},
			combineResp: FrostSidecarCombineResp{
				FeeAtomic: 101,
			},
		},
		localPubKey:      members[0],
		msgID:            msgID,
		expectedPlanHash: planHash,
		msgChan:          msgChan,
		timeout:          time.Second,
		logger:           zerolog.Nop(),
	}

	resp := coord.Execute(req, members, peerIDs)
	if resp.Status != tcommon.Fail {
		t.Fatalf("expected fail status, got %d", resp.Status)
	}
	if resp.FeeAtomic != 101 {
		t.Fatalf("expected actual fee to be propagated, got %d", resp.FeeAtomic)
	}
	if !strings.Contains(resp.Blame.FailReason, FrostFailReasonXMRMaxGasExceeded) ||
		!strings.Contains(resp.Blame.FailReason, "actual_fee_atomic=101") ||
		!strings.Contains(resp.Blame.FailReason, "max_gas_atomic=100") {
		t.Fatalf("unexpected fail reason: %q", resp.Blame.FailReason)
	}
	if len(resp.Blame.BlameNodes) != 0 {
		t.Fatalf("expected no blamed nodes for local max gas cap, got %+v", resp.Blame.BlameNodes)
	}
	if resp.Blame.Round != messages.FROSTKEYSIGNRESULT {
		t.Fatalf("max gas rejection after combine must be terminal at %s, got %q", messages.FROSTKEYSIGNRESULT, resp.Blame.Round)
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

func TestShouldAdoptFrostFailureResultPreservesTerminalShare(t *testing.T) {
	withNode := func(round string) FrostKeySignResponse {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("peer failure", round, []string{testPubKeys[1]}),
		}
	}
	withoutNode := func(round string) FrostKeySignResponse {
		return FrostKeySignResponse{
			Status: tcommon.Fail,
			Blame:  buildBlame("local failure", round, nil),
		}
	}

	tests := []struct {
		name      string
		local     FrostKeySignResponse
		candidate FrostKeySignResponse
		want      bool
	}{
		{"share cannot downgrade to preprocess", withoutNode(frostRoundShare), withNode(frostRoundPreprocess), false},
		{"share can adopt share blame", withoutNode(frostRoundShare), withNode(frostRoundShare), true},
		{"preprocess can adopt result blame", withoutNode(frostRoundPreprocess), withNode(frostRoundResult), true},
		{"existing local blame is retained", withNode(frostRoundShare), withNode(frostRoundShare), false},
		{"success candidate is not a failure replacement", withoutNode(frostRoundShare), FrostKeySignResponse{Status: tcommon.Success}, false},
	}
	for _, tc := range tests {
		if got := shouldAdoptFrostFailureResult(tc.local, tc.candidate); got != tc.want {
			t.Fatalf("%s: got %v, want %v", tc.name, got, tc.want)
		}
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
		Round:                  messages.FROSTKEYSIGNPREPROCESS,
		TargetSenderPubKey:     testPubKeys[1],
		TargetParticipantIndex: remotePayload.ParticipantIndex,
		PayloadHash:            remoteHash,
	})
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, "msg-id", testPubKeys[1], FrostVerifyPayload{
		SenderPubKey:           testPubKeys[1],
		Round:                  messages.FROSTKEYSIGNPREPROCESS,
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
	if len(failResp.Blame.BlameNodes) != 0 {
		t.Fatalf("missing verification without signed evidence must not be slashable: %+v", failResp.Blame.BlameNodes)
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

// TestFrostWaitForResultRejectsSingleVerifiedSuccess confirms that one
// self-consistent success result cannot satisfy the durable FROST proof bundle
// requirement.
func TestFrostWaitForResultRejectsSingleVerifiedSuccess(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 1)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}

	// The payload validates locally, but without a matching quorum it must not
	// become the accepted signed transaction for retry suppression.
	result := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('1'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     result,
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected timeout waiting for matching success quorum")
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

// TestFrostWaitForResultRejectsDivergentSuccessPayloads verifies that active
// signers must agree on the exact same XMR transaction payload, not merely
// report successful FROST completion.
func TestFrostWaitForResultRejectsDivergentSuccessPayloads(t *testing.T) {
	ensureBech32Prefix()

	msgChan := make(chan *p2p.Message, 2)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          100 * time.Millisecond,
	}

	// Both messages are individually well-formed successes, but they disagree
	// on tx hash and therefore cannot form a single proof bundle.
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{
		SenderPubKey: testPubKeys[1],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('c'), "deadbeef", 10),
	})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], testPubKeys[2], FrostResultPayload{
		SenderPubKey: testPubKeys[2],
		Response:     mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('d'), "deadbeef", 10),
	})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 150*time.Millisecond); err == nil {
		t.Fatalf("expected timeout waiting for matching success quorum")
	}
}

func TestFrostWaitForResultQuorumBindsTransactionKey(t *testing.T) {
	ensureBech32Prefix()
	msgChan := make(chan *p2p.Message, 3)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          time.Second,
	}
	first := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('a'), "deadbeef", 10)
	second := first
	second.TxKey = strings.Repeat("2", 64)
	var err error
	second.Eventuality, err = BuildFrostEventuality(testExpectedPlanHash, second.TxHash, second.TxHex, second.TxKey, second.FeeAtomic)
	if err != nil {
		t.Fatalf("build second eventuality: %v", err)
	}
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[0], testPubKeys[0], FrostResultPayload{SenderPubKey: testPubKeys[0], Response: first})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{SenderPubKey: testPubKeys[1], Response: second})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[2], testPubKeys[2], FrostResultPayload{SenderPubKey: testPubKeys[2], Response: first})

	resp, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, time.Second)
	if err != nil {
		t.Fatalf("wait for result: %v", err)
	}
	if resp.TxKey != first.TxKey {
		t.Fatalf("quorum adopted a transaction key without two matching votes")
	}
}

func TestFrostWaitForResultDisqualifiesConflictingSender(t *testing.T) {
	ensureBech32Prefix()
	msgChan := make(chan *p2p.Message, 3)
	coord := &FrostSignCoordinator{
		localPubKey:      testPubKeys[0],
		msgID:            "msg-id",
		expectedPlanHash: testExpectedPlanHash,
		msgChan:          msgChan,
		timeout:          50 * time.Millisecond,
		logger:           zerolog.Nop(),
	}
	first := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('a'), "deadbeef", 10)
	conflict := mustSuccessResult(t, "msg-id", testExpectedPlanHash, testHashOf('b'), "deadbeef", 10)
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[0], testPubKeys[0], FrostResultPayload{SenderPubKey: testPubKeys[0], Response: first})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[0], testPubKeys[0], FrostResultPayload{SenderPubKey: testPubKeys[0], Response: conflict})
	enqueueResultMessage(t, msgChan, "msg-id", testPubKeys[1], testPubKeys[1], FrostResultPayload{SenderPubKey: testPubKeys[1], Response: first})

	if _, err := coord.WaitForResult([]string{testPubKeys[0], testPubKeys[1], testPubKeys[2]}, 2, 100*time.Millisecond); !errors.Is(err, errFrostResultTimeout) {
		t.Fatalf("equivocating sender must not remain counted in first bucket: %v", err)
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

func enqueueFrostExecuteTranscriptMessages(
	t *testing.T,
	msgChan chan<- *p2p.Message,
	msgID string,
	members []string,
	localPreprocess string,
	remotePreprocess string,
	localShare string,
	remoteShare string,
) {
	t.Helper()
	if len(members) != 2 {
		t.Fatalf("expected two members, got %d", len(members))
	}
	_, participantIndexMap, _, err := buildParticipantIndex(members)
	if err != nil {
		t.Fatalf("participant index map: %v", err)
	}
	localPubKey := members[0]
	remotePubKey := members[1]

	localPreprocessPayload := FrostPreprocessPayload{
		SenderPubKey:     localPubKey,
		ParticipantIndex: participantIndexMap[normalizePubKey(localPubKey)],
		PreprocessHex:    localPreprocess,
	}
	remotePreprocessPayload := FrostPreprocessPayload{
		SenderPubKey:     remotePubKey,
		ParticipantIndex: participantIndexMap[normalizePubKey(remotePubKey)],
		PreprocessHex:    remotePreprocess,
	}
	localPreprocessHash, err := FrostTranscriptPreprocessHash(msgID, localPreprocessPayload)
	if err != nil {
		t.Fatalf("local preprocess hash: %v", err)
	}
	remotePreprocessHash, err := FrostTranscriptPreprocessHash(msgID, remotePreprocessPayload)
	if err != nil {
		t.Fatalf("remote preprocess hash: %v", err)
	}
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostPreprocessMsg, msgID, remotePubKey, remotePreprocessPayload)
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, msgID, remotePubKey, FrostVerifyPayload{
		SenderPubKey:           remotePubKey,
		Round:                  messages.FROSTKEYSIGNPREPROCESS,
		TargetSenderPubKey:     remotePubKey,
		TargetParticipantIndex: remotePreprocessPayload.ParticipantIndex,
		PayloadHash:            remotePreprocessHash,
	})
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, msgID, remotePubKey, FrostVerifyPayload{
		SenderPubKey:           remotePubKey,
		Round:                  messages.FROSTKEYSIGNPREPROCESS,
		TargetSenderPubKey:     localPubKey,
		TargetParticipantIndex: localPreprocessPayload.ParticipantIndex,
		PayloadHash:            localPreprocessHash,
	})

	localSharePayload := FrostSharePayload{
		SenderPubKey:     localPubKey,
		ParticipantIndex: participantIndexMap[normalizePubKey(localPubKey)],
		ShareHex:         localShare,
	}
	remoteSharePayload := FrostSharePayload{
		SenderPubKey:     remotePubKey,
		ParticipantIndex: participantIndexMap[normalizePubKey(remotePubKey)],
		ShareHex:         remoteShare,
	}
	localShareHash, err := FrostTranscriptShareHash(msgID, localSharePayload)
	if err != nil {
		t.Fatalf("local share hash: %v", err)
	}
	remoteShareHash, err := FrostTranscriptShareHash(msgID, remoteSharePayload)
	if err != nil {
		t.Fatalf("remote share hash: %v", err)
	}
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostShareMsg, msgID, remotePubKey, remoteSharePayload)
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, msgID, remotePubKey, FrostVerifyPayload{
		SenderPubKey:           remotePubKey,
		Round:                  messages.FROSTKEYSIGNSHARE,
		TargetSenderPubKey:     remotePubKey,
		TargetParticipantIndex: remoteSharePayload.ParticipantIndex,
		PayloadHash:            remoteShareHash,
	})
	enqueueKeySignMessage(t, msgChan, messages.TSSFrostVerifyMsg, msgID, remotePubKey, FrostVerifyPayload{
		SenderPubKey:           remotePubKey,
		Round:                  messages.FROSTKEYSIGNSHARE,
		TargetSenderPubKey:     localPubKey,
		TargetParticipantIndex: localSharePayload.ParticipantIndex,
		PayloadHash:            localShareHash,
	})
}

func newTestFrostSignCoordinator(localPubKey, msgID string, msgChan chan *p2p.Message, timeout time.Duration) *FrostSignCoordinator {
	return &FrostSignCoordinator{
		p2pCommunication: &p2p.Communication{BroadcastMsgChan: make(chan *messages.BroadcastMsgChan, 64)},
		localPubKey:      localPubKey,
		msgID:            msgID,
		vaultPubKey:      "thorpub1test-vault",
		network:          "mainnet",
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
