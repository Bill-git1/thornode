package thorchain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blang/semver"
	se "github.com/cosmos/cosmos-sdk/types/errors"
	. "gopkg.in/check.v1"

	tssMessages "gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/constants"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/keeper"
)

type HandlerTssKeysignSuite struct{}

var _ = Suite(&HandlerTssKeysignSuite{})

type tssKeysignFailHandlerTestHelper struct {
	ctx           cosmos.Context
	version       semver.Version
	keeper        *tssKeysignKeeperHelper
	constAccessor constants.ConstantValues
	nodeAccount   NodeAccount
	mgr           Manager
	blame         Blame
	retiringVault Vault
}

type tssKeysignKeeperHelper struct {
	keeper.Keeper
	errListActiveAccounts           bool
	errGetTssVoter                  bool
	errFailToGetNodeAccountByPubKey bool
	errFailSetNodeAccount           bool
}

func newTssKeysignFailKeeperHelper(keeper keeper.Keeper) *tssKeysignKeeperHelper {
	return &tssKeysignKeeperHelper{
		Keeper: keeper,
	}
}

func (k *tssKeysignKeeperHelper) GetNodeAccountByPubKey(ctx cosmos.Context, pk common.PubKey) (NodeAccount, error) {
	if k.errFailToGetNodeAccountByPubKey {
		return NodeAccount{}, errKaboom
	}
	return k.Keeper.GetNodeAccountByPubKey(ctx, pk)
}

func (k *tssKeysignKeeperHelper) SetNodeAccount(ctx cosmos.Context, na NodeAccount) error {
	if k.errFailSetNodeAccount {
		return errKaboom
	}
	return k.Keeper.SetNodeAccount(ctx, na)
}

func (k *tssKeysignKeeperHelper) GetTssKeysignFailVoter(ctx cosmos.Context, id string) (TssKeysignFailVoter, error) {
	if k.errGetTssVoter {
		return TssKeysignFailVoter{}, errKaboom
	}
	return k.Keeper.GetTssKeysignFailVoter(ctx, id)
}

func (k *tssKeysignKeeperHelper) ListActiveValidators(ctx cosmos.Context) (NodeAccounts, error) {
	if k.errListActiveAccounts {
		return NodeAccounts{}, errKaboom
	}
	return k.Keeper.ListActiveValidators(ctx)
}

func signVoter(ctx cosmos.Context, keeper keeper.Keeper, except cosmos.AccAddress) (result []cosmos.AccAddress) {
	active, _ := keeper.ListActiveValidators(ctx)
	for _, na := range active {
		if na.NodeAddress.Equals(except) {
			continue
		}
		result = append(result, na.NodeAddress)
	}
	return
}

// legacyKeysignFailID reproduces the raw round hashing used before canonical
// round normalization so tests can assert those IDs are rejected.
func legacyKeysignFailID(members []Node, height int64, memo string, coins common.Coins, pubKey common.PubKey, round string) (string, error) {
	sort.SliceStable(members, func(i, j int) bool {
		return members[i].Pubkey < members[j].Pubkey
	})

	var sb strings.Builder
	for _, item := range members {
		sb.WriteString(item.Pubkey)
	}
	sb.WriteString(fmt.Sprintf("%d", height))
	sb.WriteString(memo)
	sb.WriteString(pubKey.String())
	for _, coin := range coins {
		sb.WriteString(coin.String())
	}
	sb.WriteString(round)

	hash := sha256.New()
	if _, err := hash.Write([]byte(sb.String())); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func newTssKeysignHandlerTestHelper(c *C) tssKeysignFailHandlerTestHelper {
	ctx, k := setupKeeperForTest(c)
	ctx = ctx.WithBlockHeight(1023)
	keeperHelper := newTssKeysignFailKeeperHelper(k)
	// active account
	nodeAccount := GetRandomValidatorNode(NodeActive)
	nodeAccount.Bond = cosmos.NewUint(100 * common.One)
	c.Assert(keeperHelper.SetNodeAccount(ctx, nodeAccount), IsNil)
	constAccessor := constants.GetConstantValues(GetCurrentVersion())
	mgr := NewDummyMgr()

	var members []Node
	for i := 0; i < 8; i++ {
		na := GetRandomValidatorNode(NodeActive)
		members = append(members, Node{Pubkey: na.PubKeySet.Secp256k1.String()})
		_ = keeperHelper.SetNodeAccount(ctx, na)
	}
	blame := Blame{
		FailReason: "whatever",
		Round:      tssMessages.KEYSIGN1aUnicast,
		BlameNodes: []Node{members[0], members[1]},
	}
	asgardVault := NewVault(ctx.BlockHeight(), ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.ETHChain}.Strings(), []ChainContract{})
	c.Assert(keeperHelper.SetVault(ctx, asgardVault), IsNil)
	retiringVault := NewVault(ctx.BlockHeight(), RetiringVault, AsgardVault, GetRandomPubKey(), common.Chains{common.ETHChain}.Strings(), []ChainContract{})
	for _, item := range members {
		retiringVault.Membership = append(retiringVault.Membership, item.Pubkey)
	}
	c.Assert(keeperHelper.SetVault(ctx, retiringVault), IsNil)
	return tssKeysignFailHandlerTestHelper{
		ctx:           ctx,
		version:       GetCurrentVersion(),
		keeper:        keeperHelper,
		constAccessor: constAccessor,
		nodeAccount:   nodeAccount,
		mgr:           mgr,
		blame:         blame,
		retiringVault: retiringVault,
	}
}

