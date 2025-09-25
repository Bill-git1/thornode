package pubkeymanager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	openapi "gitlab.com/thorchain/thornode/v3/openapi/gen"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

func Test(t *testing.T) { TestingT(t) }

type PubKeyMgrSuite struct{}

var _ = Suite(&PubKeyMgrSuite{})

func (s *PubKeyMgrSuite) TestPubkeyMgr(c *C) {
	pk1 := types.GetRandomPubKey()
	pk2 := types.GetRandomPubKey()
	pk3 := types.GetRandomPubKey()
	pk4 := types.GetRandomPubKey()

	pubkeyMgr, err := NewPubKeyManager(nil, nil)
	c.Assert(err, IsNil)
	c.Check(pubkeyMgr.HasPubKey(pk1), Equals, false)
	pubkeyMgr.AddPubKey(pk1, true, common.SigningAlgoSecp256k1)
	c.Check(pubkeyMgr.HasPubKey(pk1), Equals, true)
	c.Check(pubkeyMgr.pubkeys[0].PubKey.Equals(pk1), Equals, true)
	c.Check(pubkeyMgr.pubkeys[0].Signer, Equals, true)

	pubkeyMgr.AddPubKey(pk2, false, common.SigningAlgoSecp256k1)
	c.Check(pubkeyMgr.HasPubKey(pk2), Equals, true)
	c.Check(pubkeyMgr.pubkeys[1].PubKey.Equals(pk2), Equals, true)
	c.Check(pubkeyMgr.pubkeys[1].Signer, Equals, false)

	pks := pubkeyMgr.GetPubKeys()
	c.Assert(pks, HasLen, 2)

	pks = pubkeyMgr.GetSignPubKeys()
	c.Assert(pks, HasLen, 1)
	c.Check(pks[0].Equals(pk1), Equals, true)

	// remove a pubkey
	pubkeyMgr.RemovePubKey(pk2)
	c.Check(pubkeyMgr.HasPubKey(pk2), Equals, false)

	addr, err := pk1.GetAddress(common.ETHChain)
	c.Assert(err, IsNil)
	ok, _ := pubkeyMgr.IsValidPoolAddress(addr.String(), common.ETHChain)
	c.Assert(ok, Equals, true)

	addr, err = pk3.GetAddress(common.ETHChain)
	c.Assert(err, IsNil)
	pubkeyMgr.AddNodePubKey(pk4, common.SigningAlgoSecp256k1)
	c.Check(pubkeyMgr.GetNodePubKey(common.SigningAlgoSecp256k1).String(), Equals, pk4.String())
	ok, _ = pubkeyMgr.IsValidPoolAddress(addr.String(), common.ETHChain)
	c.Assert(ok, Equals, false)
}

func (s *PubKeyMgrSuite) TestFetchKeys(c *C) {
	nodePk := types.GetRandomPubKey()
	vaultPk := types.GetRandomPubKey()
	extraPk := types.GetRandomPubKey()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.Logf("================>:%s", r.RequestURI)
		if r.RequestURI == "/thorchain/vaults/pubkeys" {
			var result openapi.VaultPubkeysResponse
			chain := common.ETHChain.String()
			router := "0xE65e9d372F8cAcc7b6dfcd4af6507851Ed31bb44"
			result.Asgard = append(result.Asgard, openapi.VaultInfo{
				PubKey: vaultPk.String(),
				Routers: []openapi.VaultRouter{
					{
						Chain:  &chain,
						Router: &router,
					},
				},
				Membership: []string{nodePk.String()},
			})
			buf, err := json.MarshalIndent(result, "", "	")
			c.Assert(err, IsNil)
			if _, err = w.Write(buf); err != nil {
				c.Error(err)
			}
		}
	}))

	cfg := config.BifrostClientConfiguration{
		ChainID:   "thorchain",
		ChainHost: server.URL[7:],
	}
	bridge, err := thorclient.NewThorchainBridge(cfg, nil, nil)
	c.Assert(err, IsNil)
	pubkeyMgr, err := NewPubKeyManager(bridge, nil)
	c.Assert(err, IsNil)
	hasCallbackFired := false
	callBack := func(pk common.PubKey) error {
		hasCallbackFired = true
		return nil
	}
	pubkeyMgr.RegisterCallback(callBack)

	// add extra old pubkey that should get pruned
	pubkeyMgr.AddPubKey(extraPk, false, common.SigningAlgoSecp256k1)
	c.Check(hasCallbackFired, Equals, true)

	// add a key that is the node account, ensure it is not pruned
	pubkeyMgr.pubkeys = append(pubkeyMgr.pubkeys, pubKeyInfo{
		PubKey:      nodePk,
		Signer:      true,
		NodeAccount: true,
		Algo:        common.SigningAlgoSecp256k1,
		Contracts:   map[common.Chain]common.Address{},
	})
	c.Check(len(pubkeyMgr.GetPubKeys()), Equals, 2)
	err = pubkeyMgr.Start()
	c.Assert(err, IsNil)

	// fetch without prune
	pubkeyMgr.fetchPubKeys(false)
	pubKeys := pubkeyMgr.GetPubKeys()
	c.Check(len(pubKeys), Equals, 3)
	c.Check(pubKeys[0].Equals(extraPk), Equals, true)
	c.Check(pubKeys[1].Equals(nodePk), Equals, true)
	c.Check(pubKeys[2].Equals(vaultPk), Equals, true)
	c.Check(pubkeyMgr.pubkeys[0].Signer, Equals, false)
	c.Check(pubkeyMgr.pubkeys[1].Signer, Equals, true)
	c.Check(pubkeyMgr.pubkeys[2].Signer, Equals, true)
	c.Check(pubkeyMgr.pubkeys[0].NodeAccount, Equals, false)
	c.Check(pubkeyMgr.pubkeys[1].NodeAccount, Equals, true)
	c.Check(pubkeyMgr.pubkeys[2].NodeAccount, Equals, false)

	// fetch with prune, add 2 extra keys to ensure all the prior ones are in prune range
	pubkeyMgr.AddPubKey(types.GetRandomPubKey(), false, common.SigningAlgoSecp256k1)
	pubkeyMgr.AddPubKey(types.GetRandomPubKey(), false, common.SigningAlgoSecp256k1)
	pubkeyMgr.fetchPubKeys(true)
	pubKeys = pubkeyMgr.GetPubKeys()
	c.Check(len(pubKeys), Equals, 4)
	c.Check(pubKeys[0].Equals(extraPk), Equals, false) // pruned
	c.Check(pubKeys[1].Equals(nodePk), Equals, true)
	c.Check(pubKeys[2].Equals(vaultPk), Equals, true)
	c.Check(pubkeyMgr.pubkeys[0].Signer, Equals, false)
	c.Check(pubkeyMgr.pubkeys[1].Signer, Equals, true)
	c.Check(pubkeyMgr.pubkeys[2].Signer, Equals, true)
	c.Check(pubkeyMgr.pubkeys[3].Signer, Equals, false)
	c.Check(pubkeyMgr.pubkeys[0].NodeAccount, Equals, false)
	c.Check(pubkeyMgr.pubkeys[1].NodeAccount, Equals, true)
	c.Check(pubkeyMgr.pubkeys[2].NodeAccount, Equals, false)
	c.Check(pubkeyMgr.pubkeys[3].NodeAccount, Equals, false)

	err = pubkeyMgr.Stop()
	c.Assert(err, IsNil)
}
