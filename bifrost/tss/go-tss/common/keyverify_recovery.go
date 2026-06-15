// TEMP: keyverify — remove this entire file with the keyverify rollout.
//
// Per-attempt recovery state for keyverify (see
// bifrost/tss/keyverify/DESIGN_retry_and_blame_visibility.md, Part A).
// All state here is inert unless EnableKeyverifyRecovery was called on the
// TssCommon instance, which only the keyverify wrapper does — keygen and
// keysign behavior is unchanged.
package common

import (
	"time"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

// keyverifyRoundIndex orders the keyverify rounds so "last round" is
// well-defined when computing per-peer wire receipts.
var keyverifyRoundIndex = map[string]int{
	messages.KEYVERIFY1:  0,
	messages.KEYVERIFY2a: 1,
	messages.KEYVERIFY2b: 2,
	messages.KEYVERIFY3:  3,
}

// KeyverifyPeerWire is the wire-level receipt summary for one peer (party ID
// keyed) within one keyverify attempt. "Wire" means a signature-verified
// message arrived from the peer — independent of whether it later passed the
// broadcast hash-check and was accepted. The accepted-vs-wire gap is the
// signal the blame visibility work needs (a peer whose messages arrive but
// never confirm is a different failure than a silent peer).
type KeyverifyPeerWire struct {
	LastRound  string
	LastSeenAt time.Time
	MsgsSeen   int
}

// KeyverifyRecoveryStats counts the recovery actions performed by one
// RunKeyverifyRecovery tick, for metrics.
type KeyverifyRecoveryStats struct {
	UnicastResent       int
	BroadcastResent     int
	ConfirmsRebroadcast int
	SharesRerequested   int
}

type kvSentMsg struct {
	msg         *messages.BroadcastMsgChan
	isBroadcast bool
	retries     int
}

type keyverifyRecovery struct {
	sentMsgs      []*kvSentMsg
	actionRetries map[string]int
	wireReceipt   map[string]*KeyverifyPeerWire
	recoveredHook func(round string)
}

// EnableKeyverifyRecovery switches on recovery state tracking for this
// TssCommon instance. recoveredHook (optional) fires when a broadcast share
// is accepted after at least one recovery action targeted it, with the
// share's round — backing tss_keyverify_recovered_total.
//
// Must be called before the protocol starts (single-goroutine setup phase);
// the flag itself is then only read.
func (t *TssCommon) EnableKeyverifyRecovery(recoveredHook func(round string)) {
	t.unConfirmedMsgLock.Lock()
	defer t.unConfirmedMsgLock.Unlock()
	t.kvRecovery = &keyverifyRecovery{
		actionRetries: make(map[string]int),
		wireReceipt:   make(map[string]*KeyverifyPeerWire),
		recoveredHook: recoveredHook,
	}
}

// recordKeyverifySent retains the exact outbound payload so the recovery
// ticker can re-send it verbatim (same signature, same recipients). Called
// from sendBulkMsg under no lock; serialized via unConfirmedMsgLock which is
// uncontended on this path.
func (t *TssCommon) recordKeyverifySent(msg *messages.BroadcastMsgChan, isBroadcast bool) {
	t.unConfirmedMsgLock.Lock()
	defer t.unConfirmedMsgLock.Unlock()
	if t.kvRecovery == nil {
		return
	}
	t.kvRecovery.sentMsgs = append(t.kvRecovery.sentMsgs, &kvSentMsg{
		msg:         msg,
		isBroadcast: isBroadcast,
	})
}

// recordKeyverifyWireReceipt notes that a signature-verified keyverify wire
// message arrived from the given party for the given round.
func (t *TssCommon) recordKeyverifyWireReceipt(fromPartyID, roundInfo string) {
	t.unConfirmedMsgLock.Lock()
	defer t.unConfirmedMsgLock.Unlock()
	if t.kvRecovery == nil {
		return
	}
	w, ok := t.kvRecovery.wireReceipt[fromPartyID]
	if !ok {
		w = &KeyverifyPeerWire{}
		t.kvRecovery.wireReceipt[fromPartyID] = w
	}
	w.MsgsSeen++
	w.LastSeenAt = time.Now()
	// RoundInfo is sender-asserted (signed, but content-arbitrary): only the
	// four known rounds may advance LastRound, so a culprit cannot pollute
	// their own receipt entry in our failure records. Unknown strings still
	// count toward MsgsSeen/LastSeenAt above.
	idx, known := keyverifyRoundIndex[roundInfo]
	if !known {
		return
	}
	if w.LastRound == "" || idx >= keyverifyRoundIndex[w.LastRound] {
		w.LastRound = roundInfo
	}
}

// GetKeyverifyWireReceipts returns a copy of the per-peer wire receipt map,
// keyed by party ID. Used by the keyverify timeout path to build per-culprit
// detail records.
func (t *TssCommon) GetKeyverifyWireReceipts() map[string]KeyverifyPeerWire {
	t.unConfirmedMsgLock.Lock()
	defer t.unConfirmedMsgLock.Unlock()
	if t.kvRecovery == nil {
		return nil
	}
	out := make(map[string]KeyverifyPeerWire, len(t.kvRecovery.wireReceipt))
	for k, v := range t.kvRecovery.wireReceipt {
		out[k] = *v
	}
	return out
}

// noteKeyverifyRecovered fires the recovered hook when an accepted share was
// previously the target of a recovery action (confirm re-broadcast or share
// re-request). Called from applyShare after successful acceptance.
func (t *TssCommon) noteKeyverifyRecovered(key, round string) {
	t.unConfirmedMsgLock.Lock()
	if t.kvRecovery == nil {
		t.unConfirmedMsgLock.Unlock()
		return
	}
	recovered := t.kvRecovery.actionRetries["confirm:"+key] > 0 || t.kvRecovery.actionRetries["req:"+key] > 0
	hook := t.kvRecovery.recoveredHook
	t.unConfirmedMsgLock.Unlock()
	if recovered && hook != nil {
		// round is the accepted message's sender-asserted RoundInfo — clamp
		// to the known rounds so a malicious member cannot mint unbounded
		// metric label values (Prometheus cardinality).
		if _, known := keyverifyRoundIndex[round]; !known {
			round = "other"
		}
		hook(round)
	}
}

// RunKeyverifyRecovery performs one tick of the recovery actions (design
// Part A, actions a–d), each artifact capped at maxRetries re-sends:
//
//	(a) re-send own unicast messages (KEYVERIFY2a) — receiver-deduplicated
//	    by CheckMsgDuplication, the only coverage unicast has;
//	(b) re-broadcast own hash-confirms for received-but-unaccepted
//	    broadcasts — unblocks local acceptance, remote pull discovery, and
//	    remote pull serving in one action;
//	(c) re-fire the existing requestShareFromPeer pull for broadcasts we
//	    know we are missing (majority hash determinable);
//	(d) re-send own broadcasts blind — last resort, no receipt signal
//	    exists for the sender (confirms exclude the data owner).
func (t *TssCommon) RunKeyverifyRecovery(maxRetries int) KeyverifyRecoveryStats {
	var stats KeyverifyRecoveryStats

	t.unConfirmedMsgLock.Lock()
	if t.kvRecovery == nil {
		t.unConfirmedMsgLock.Unlock()
		return stats
	}
	// snapshot what to act on under the lock, act after releasing it —
	// renderToP2P and getMsgHash must not run under unConfirmedMsgLock.
	var resend []*messages.BroadcastMsgChan
	for _, s := range t.kvRecovery.sentMsgs {
		if s.retries >= maxRetries {
			continue
		}
		s.retries++
		resend = append(resend, s.msg)
		if s.isBroadcast {
			stats.BroadcastResent++
		} else {
			stats.UnicastResent++
		}
	}
	type cacheSnap struct {
		key      string
		msg      *messages.WireMessage
		confirms map[string]string
	}
	var confirmables, requestables []cacheSnap
	for key, item := range t.unConfirmedMessages {
		// Msg and ConfirmedList are copied under the item lock: the inbound
		// goroutine mutates both concurrently, and iterating the live map
		// from this goroutine would be a runtime fatal. Lock order here is
		// unConfirmedMsgLock → item.lock; no path takes them in reverse.
		msg, confirms := item.snapshotMsgAndConfirms()
		switch {
		case msg != nil:
			if t.kvRecovery.actionRetries["confirm:"+key] < maxRetries {
				confirmables = append(confirmables, cacheSnap{key: key, msg: msg})
			}
		default:
			if t.kvRecovery.actionRetries["req:"+key] < maxRetries {
				requestables = append(requestables, cacheSnap{key: key, confirms: confirms})
			}
		}
	}
	t.unConfirmedMsgLock.Unlock()

	for _, m := range resend {
		t.renderToP2P(m)
	}

	partyInfo := t.getPartyInfo()
	if partyInfo == nil {
		return stats
	}
	threshold, err := conversion.GetThreshold(len(partyInfo.PartyIDMap))
	if err != nil {
		t.logger.Error().Err(err).Msg("keyverify recovery: threshold")
		return stats
	}

	for _, c := range confirmables {
		if err := t.receiverBroadcastHashToPeers(c.msg, messages.TSSKeyVerifyMsg); err != nil {
			t.logger.Error().Err(err).Str("key", c.key).Msg("keyverify recovery: re-broadcast confirm")
			continue
		}
		t.bumpKeyverifyAction("confirm:" + c.key)
		stats.ConfirmsRebroadcast++
	}

	for _, r := range requestables {
		// getMsgHash/requestShareFromPeer iterate ConfirmedList without the
		// item lock (safe on the inbound goroutine, not from this ticker), so
		// hand them a detached item built from the snapshot copy.
		snap := NewLocalCacheItem(nil, "")
		snap.ConfirmedList = r.confirms
		// requestShareFromPeer silently no-ops when the majority hash is not
		// yet determinable; replicate its gate so the retry counter and
		// metric only reflect requests actually sent.
		if _, err := t.getMsgHash(snap, threshold); err != nil {
			continue
		}
		if err := t.requestShareFromPeer(snap, threshold, r.key, messages.TSSKeyVerifyVerMsg); err != nil {
			t.logger.Error().Err(err).Str("key", r.key).Msg("keyverify recovery: re-request share")
			continue
		}
		t.bumpKeyverifyAction("req:" + r.key)
		stats.SharesRerequested++
	}

	return stats
}

func (t *TssCommon) bumpKeyverifyAction(key string) {
	t.unConfirmedMsgLock.Lock()
	defer t.unConfirmedMsgLock.Unlock()
	if t.kvRecovery == nil {
		return
	}
	t.kvRecovery.actionRetries[key]++
}