func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler(c *C) {
	testCases := []struct {
		name           string
		messageCreator func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg
		runner         func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error)
		validator      func(helper tssKeysignFailHandlerTestHelper, msg cosmos.Msg, result *cosmos.Result, c *C)
		expectedResult error
	}{
		{
			name: "invalid message should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				return NewMsgNoOp(GetRandomObservedTx(), helper.nodeAccount.NodeAddress, "")
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: errInvalidMessage,
		},
		{
			name: "Not signed by an active account should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, GetRandomBech32Addr(), GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: se.ErrUnauthorized,
		},
		{
			name: "empty signer should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, cosmos.AccAddress{}, GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: se.ErrInvalidAddress,
		},
		{
			name: "empty id should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				tssMsg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				tssMsg.ID = ""
				return tssMsg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: se.ErrUnknownRequest,
		},
		{
			name: "empty member pubkeys should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), Blame{
					FailReason: "",
					Round:      tssMessages.KEYSIGN1aUnicast,
					BlameNodes: []Node{},
				}, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: se.ErrUnknownRequest,
		},
		{
			name: "normal blame should works fine",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, helper.retiringVault.PubKey)
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: nil,
		},
		{
			name: "when the same signer already sign the tss keysign failure , it should not do anything",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				voter, _ := helper.keeper.Keeper.GetTssKeysignFailVoter(helper.ctx, msg.ID)
				voter.Sign(msg.Signer)
				helper.keeper.Keeper.SetTssKeysignFailVoter(helper.ctx, voter)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: nil,
		},
		{
			name: "fail to list active node accounts should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				helper.keeper.errListActiveAccounts = true
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: errKaboom,
		},
		{
			name: "fail to get Tss Keysign fail voter should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				helper.keeper.errGetTssVoter = true
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: errKaboom,
		},
		{
			name: "fail to get node account should return an error",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, GetRandomPubKey())
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				mmsg, ok := msg.(*MsgTssKeysignFail)
				c.Assert(ok, Equals, true)
				// prepopulate the voter with other signers
				voter, _ := helper.keeper.GetTssKeysignFailVoter(helper.ctx, mmsg.ID)
				signers := signVoter(helper.ctx, helper.keeper, mmsg.Signer)
				voter.Signers = make([]string, len(signers))
				for i, sign := range signers {
					voter.Signers[i] = sign.String()
				}
				helper.keeper.SetTssKeysignFailVoter(helper.ctx, voter)
				helper.keeper.errFailToGetNodeAccountByPubKey = true
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: errInternal,
		},
		{
			name: "without majority it should not take any actions",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, helper.retiringVault.PubKey)
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				for i := 0; i < 3; i++ {
					na := GetRandomValidatorNode(NodeActive)
					if err := helper.keeper.SetNodeAccount(helper.ctx, na); err != nil {
						return nil, ErrInternal(err, "fail to set node account")
					}
				}
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: nil,
		},
		{
			name: "with majority it should take actions",
			messageCreator: func(helper tssKeysignFailHandlerTestHelper) cosmos.Msg {
				msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, helper.nodeAccount.NodeAddress, helper.retiringVault.PubKey)
				c.Assert(err, IsNil)
				return msg
			},
			runner: func(handler TssKeysignHandler, msg cosmos.Msg, helper tssKeysignFailHandlerTestHelper) (*cosmos.Result, error) {
				var na NodeAccount
				for i := 0; i < 3; i++ {
					na = GetRandomValidatorNode(NodeActive)
					if err := helper.keeper.SetNodeAccount(helper.ctx, na); err != nil {
						return nil, ErrInternal(err, "fail to set node account")
					}
				}
				_, err := handler.Run(helper.ctx, msg)
				if err != nil {
					return nil, err
				}
				msg, err = NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, na.NodeAddress, helper.retiringVault.PubKey)
				c.Assert(err, IsNil)
				return handler.Run(helper.ctx, msg)
			},
			expectedResult: nil,
		},
	}
	for _, tc := range testCases {
		helper := newTssKeysignHandlerTestHelper(c)
		handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))
		msg := tc.messageCreator(helper)

		c.Logf(">Name: %s\n", tc.name)
		result, err := tc.runner(handler, msg, helper)
		if tc.expectedResult == nil {
			c.Logf("Name: %s, %s\n", tc.name, err)
			c.Assert(err, IsNil)
		} else {
			c.Assert(errors.Is(err, tc.expectedResult), Equals, true, Commentf("name:%s, %w", tc.name, err))
		}
		if tc.validator != nil {
			tc.validator(helper, msg, result, c)
		}
	}
}

