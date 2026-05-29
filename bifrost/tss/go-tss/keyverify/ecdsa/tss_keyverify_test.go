// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ipfs/go-log"

	tcrypto "github.com/cometbft/cometbft/crypto"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	"github.com/libp2p/go-libp2p-core/peer"

	maddr "github.com/multiformats/go-multiaddr"
	btsskeygen "gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/p2p"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/conversion"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/tss/go-tss/common"
)

var (
	testPubKeys = []string{
		"thorpub1addwnpepq2ryyje5zr09lq7gqptjwnxqsy2vcdngvwd6z7yt5yjcnyj8c8cn559xe69",
		"thorpub1addwnpepqfjcw5l4ay5t00c32mmlky7qrppepxzdlkcwfs2fd5u73qrwna0vzag3y4j",
		"thorpub1addwnpepqtdklw8tf3anjz7nn5fly3uvq2e67w2apn560s4smmrt9e3x52nt2svmmu3",
		"thorpub1addwnpepqtspqyy6gk22u37ztra4hq3hdakc0w0k60sfy849mlml2vrpfr0wvm6uz09",
	}
	testPriKeyArr = []string{
		"6LABmWB4iXqkqOJ9H0YFEA2CSSx6bA7XAKGyI/TDtas=",
		"528pkgjuCWfHx1JihEjiIXS7jfTS/viEdAbjqVvSifQ=",
		"JFB2LIJZtK+KasK00NcNil4PRJS4c4liOnK0nDalhqc=",
		"vLMGhVXMOXQVnAE3BUU8fwNj/q0ZbndKkwmxfS5EN9Y=",
	}

	testNodePrivkey = []string{
		"ZThiMDAxOTk2MDc4ODk3YWE0YThlMjdkMWY0NjA1MTAwZDgyNDkyYzdhNmMwZWQ3MDBhMWIyMjNmNGMzYjVhYg==",
		"ZTc2ZjI5OTIwOGVlMDk2N2M3Yzc1MjYyODQ0OGUyMjE3NGJiOGRmNGQyZmVmODg0NzQwNmUzYTk1YmQyODlmNA==",
		"MjQ1MDc2MmM4MjU5YjRhZjhhNmFjMmI0ZDBkNzBkOGE1ZTBmNDQ5NGI4NzM4OTYyM2E3MmI0OWMzNmE1ODZhNw==",
		"YmNiMzA2ODU1NWNjMzk3NDE1OWMwMTM3MDU0NTNjN2YwMzYzZmVhZDE5NmU3NzRhOTMwOWIxN2QyZTQ0MzdkNg==",
	}

	targets = []string{
		"16Uiu2HAmACG5DtqmQsHtXg4G2sLS65ttv84e7MrL4kapkjfmhxAp", "16Uiu2HAm4TmEzUqy3q3Dv7HvdoSboHk5sFj2FH3npiN5vDbJC6gh",
		"16Uiu2HAm2FzqoUdS6Y9Esg2EaGcAG5rVe1r6BFNnmmQr2H3bqafa",
	}
)

func TestPackage(t *testing.T) { TestingT(t) }

type TssECDSAKeyverifyTestSuite struct {
	comms        []*p2p.Communication
	preParams    []*btsskeygen.LocalPreParams
	partyNum     int
	stateMgrs    []storage.LocalStateManager
	nodePrivKeys []tcrypto.PrivKey
	targePeers   []peer.ID
}

var _ = Suite(&TssECDSAKeyverifyTestSuite{})

func (s *TssECDSAKeyverifyTestSuite) SetUpSuite(c *C) {
	common.InitLog("info", true, "keyverify_test")
	conversion.SetupBech32Prefix()
	for _, el := range testNodePrivkey {
		priHexBytes, err := base64.StdEncoding.DecodeString(el)
		c.Assert(err, IsNil)
		rawBytes, err := hex.DecodeString(string(priHexBytes))
		c.Assert(err, IsNil)
		var priKey secp256k1.PrivKey
		priKey = rawBytes[:32]
		s.nodePrivKeys = append(s.nodePrivKeys, priKey)
	}

	for _, el := range targets {
		p, err := peer.Decode(el)
		c.Assert(err, IsNil)
		s.targePeers = append(s.targePeers, p)
	}
}

func (s *TssECDSAKeyverifyTestSuite) TearDownSuite(c *C) {
	for i := range s.comms {
		tempFilePath := path.Join(os.TempDir(), strconv.Itoa(i), "ecdsa")
		err := os.RemoveAll(tempFilePath)
		c.Assert(err, IsNil)
	}
}

// SetUpTest set up environment for test key verify
func (s *TssECDSAKeyverifyTestSuite) SetUpTest(c *C) {
	ports := []int{
		18766, 18767, 18768, 18769,
	}
	s.partyNum = 4
	s.comms = make([]*p2p.Communication, s.partyNum)
	s.stateMgrs = make([]storage.LocalStateManager, s.partyNum)
	bootstrapPeer := "/ip4/127.0.0.1/tcp/18766/p2p/16Uiu2HAm4TmEzUqy3q3Dv7HvdoSboHk5sFj2FH3npiN5vDbJC6gh"
	multiAddr, err := maddr.NewMultiaddr(bootstrapPeer)
	c.Assert(err, IsNil)
	s.preParams = getPreparams(c)
	for i := 0; i < s.partyNum; i++ {
		buf, err := base64.StdEncoding.DecodeString(testPriKeyArr[i])
		c.Assert(err, IsNil)
		if i == 0 {
			comm, err := p2p.NewCommunication(&p2p.Config{Port: ports[i], RendezvousString: "asgard"}, nil, nil)
			c.Assert(err, IsNil)
			c.Assert(comm.Start(buf[:]), IsNil)
			s.comms[i] = comm
			continue
		}
		comm, err := p2p.NewCommunication(&p2p.Config{Port: ports[i], RendezvousString: "asgard", BootstrapPeers: []maddr.Multiaddr{multiAddr}}, nil, nil)
		c.Assert(err, IsNil)
		c.Assert(comm.Start(buf[:]), IsNil)
		s.comms[i] = comm
	}

	for i := 0; i < s.partyNum; i++ {
		baseHome := path.Join(os.TempDir(), "ecdsa", strconv.Itoa(i))
		fMgr, err := storage.NewFileStateMgr(baseHome)
		c.Assert(err, IsNil)
		s.stateMgrs[i] = fMgr
	}
}

