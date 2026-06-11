package p2p

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"github.com/libp2p/go-libp2p-core/peer"
	maddr "github.com/multiformats/go-multiaddr"

	"github.com/libp2p/go-libp2p-core/crypto"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

type CommunicationTestSuite struct{}

var _ = Suite(&CommunicationTestSuite{})

func (CommunicationTestSuite) TestBasicCommunication(c *C) {
	comm, err := NewCommunication(&Config{Port: 6688, RendezvousString: "rendezvous"}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(comm, NotNil)
	comm.SetSubscribe(messages.TSSKeyGenMsg, "hello", make(chan *Message))
	c.Assert(comm.getSubscriber(messages.TSSKeySignMsg, "hello"), IsNil)
	c.Assert(comm.getSubscriber(messages.TSSKeyGenMsg, "hello"), NotNil)
	comm.CancelSubscribe(messages.TSSKeyGenMsg, "hello")
	comm.CancelSubscribe(messages.TSSKeyGenMsg, "whatever")
	comm.CancelSubscribe(messages.TSSKeySignMsg, "asdsdf")
}

func checkExist(a []maddr.Multiaddr, b string) bool {
	for _, el := range a {
		if el.String() == b {
			return true
		}
	}
	return false
}

func (CommunicationTestSuite) TestEstablishP2pCommunication(c *C) {
	bootstrapPeer := "/ip4/127.0.0.1/tcp/2220/p2p/16Uiu2HAm4TmEzUqy3q3Dv7HvdoSboHk5sFj2FH3npiN5vDbJC6gh"
	bootstrapPrivKey := "6LABmWB4iXqkqOJ9H0YFEA2CSSx6bA7XAKGyI/TDtas="
	fakeExternalIP := "11.22.33.44"
	fakeExternalMultiAddr := "/ip4/11.22.33.44/tcp/2220"
	validMultiAddr, err := maddr.NewMultiaddr(bootstrapPeer)
	c.Assert(err, IsNil)
	privKey, err := base64.StdEncoding.DecodeString(bootstrapPrivKey)
	c.Assert(err, IsNil)
	comm, err := NewCommunication(&Config{Port: 2220, RendezvousString: "commTest", ExternalIP: fakeExternalIP}, nil, nil)
	c.Assert(err, IsNil)
	c.Assert(comm.Start(privKey), IsNil)

	defer func() {
		stopErr := comm.Stop()
		c.Assert(stopErr, IsNil)
	}()
	sk1, _, err := crypto.GenerateSecp256k1Key(rand.Reader)
	sk1raw, _ := sk1.Raw()
	c.Assert(err, IsNil)
	comm2, err := NewCommunication(&Config{Port: 2221, RendezvousString: "commTest", BootstrapPeers: []maddr.Multiaddr{validMultiAddr}}, nil, nil)
	c.Assert(err, IsNil)
	err = comm2.Start(sk1raw)
	c.Assert(err, IsNil)
	defer func() {
		stopErr := comm2.Stop()
		c.Assert(stopErr, IsNil)
	}()

	// Configured bootstrap peers still fail fast by default so production
	// misconfiguration remains visible.
	sk2, _, err := crypto.GenerateSecp256k1Key(rand.Reader)
	c.Assert(err, IsNil)
	id, err := peer.IDFromPrivateKey(sk2)
	c.Assert(err, IsNil)
	invalidAddr := "/ip4/127.0.0.1/tcp/2220/p2p/" + id.String()
	invalidMultiAddr, err := maddr.NewMultiaddr(invalidAddr)
	c.Assert(err, IsNil)
	comm3, err := NewCommunication(&Config{Port: 2222, RendezvousString: "commTest", BootstrapPeers: []maddr.Multiaddr{invalidMultiAddr}}, nil, nil)
	c.Assert(err, IsNil)
	err = comm3.Start(sk1raw)
	if allowUnreachableConfiguredBootstrapPeers {
		c.Assert(err, IsNil)
		defer func() {
			stopErr := comm3.Stop()
			c.Assert(stopErr, IsNil)
		}()
	} else {
		c.Assert(err, ErrorMatches, "fail to connect to bootstrap peer: fail to connect to any peer")
		c.Assert(comm3.Stop(), IsNil)
	}

	// we connect to one invalid and one valid address
	comm4, err := NewCommunication(&Config{Port: 2223, RendezvousString: "commTest", BootstrapPeers: []maddr.Multiaddr{invalidMultiAddr, validMultiAddr}}, nil, nil)
	c.Assert(err, IsNil)
	err = comm4.Start(sk1raw)
	c.Assert(err, IsNil)
	defer func() {
		err := comm4.Stop()
		c.Assert(err, IsNil)
	}()

	// we add test for external ip advertising
	c.Assert(checkExist(comm.host.Addrs(), fakeExternalMultiAddr), Equals, true)
	ps := comm2.host.Peerstore()
	c.Assert(checkExist(ps.Addrs(comm.host.ID()), fakeExternalMultiAddr), Equals, true)
	ps = comm4.host.Peerstore()
	c.Assert(checkExist(ps.Addrs(comm.host.ID()), fakeExternalMultiAddr), Equals, true)
}

// TestConnectToBootstrapPeersSelfDoesNotDeadlock is a regression test for the
// connRet read-deadlock in connectToBootstrapPeers.
//
// Bug: the for-loop reading from connRet iterated len(bootstrapPeers) times,
// but goroutines were skipped for self entries (`if pi.ID == c.host.ID()`),
// so the read for the self slot blocked forever waiting for a send that never
// came. This is the exact configuration produced after a TSS keysign
// SaveAddressBook, which writes self into address_book.seed.
//
// Fix: track dialAttempts separately and read exactly that many.
//
// This test constructs bootstrap peers containing the host's own peer ID
// plus an unreachable peer, calls Start(), and asserts it returns within a
// timeout instead of hanging.
func (CommunicationTestSuite) TestConnectToBootstrapPeersSelfDoesNotDeadlock(c *C) {
	// Generate a key for our host so we can derive its peer ID and put it in
	// the bootstrap list.
	priv, _, err := crypto.GenerateSecp256k1Key(rand.Reader)
	c.Assert(err, IsNil)
	privRaw, err := priv.Raw()
	c.Assert(err, IsNil)
	selfID, err := peer.IDFromPrivateKey(priv)
	c.Assert(err, IsNil)
	selfAddr, err := maddr.NewMultiaddr("/ip4/127.0.0.1/tcp/2230/p2p/" + selfID.String())
	c.Assert(err, IsNil)

	// An unreachable peer with a different ID, so dialAttempts > 0 and we
	// actually exercise the read loop after wg.Wait. (If self were the only
	// entry, dialAttempts would be 0 and the early return would mask the bug.)
	otherPriv, _, err := crypto.GenerateSecp256k1Key(rand.Reader)
	c.Assert(err, IsNil)
	otherID, err := peer.IDFromPrivateKey(otherPriv)
	c.Assert(err, IsNil)
	otherAddr, err := maddr.NewMultiaddr("/ip4/127.0.0.1/tcp/2231/p2p/" + otherID.String())
	c.Assert(err, IsNil)

	comm, err := NewCommunication(&Config{
		Port:             2230,
		RendezvousString: "selfDeadlockTest",
		BootstrapPeers:   []maddr.Multiaddr{selfAddr, otherAddr},
	}, nil, nil)
	c.Assert(err, IsNil)

	// Start in a goroutine so we can enforce a timeout.
	startDone := make(chan error, 1)
	go func() {
		startDone <- comm.Start(privRaw)
	}()

	select {
	case startErr := <-startDone:
		if allowUnreachableConfiguredBootstrapPeers {
			c.Assert(startErr, IsNil)
		} else {
			c.Assert(startErr, ErrorMatches, "fail to connect to bootstrap peer: fail to connect to any peer")
		}
	case <-time.After(60 * time.Second):
		c.Fatalf("Start() deadlocked when self was in bootstrap peers list")
	}

	stopErr := comm.Stop()
	c.Assert(stopErr, IsNil)
}