func (h HandlerTssKeysignSuite) TestDuplicateKeysignFailureVoteDoesNotSlashAgain(c *C) {
	helper := newTssKeysignHandlerTestHelper(c)
	handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))
	msg, err := NewMsgTssKeysignFail(
		helper.ctx.BlockHeight(),
		helper.blame,
		"duplicate-vote",
		common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))},
		helper.nodeAccount.NodeAddress,
		helper.retiringVault.PubKey,
	)
	c.Assert(err, IsNil)
	voter, err := helper.keeper.GetTssKeysignFailVoter(helper.ctx, msg.ID)
	c.Assert(err, IsNil)
	c.Assert(voter.Sign(msg.Signer), Equals, true)
	helper.keeper.SetTssKeysignFailVoter(helper.ctx, voter)

	before, err := helper.keeper.GetNodeAccountSlashPoints(helper.ctx, msg.Signer)
	c.Assert(err, IsNil)
	_, err = handler.Run(helper.ctx, msg)
	c.Assert(err, IsNil)
	after, err := helper.keeper.GetNodeAccountSlashPoints(helper.ctx, msg.Signer)
	c.Assert(err, IsNil)
	c.Assert(after, Equals, before)
}

func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_accept_standby_node_messages(c *C) {
	helper := newTssKeysignHandlerTestHelper(c)
	handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))
	vault := NewVault(1024, RetiringVault, AsgardVault, GetRandomPubKey(), []string{
		common.ETHChain.String(),
	}, []ChainContract{})
	accounts := NodeAccounts{}
	for i := 0; i < 8; i++ {
		na := GetRandomValidatorNode(NodeActive)
		_ = helper.keeper.SetNodeAccount(helper.ctx, na)
		vault.Membership = append(vault.Membership, na.PubKeySet.Secp256k1.String())
		accounts = append(accounts, na)
	}
	naStandby := GetRandomValidatorNode(NodeStandby)
	_ = helper.keeper.SetNodeAccount(helper.ctx, naStandby)
	vault.Membership = append(vault.Membership, naStandby.PubKeySet.Secp256k1.String())
	c.Assert(helper.keeper.SetVault(helper.ctx, vault), IsNil)
	for idx, item := range accounts {
		if idx >= 4 {
			break
		}
		msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, item.NodeAddress, vault.PubKey)
		c.Assert(err, IsNil)
		result, err := handler.Run(helper.ctx, msg)
		c.Assert(result, NotNil)
		c.Assert(err, IsNil)
	}
	msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, naStandby.NodeAddress, vault.PubKey)
	c.Assert(err, IsNil)
	result, err := handler.Run(helper.ctx, msg)
	c.Assert(result, NotNil)
	c.Assert(err, IsNil)

	msg1, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}, naStandby.NodeAddress, vault.PubKey)
	msg1.Blame.BlameNodes = []Node{}
	c.Assert(err, IsNil)
	result, err = handler.Run(helper.ctx, msg1)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)
	c.Assert(errors.Is(err, se.ErrUnknownRequest), Equals, true)
}

