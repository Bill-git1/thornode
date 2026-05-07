package thorchain

import (
	cometed25519 "github.com/cometbft/cometbft/crypto/ed25519"
	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
)

type XMRMonovaultSuite struct{}

var _ = Suite(&XMRMonovaultSuite{})

func getRandomXMRFrostPubKey(c *C) common.PubKey {
	pubKey, err := common.NewPubKeyFromCrypto(cometed25519.GenPrivKey().PubKey())
	c.Assert(err, IsNil)
	_, err = pubKey.GetAddress(common.XMRChain)
	c.Assert(err, IsNil)
	return pubKey
}

func (s *XMRMonovaultSuite) TestShouldMaintainXMRMonovaultSingleValidator(c *C) {
	c.Assert(shouldMaintainXMRMonovault(nil, 1), Equals, true)
}

func (s *XMRMonovaultSuite) TestSelectActiveVaultForChainPrefersRegularTHORVaults(c *C) {
	ctx, k := setupKeeperForTest(c)
	regular := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain}.Strings(), nil)
	xmr := NewVaultV2(1, ActiveVault, AsgardVault, GetRandomPubKey(), xmrMonovaultChains().Strings(), nil, GetRandomPubKey())
	xmr.PubKeyFrost = getRandomXMRFrostPubKey(c)

	selected, err := selectActiveVaultForChain(ctx, k, Vaults{xmr, regular}, common.THORChain, 0)
	c.Assert(err, IsNil)
	c.Assert(selected.PubKey.Equals(regular.PubKey), Equals, true)
}

func (s *XMRMonovaultSuite) TestPreferredActiveVaultsForChainAcceptsXMRCapableVault(c *C) {
	hybrid := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.XMRChain, common.BTCChain}.Strings(), nil)
	hybrid.PubKeyFrost = getRandomXMRFrostPubKey(c)

	filtered := preferredActiveVaultsForChain(Vaults{hybrid}, common.XMRChain)
	c.Assert(filtered, HasLen, 1)
	c.Assert(filtered[0].PubKey.Equals(hybrid.PubKey), Equals, true)
}

func (s *XMRMonovaultSuite) TestPreferredActiveVaultsForChainRejectsXMRVaultWithoutFrost(c *C) {
	hybrid := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.XMRChain, common.BTCChain}.Strings(), nil)
	regular := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.BTCChain}.Strings(), nil)

	filtered := preferredActiveVaultsForChain(Vaults{regular, hybrid}, common.XMRChain)
	c.Assert(filtered, HasLen, 0)
}

func (s *XMRMonovaultSuite) TestFilterActiveVaultsForOutboundChainRejectsXMRVaultWithoutFrost(c *C) {
	hybrid := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.XMRChain, common.BTCChain}.Strings(), nil)
	regular := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.BTCChain}.Strings(), nil)

	filtered := filterActiveVaultsForOutboundChain(Vaults{regular, hybrid}, common.XMRChain)
	c.Assert(filtered, HasLen, 0)
}

func (s *XMRMonovaultSuite) TestFilterActiveVaultsForOutboundChainKeepsXMRCapableVaultForOtherChains(c *C) {
	hybrid := NewVault(1, ActiveVault, AsgardVault, GetRandomPubKey(), common.Chains{common.THORChain, common.XMRChain, common.BTCChain}.Strings(), nil)
	hybrid.PubKeyFrost = getRandomXMRFrostPubKey(c)

	filtered := filterActiveVaultsForOutboundChain(Vaults{hybrid}, common.BTCChain)
	c.Assert(filtered, HasLen, 1)
	c.Assert(filtered[0].PubKey.Equals(hybrid.PubKey), Equals, true)
}
