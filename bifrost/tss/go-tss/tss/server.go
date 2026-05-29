package tss

import (
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keygen"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/keysign"
)

// Server define the necessary functionality should be provide by a TSS Server implementation
type Server interface {
	Start() error
	Stop()
	GetLocalPeerID() string
	GetKnownPeers() []PeerInfo
	Keygen(req keygen.Request) (keygen.Response, error)
	KeygenAllAlgo(req keygen.Request) ([]keygen.Response, error)
	KeySign(req keysign.Request) (keysign.Response, error)
	FrostKeySign(req FrostKeySignRequest, sidecar FrostSidecarClient) (FrostKeySignResponse, error)
	FrostKeygen(req FrostKeygenRequest, sidecar FrostSidecarKeygenClient) (FrostKeygenResponse, error)
	// GetP2PComm returns the underlying p2p.Communication so the keyverify
	// scheduler can construct TssKeyVerify instances against it.
	// TEMP: keyverify — remove with the keyverify rollout.
	GetP2PComm() *p2p.Communication
	// GetBroadcastChan returns the shared broadcast message channel.
	// TEMP: keyverify — remove with the keyverify rollout.
	GetBroadcastChan() chan *messages.BroadcastMsgChan
	// GetLocalNodePubKey returns the bech32-encoded thorpub of the local node.
	// TEMP: keyverify — remove with the keyverify rollout.
	GetLocalNodePubKey() string
}