// TestTssKeysignFailHandler_TerminalRoundIncrementsLastRoundCount asserts the
// consensus-affecting LastRoundCount++ path: a MsgTssKeysignFail carrying one
// of the accepted terminal-round names (v3 secp256k1 KEYSIGN9, EdDSA
// EDDSAKEYSIGN3, FROST frost_share/frost_result) must increment
// voter.LastRoundCount for
// the signer's vault membership. Non-terminal rounds — including KEYSIGN7,
// which was terminal under tss-lib v1 but is an interior consistency-check
// round under v3 — must
// NOT increment. Regressing this would either freeze vaults too eagerly (on
// mid-protocol round-7 timeouts) or fail to freeze vaults that should be frozen
// on terminal-round consensus.
func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_TerminalRoundIncrementsLastRoundCount(c *C) {
	cases := []struct {
		name            string
		round           string
		emptyBlameNodes bool
		wantIncrement   bool
	}{
		{"v3 secp terminal round 9 increments", tssMessages.KEYSIGN9, false, true},
		{"eddsa terminal round 3 increments", tssMessages.EDDSAKEYSIGN3, false, true},
		{"frost share exposure boundary increments", tssMessages.FROSTKEYSIGNSHARE, false, true},
		// frost_result sits PAST the exposure boundary, not before it: the only
		// blame stamped with this round comes from WaitForResult, which runs
		// after exchangeShares has already broadcast the local signature share.
		// A quorum of peers can therefore aggregate and broadcast the original
		// transaction, so the signer must stick to the same vault. Treating it
		// as non-terminal would let the outbound reschedule to a different
		// vault while the share is already out — a double spend.
		{"frost terminal result increments", tssMessages.FROSTKEYSIGNRESULT, false, true},
		{"frost preprocess does not increment", tssMessages.FROSTKEYSIGNPREPROCESS, false, false},
		// KEYSIGN7 was terminal under tss-lib v1 but is an interior round under
		// v3 (the U_i/T_i consistency-check commitment, before shares reveal at
		// round 9), so it must NOT increment — otherwise a routine round-7 peer
		// timeout would freeze the vault.
		{"v3 secp interior round 7 does not increment", tssMessages.KEYSIGN7, false, false},
		{"v3 secp non-terminal round 8 does not increment", tssMessages.KEYSIGN8, false, false},
		{"v3 secp non-terminal round 5 does not increment", tssMessages.KEYSIGN5, false, false},
		{"eddsa non-terminal round 1 does not increment", tssMessages.EDDSAKEYSIGN1, false, false},
		// Empty-BlameNodes guard: terminal-round reports that blame nobody
		// must not bump LastRoundCount, otherwise a colluding minority can
		// freeze a vault by spamming terminal-round reports with no blames.
		{"v3 secp terminal round 9 with empty BlameNodes does not increment", tssMessages.KEYSIGN9, true, false},
		{"eddsa terminal round 3 with empty BlameNodes does not increment", tssMessages.EDDSAKEYSIGN3, true, false},
		{"frost share with empty BlameNodes does not increment", tssMessages.FROSTKEYSIGNSHARE, true, false},
		{"frost terminal result with empty BlameNodes does not increment", tssMessages.FROSTKEYSIGNRESULT, true, false},
	}

	for _, tc := range cases {
		c.Logf(">Name: %s", tc.name)
		helper := newTssKeysignHandlerTestHelper(c)
		handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))

		// The LastRoundCount increment fires only when the signer is in the
		// vault's membership (the freeze path counts terminal-round failures
		// from the actual signing committee). The default helper doesn't add
		// helper.nodeAccount to retiringVault.Membership, so push it in.
		helper.retiringVault.Membership = append(helper.retiringVault.Membership, helper.nodeAccount.PubKeySet.Secp256k1.String())
		c.Assert(helper.keeper.SetVault(helper.ctx, helper.retiringVault), IsNil)

		helper.blame.Round = tc.round
		if tc.emptyBlameNodes {
			helper.blame.BlameNodes = []Node{}
		}
		coins := common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}
		msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), helper.blame, "hello", coins, helper.nodeAccount.NodeAddress, helper.retiringVault.PubKey)
		c.Assert(err, IsNil)

		_, err = handler.Run(helper.ctx, msg)
		c.Assert(err, IsNil)

		voter, vErr := helper.keeper.GetTssKeysignFailVoter(helper.ctx, msg.ID)
		c.Assert(vErr, IsNil)
		if tc.wantIncrement {
			c.Assert(voter.LastRoundCount, Equals, int64(1), Commentf("%s: expected LastRoundCount=1 after terminal-round report", tc.name))
		} else {
			c.Assert(voter.LastRoundCount, Equals, int64(0), Commentf("%s: expected LastRoundCount=0 for non-terminal round", tc.name))
		}
	}
}

