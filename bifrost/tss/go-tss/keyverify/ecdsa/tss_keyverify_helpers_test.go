// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/libp2p/go-libp2p-peerstore/addr"
	tsscrypto "gitlab.com/thorchain/tss/tss-lib-private.git/v3/crypto"
	btsskeygen "gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
	libtss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keygen"
	kgecdsa "gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keygen/ecdsa"
	tcommon "gitlab.com/thorchain/thornode/v3/common"
)

// runKeygen drives keygen across all 4 parties using the suite's comms and
// state managers, and returns the resulting pool pubkey. Mirrors the Phase 1
// section of TestKeyverifyHappyPath verbatim.
func (s *TssECDSAKeyverifyTestSuite) runKeygen(c *C, conf common.TssConfig) string {
	kgReq := keygen.NewRequest(testPubKeys, 10, "", tcommon.SigningAlgoSecp256k1)
	kgMsgID, err := common.MsgToHashString([]byte(strings.Join(kgReq.Keys, "")))
	c.Assert(err, IsNil)

	pubkeys := make([]string, s.partyNum)
	wg := sync.WaitGroup{}
	for i := 0; i < s.partyNum; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			comm := s.comms[idx]
			stopChan := make(chan struct{})
			localPubKey := testPubKeys[idx]
			keygenInstance := kgecdsa.NewTssKeyGen(
				comm.GetLocalPeerID(),
				conf,
				localPubKey,
				comm.BroadcastMsgChan,
				stopChan,
				s.preParams[idx],
				kgMsgID,
				s.stateMgrs[idx], s.nodePrivKeys[idx], s.comms[idx],
			)
			c.Assert(keygenInstance, NotNil)
			keygenMsgChannel := keygenInstance.GetTssKeyGenChannels()
			comm.SetSubscribe(messages.TSSKeyGenMsg, kgMsgID, keygenMsgChannel)
			comm.SetSubscribe(messages.TSSKeyGenVerMsg, kgMsgID, keygenMsgChannel)
			comm.SetSubscribe(messages.TSSControlMsg, kgMsgID, keygenMsgChannel)
			comm.SetSubscribe(messages.TSSTaskDone, kgMsgID, keygenMsgChannel)
			defer comm.CancelSubscribe(messages.TSSKeyGenMsg, kgMsgID)
			defer comm.CancelSubscribe(messages.TSSKeyGenVerMsg, kgMsgID)
			defer comm.CancelSubscribe(messages.TSSControlMsg, kgMsgID)
			defer comm.CancelSubscribe(messages.TSSTaskDone, kgMsgID)
			resp, err := keygenInstance.GenerateNewKey(kgReq)
			c.Assert(err, IsNil)
			c.Assert(resp, NotNil)
			pk, _, err := conversion.GetTssPubKeyECDSA(resp)
			c.Assert(err, IsNil)
			pubkeys[idx] = pk
		}(i)
	}
	wg.Wait()

	pubkey := pubkeys[0]
	c.Assert(pubkey, Not(Equals), "")
	for i := range pubkeys {
		c.Assert(pubkeys[i], Equals, pubkey, Commentf("party %d pubkey mismatch", i))
	}
	return pubkey
}

// runKeyverify drives keyverify across all 4 parties using the supplied
// stateMgrs (one per party). Mirrors Phase 2 of TestKeyverifyHappyPath but
// accepts stateMgrs as a parameter so individual parties can be swapped for
// a tamperingStateMgr.
func (s *TssECDSAKeyverifyTestSuite) runKeyverify(
	c *C,
	conf common.TssConfig,
	pubkey string,
	stateMgrs []storage.LocalStateManager,
	msgIDSuffix string,
) []error {
	kvMsgID, err := common.MsgToHashString([]byte(
		"keyverify-" + pubkey + "-" + strings.Join(testPubKeys, "") + "-" + msgIDSuffix,
	))
	c.Assert(err, IsNil)

	errs := make([]error, s.partyNum)
	wg := sync.WaitGroup{}
	for i := 0; i < s.partyNum; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			comm := s.comms[idx]
			stopChan := make(chan struct{})
			localPubKey := testPubKeys[idx]
			keyverifier := NewTssKeyVerify(
				comm.GetLocalPeerID(),
				conf,
				localPubKey,
				comm.BroadcastMsgChan,
				stopChan,
				kvMsgID,
				stateMgrs[idx],
				s.nodePrivKeys[idx],
			)
			c.Assert(keyverifier, NotNil)
			ch := keyverifier.GetTssKeyVerifyChannels()
			comm.SetSubscribe(messages.TSSKeyVerifyMsg, kvMsgID, ch)
			comm.SetSubscribe(messages.TSSKeyVerifyVerMsg, kvMsgID, ch)
			comm.SetSubscribe(messages.TSSControlMsg, kvMsgID, ch)
			comm.SetSubscribe(messages.TSSTaskDone, kvMsgID, ch)
			defer comm.CancelSubscribe(messages.TSSKeyVerifyMsg, kvMsgID)
			defer comm.CancelSubscribe(messages.TSSKeyVerifyVerMsg, kvMsgID)
			defer comm.CancelSubscribe(messages.TSSControlMsg, kvMsgID)
			defer comm.CancelSubscribe(messages.TSSTaskDone, kvMsgID)

			errs[idx] = keyverifier.VerifyKey(context.Background(), Request{
				PoolPubKey:  pubkey,
				Members:     testPubKeys[:s.partyNum],
				BlockHeight: 100,
			})
		}(i)
	}
	wg.Wait()
	return errs
}

