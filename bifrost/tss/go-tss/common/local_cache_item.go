package common

import (
	"sync"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

// LocalCacheItem used to cache the unconfirmed broadcast message
type LocalCacheItem struct {
	Msg           *messages.WireMessage
	Hash          string
	lock          *sync.Mutex
	ConfirmedList map[string]string
}

func NewLocalCacheItem(msg *messages.WireMessage, hash string) *LocalCacheItem {
	return &LocalCacheItem{
		Msg:           msg,
		Hash:          hash,
		lock:          &sync.Mutex{},
		ConfirmedList: make(map[string]string),
	}
}

// UpdateConfirmList add the given party's hash into the confirm list
func (l *LocalCacheItem) UpdateConfirmList(P2PID, hash string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	l.ConfirmedList[P2PID] = hash
}

// snapshotMsgAndConfirms returns the current Msg pointer and a copy of the
// ConfirmedList, read together under the item lock.
// TEMP: keyverify — the recovery ticker is the only reader of cache items
// outside the inbound goroutine; it must never iterate the live ConfirmedList
// (concurrent with UpdateConfirmList it is a runtime fatal) nor read Msg
// unsynchronized. Pairs with the locked Msg writes in tss.go.
func (l *LocalCacheItem) snapshotMsgAndConfirms() (*messages.WireMessage, map[string]string) {
	l.lock.Lock()
	defer l.lock.Unlock()
	confirms := make(map[string]string, len(l.ConfirmedList))
	for k, v := range l.ConfirmedList {
		confirms[k] = v
	}
	return l.Msg, confirms
}

// TotalConfirmParty number of parties that already confirmed their hash
func (l *LocalCacheItem) TotalConfirmParty() int {
	l.lock.Lock()
	defer l.lock.Unlock()
	return len(l.ConfirmedList)
}

func (l *LocalCacheItem) GetPeers() []string {
	peers := make([]string, 0, len(l.ConfirmedList))
	l.lock.Lock()
	defer l.lock.Unlock()
	for peer := range l.ConfirmedList {
		peers = append(peers, peer)
	}
	return peers
}