// TestTssKeysignFailHandler_rejects_legacy_round_ids ensures Thornode only
// accepts the canonical voter ID once round normalization is enforced.
func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_rejects_legacy_round_ids(c *C) {
	helper := newTssKeysignHandlerTestHelper(c)
	handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))
	coins := common.Coins{common.NewCoin(common.ETHAsset, cosmos.NewUint(100))}

	helper.blame.Round = tssMessages.KEYSIGN7
	legacyID, err := legacyKeysignFailID(helper.blame.BlameNodes, helper.ctx.BlockHeight(), "hello", coins, helper.retiringVault.PubKey, "thorchain.tsslib.ecdsa.signing.SignRound7Message")
	c.Assert(err, IsNil)

	msg := &MsgTssKeysignFail{
		ID:     legacyID,
		Height: helper.ctx.BlockHeight(),
		Blame:  helper.blame,
		Memo:   "hello",
		Coins:  coins,
		Signer: helper.nodeAccount.NodeAddress,
		PubKey: helper.retiringVault.PubKey,
	}

	result, err := handler.Run(helper.ctx, msg)
	c.Assert(result, IsNil)
	c.Assert(err, NotNil)
	c.Assert(errors.Is(err, se.ErrUnknownRequest), Equals, true)
}

// runTerminalRoundFreezeScenario submits an MsgTssKeysignFail with the given
// terminal round from a simple majority of vault members. The blameBuilder
// callback receives the registered member accounts and returns the BlameNodes
// list to use, so positive-path callers can reference real registered pubkeys
// (the handler resolves blame entries through GetNodeAccountByPubKey).
func runTerminalRoundFreezeScenario(c *C, round string, coin common.Coin, blameBuilder func(members NodeAccounts) []Node) Vault {
	helper := newTssKeysignHandlerTestHelper(c)
	handler := NewTssKeysignHandler(NewDummyMgrWithKeeper(helper.keeper))
	coins := common.Coins{coin}

	vault := NewVault(helper.ctx.BlockHeight(), ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{coin.Asset.GetChain()}.Strings(), []ChainContract{})
	accounts := NodeAccounts{}
	for i := 0; i < 8; i++ {
		na := GetRandomValidatorNode(NodeActive)
		c.Assert(helper.keeper.SetNodeAccount(helper.ctx, na), IsNil)
		vault.Membership = append(vault.Membership, na.PubKeySet.Secp256k1.String())
		accounts = append(accounts, na)
	}
	c.Assert(helper.keeper.SetVault(helper.ctx, vault), IsNil)

	blame := Blame{
		FailReason: "tss timeout",
		Round:      round,
		BlameNodes: blameBuilder(accounts),
	}
	// Submit from 5 of 8 vault members to clear simple-majority consensus.
	for i := 0; i < 5; i++ {
		msg, err := NewMsgTssKeysignFail(helper.ctx.BlockHeight(), blame, "hello", coins, accounts[i].NodeAddress, vault.PubKey)
		c.Assert(err, IsNil)
		_, err = handler.Run(helper.ctx, msg)
		c.Assert(err, IsNil)
	}

	got, err := helper.keeper.GetVault(helper.ctx, vault.PubKey)
	c.Assert(err, IsNil)
	return got
}