func (s *TssECDSAKeyverifyTestSuite) TearDownTest(c *C) {
	time.Sleep(time.Second)
	for _, item := range s.comms {
		c.Assert(item.Stop(), IsNil)
	}
}

func getPreparams(c *C) []*btsskeygen.LocalPreParams {
	const (
		testFileLocation = "../../test_data"
		preParamTestFile = "preParam_test.data"
	)
	var preParamArray []*btsskeygen.LocalPreParams
	buf, err := ioutil.ReadFile(path.Join(testFileLocation, preParamTestFile))
	c.Assert(err, IsNil)
	preParamsStr := strings.Split(string(buf), "\n")
	for _, item := range preParamsStr {
		var preParam btsskeygen.LocalPreParams
		val, err := hex.DecodeString(item)
		c.Assert(err, IsNil)
		c.Assert(json.Unmarshal(val, &preParam), IsNil)
		preParamArray = append(preParamArray, &preParam)
	}
	return preParamArray
}

// TestKeyverifyHappyPath runs keygen across 4 nodes, then runs keyverify
// against the resulting keyshares and asserts every party returns nil.
func (s *TssECDSAKeyverifyTestSuite) TestKeyverifyHappyPath(c *C) {
	log.SetLogLevel("tss-lib", "info")
	sort.Strings(testPubKeys)
	conf := defaultVerifyConf()

	pubkey := s.runKeygen(c, conf)
	errs := s.runKeyverify(c, conf, pubkey, s.stateMgrsCopy(), "happy")
	for i, err := range errs {
		c.Assert(err, IsNil, Commentf("party %d: %v", i, err))
	}
}

// TestKeyverify_TamperedECDSAPub replaces party 2's saved ECDSAPub with
// 2*G, leaving every other party with the real pool pubkey. The Round 4
// Paillier-N proof party 2 generates is bound to the wrong ECDSAPub, so
// the other parties' proof verification fails and the protocol surfaces
// a *btss.Error with culprits=[party 2] via the endCh path. This is the
// path that production cmd/bifrost/main.go::extractCulprits handles and
// that produces a .fail marker for /status/keyverify.
//
// The test asserts both:
//   - At least 3 honest parties return *btss.Error blaming party 2 (raw
//     PartyID).
//   - The same index resolves to testPubKeys[tamperedIdx] when run
//     through the same sorted-index lookup main.go uses — i.e. the
//     .fail marker JSON will name the actual bad-share peer, not an
//     opaque "2" id.
//
// Note on PaillierN tamper: corrupting save.PaillierPKs[partyIdx].N also
// causes the protocol to detect a mismatch, but in Round 2 it surfaces as
// a "fail to apply the share to tss" error inside tssCommon's inbound
// pipeline and never reaches the endCh — the other parties stall in
// Round 2 and timeout instead. That path is intentionally not covered
// here because production extractCulprits only handles *btss.Error;
// timeouts are treated as "peers offline" and produce no .fail marker.
// See REMOVAL.md / spec for the timeout-vs-culprit dispatch matrix.
func (s *TssECDSAKeyverifyTestSuite) TestKeyverify_TamperedECDSAPub(c *C) {
	log.SetLogLevel("tss-lib", "info")
	sort.Strings(testPubKeys)
	conf := defaultVerifyConf()

	pubkey := s.runKeygen(c, conf)

	const tamperedIdx = 2
	mgrs := s.stateMgrsCopy()
	mgrs[tamperedIdx] = &tamperingStateMgr{
		inner:  s.stateMgrs[tamperedIdx],
		mutate: tamperECDSAPub(),
	}

	errs := s.runKeyverify(c, conf, pubkey, mgrs, "tampered-ecdsa-pub")
	assertBadShareBlame(c, errs, strconv.Itoa(tamperedIdx), 3)

	// Bridge to the production .fail JSON pipeline: run the index → thorpub
	// resolution main.go::extractCulprits does and confirm the resolved
	// culprit pubkey is the bad-share peer's thorpub.
	members := append([]string(nil), testPubKeys[:s.partyNum]...)
	sort.Strings(members)
	mapped := 0
	for i, err := range errs {
		if i == tamperedIdx || err == nil {
			continue
		}
		thorpubs := mapCulpritsToThorpubs(err, members)
		if thorpubs == nil {
			continue
		}
		found := false
		for _, pk := range thorpubs {
			if pk == testPubKeys[tamperedIdx] {
				found = true
				break
			}
		}
		c.Assert(found, Equals, true,
			Commentf("party %d culprits=%v missing expected thorpub %s", i, thorpubs, testPubKeys[tamperedIdx]))
		mapped++
	}
	c.Assert(mapped >= 1, Equals, true,
		Commentf("expected at least one honest party to surface a *btss.Error mappable to thorpubs"))
}