// stateMgrsCopy returns a fresh []LocalStateManager that aliases s.stateMgrs
// so tests can selectively wrap one party's manager without affecting others.
func (s *TssECDSAKeyverifyTestSuite) stateMgrsCopy() []storage.LocalStateManager {
	out := make([]storage.LocalStateManager, s.partyNum)
	copy(out, s.stateMgrs)
	return out
}

// tamperingStateMgr wraps a LocalStateManager and applies mutate to the
// decoded LocalPartySaveData on every GetLocalState call. SaveLocalState is
// passed through unchanged so a prior keygen still produces real on-disk
// keyshares — corruption is in-memory only.
type tamperingStateMgr struct {
	inner  storage.LocalStateManager
	mutate func(*btsskeygen.LocalPartySaveData)
}

func (t *tamperingStateMgr) SaveLocalState(state storage.KeygenLocalState) error {
	return t.inner.SaveLocalState(state)
}

func (t *tamperingStateMgr) GetLocalState(pubKey string) (storage.KeygenLocalState, error) {
	got, err := t.inner.GetLocalState(pubKey)
	if err != nil {
		return got, err
	}
	var save btsskeygen.LocalPartySaveData
	if err := json.Unmarshal(got.LocalData, &save); err != nil {
		return got, err
	}
	t.mutate(&save)
	buf, err := json.Marshal(&save)
	if err != nil {
		return got, err
	}
	got.LocalData = buf
	return got, nil
}

func (t *tamperingStateMgr) SaveAddressBook(book map[peer.ID]addr.AddrList) error {
	return t.inner.SaveAddressBook(book)
}

func (t *tamperingStateMgr) RetrieveP2PAddresses() (addr.AddrList, error) {
	return t.inner.RetrieveP2PAddresses()
}

// culpritIDs extracts the culprit PartyID.Id values from a tss.Error. Returns
// nil if err is not a *libtss.Error.
func culpritIDs(err error) []string {
	var tssErr *libtss.Error
	if !errors.As(err, &tssErr) {
		return nil
	}
	out := make([]string, 0, len(tssErr.Culprits()))
	for _, p := range tssErr.Culprits() {
		out = append(out, p.Id)
	}
	return out
}

// mapCulpritsToThorpubs replicates cmd/bifrost/main.go::extractCulprits:
// PartyID.Id is the sorted index assigned by conversion.GetParties, so we
// reverse it against the sorted members list to recover each culprit's
// thorpub. Returns nil if err is not a *libtss.Error. Caller pre-sorts
// `members` to match conversion.GetParties' ordering.
func mapCulpritsToThorpubs(err error, sortedMembers []string) []string {
	var tssErr *libtss.Error
	if !errors.As(err, &tssErr) {
		return nil
	}
	out := make([]string, 0, len(tssErr.Culprits()))
	for _, p := range tssErr.Culprits() {
		idx, perr := strconv.Atoi(p.Id)
		if perr != nil || idx < 0 || idx >= len(sortedMembers) {
			out = append(out, p.Id)
			continue
		}
		out = append(out, sortedMembers[idx])
	}
	return out
}

// assertBadShareBlame asserts that at least minBlamingParties of the supplied
// errs are *libtss.Error and that the expectedCulpritID appears in their
// culprit lists. Used by both tampered-share tests so the assertion shape is
// consistent.
func assertBadShareBlame(c *C, errs []error, expectedCulpritID string, minBlamingParties int) {
	blaming := 0
	for i, err := range errs {
		if err == nil {
			continue
		}
		ids := culpritIDs(err)
		if len(ids) == 0 {
			c.Logf("party %d returned non-tss error: %v", i, err)
			continue
		}
		found := false
		for _, id := range ids {
			if id == expectedCulpritID {
				found = true
				break
			}
		}
		if found {
			blaming++
			c.Logf("party %d blamed culprit %s (full culprits=%v)", i, expectedCulpritID, ids)
		} else {
			c.Logf("party %d returned tss error but did not blame %s (culprits=%v)", i, expectedCulpritID, ids)
		}
	}
	c.Assert(blaming >= minBlamingParties, Equals, true,
		Commentf("expected at least %d parties to blame culprit id %q; got %d", minBlamingParties, expectedCulpritID, blaming))
}

// defaultVerifyConf returns the TssConfig used by all keyverify tests. Kept
// in a helper so the timeout values are consistent.
func defaultVerifyConf() common.TssConfig {
	return common.TssConfig{
		KeyGenTimeout:   120 * time.Second,
		KeySignTimeout:  120 * time.Second,
		PreParamTimeout: 5 * time.Second,
	}
}

// tamperECDSAPub returns a mutator that replaces ECDSAPub with a different
// on-curve point (2*G). Other parties hold the real pool public key and so
// will reject the bound Paillier-N proof produced by the tampered party in
// Round 4.
func tamperECDSAPub() func(*btsskeygen.LocalPartySaveData) {
	return func(save *btsskeygen.LocalPartySaveData) {
		save.ECDSAPub = tsscrypto.ScalarBaseMult(libtss.S256(), big.NewInt(2))
	}
}