// TestTssKeysignFailHandler_terminal_round_empty_blamenodes_does_not_freeze is
// the regression for the BlameNodes-guard fix: a terminal-round MsgTssKeysignFail
// with len(BlameNodes) == 0 must NOT increment LastRoundCount and therefore
// must NOT freeze the vault. Without the guard, a colluding minority could
// lock a vault by reporting terminal-round failures that blame nobody, since
// FailReason alone satisfies Blame.IsEmpty().
func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_terminal_round_empty_blamenodes_does_not_freeze(c *C) {
	cases := []struct {
		round string
		coin  common.Coin
	}{
		{tssMessages.KEYSIGN9, common.NewCoin(common.ETHAsset, cosmos.NewUint(100))},
		{tssMessages.EDDSAKEYSIGN3, common.NewCoin(common.ETHAsset, cosmos.NewUint(100))},
		{tssMessages.FROSTKEYSIGNSHARE, common.NewCoin(common.XMRAsset, cosmos.NewUint(100))},
		{tssMessages.FROSTKEYSIGNRESULT, common.NewCoin(common.XMRAsset, cosmos.NewUint(100))},
	}
	for _, tc := range cases {
		got := runTerminalRoundFreezeScenario(c, tc.round, tc.coin, func(_ NodeAccounts) []Node { return []Node{} })
		c.Assert(got.Frozen, HasLen, 0, Commentf("round=%s: empty BlameNodes must not freeze", tc.round))
	}
}

// TestTssKeysignFailHandler_terminal_round_with_blamenodes_freezes covers the
// positive path: a terminal-round MsgTssKeysignFail with a non-empty BlameNodes
// list reported by a majority of vault members must freeze the vault on the
// blamed chain. Together with the empty-BlameNodes regression above, this
// pins down the exact condition the guard enforces.
func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_terminal_round_with_blamenodes_freezes(c *C) {
	cases := []struct {
		round string
		coin  common.Coin
	}{
		{tssMessages.KEYSIGN9, common.NewCoin(common.ETHAsset, cosmos.NewUint(100))},
		{tssMessages.EDDSAKEYSIGN3, common.NewCoin(common.ETHAsset, cosmos.NewUint(100))},
		{tssMessages.FROSTKEYSIGNSHARE, common.NewCoin(common.XMRAsset, cosmos.NewUint(100))},
		{tssMessages.FROSTKEYSIGNRESULT, common.NewCoin(common.XMRAsset, cosmos.NewUint(100))},
	}
	for _, tc := range cases {
		got := runTerminalRoundFreezeScenario(c, tc.round, tc.coin, func(members NodeAccounts) []Node {
			return []Node{{Pubkey: members[7].PubKeySet.Secp256k1.String()}}
		})
		c.Assert(got.Frozen, DeepEquals, []string{tc.coin.Asset.GetChain().String()}, Commentf("round=%s: non-empty BlameNodes must freeze", tc.round))
	}
}

// TestTssKeysignFailHandler_frost_preprocess_does_not_freeze pins the other
// side of the FROST safety boundary: preprocess failures occur before any
// signature share is broadcast, so they may be rescheduled to another vault.
func (h HandlerTssKeysignSuite) TestTssKeysignFailHandler_frost_preprocess_does_not_freeze(c *C) {
	got := runTerminalRoundFreezeScenario(
		c,
		tssMessages.FROSTKEYSIGNPREPROCESS,
		common.NewCoin(common.XMRAsset, cosmos.NewUint(100)),
		func(members NodeAccounts) []Node {
			return []Node{{Pubkey: members[7].PubKeySet.Secp256k1.String()}}
		},
	)
	c.Assert(got.Frozen, HasLen, 0)
}
