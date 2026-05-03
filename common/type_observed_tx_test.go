package common

import (
	"strings"

	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	. "gopkg.in/check.v1"
)

type ObservedTxSuite struct{}

var _ = Suite(&ObservedTxSuite{})

const (
	testSpendTxHash  = "abcd000000000000000000000000000000000000000000000000000000000001"
	testSpendTxHash2 = "abcd000000000000000000000000000000000000000000000000000000000002"
	testRefTxHashA   = "ef01000000000000000000000000000000000000000000000000000000000001"
	testRefTxHashB   = "ef01000000000000000000000000000000000000000000000000000000000002"
	testKeyImageA    = "ki01000000000000000000000000000000000000000000000000000000000001"
	testKeyImageB    = "ki02000000000000000000000000000000000000000000000000000000000001"
	testPubKey       = PubKey("thorpub1addwnpepqfshsq2y6ejy2ysxmq4gj8n8mzuzyulk9wh4n946jv5w2vpwdn2yuyp6sp4")
)

func makeRef(txHash string, idx uint32, keyImage, spendTxHash string) OutputRef {
	return OutputRef{
		TxHash:      txHash,
		OutputIndex: idx,
		KeyImage:    keyImage,
		SpendTxHash: spendTxHash,
	}
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_Empty(c *C) {
	c.Assert(ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), nil), IsNil)
	c.Assert(ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), []OutputRef{}), IsNil)
	c.Assert(ValidateSpentOutputRefs(BTCChain, TxID(testSpendTxHash), nil), IsNil)
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_NonXMRChain(c *C) {
	refs := []OutputRef{makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)}
	err := ValidateSpentOutputRefs(BTCChain, TxID(testSpendTxHash), refs)
	c.Assert(err, ErrorMatches, "spent output refs are only valid for XMR observed txs")
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_OverCap(c *C) {
	refs := make([]OutputRef, MaxXMRSpentOutputRefs+1)
	for i := range refs {
		refs[i] = makeRef(testRefTxHashA, uint32(i), testKeyImageA, testSpendTxHash)
	}
	err := ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), refs)
	c.Assert(err, ErrorMatches, "spent output refs exceeds max 32")
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_EmptyFields(c *C) {
	tests := []struct {
		ref    OutputRef
		errSub string
	}{
		{makeRef("", 0, testKeyImageA, testSpendTxHash), "tx hash cannot be empty"},
		{makeRef("   ", 0, testKeyImageA, testSpendTxHash), "tx hash cannot be empty"},
		{makeRef(testRefTxHashA, 0, "", testSpendTxHash), "key image cannot be empty"},
		{makeRef(testRefTxHashA, 0, testKeyImageA, ""), "spend tx hash cannot be empty"},
	}
	for _, tc := range tests {
		err := ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), []OutputRef{tc.ref})
		c.Assert(err, NotNil)
		c.Assert(strings.Contains(err.Error(), tc.errSub), Equals, true,
			Commentf("expected %q in %v", tc.errSub, err))
	}
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_MismatchedSpendTxHash(c *C) {
	refs := []OutputRef{makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash2)}
	err := ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), refs)
	c.Assert(err, NotNil)
	c.Assert(strings.Contains(err.Error(), "does not match observed tx"), Equals, true)
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_DuplicateSameCase(c *C) {
	ref := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	err := ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), []OutputRef{ref, ref})
	c.Assert(err, NotNil)
	c.Assert(strings.Contains(err.Error(), "duplicate spent output ref"), Equals, true)
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_DuplicateCaseInsensitive(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	b := makeRef(strings.ToUpper(testRefTxHashA), 0, strings.ToUpper(testKeyImageA), testSpendTxHash)
	err := ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), []OutputRef{a, b})
	c.Assert(err, NotNil)
	c.Assert(strings.Contains(err.Error(), "duplicate spent output ref"), Equals, true)
}

func (s ObservedTxSuite) TestValidateSpentOutputRefs_HappyPath(c *C) {
	refs := []OutputRef{
		makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash),
		makeRef(testRefTxHashB, 1, testKeyImageB, testSpendTxHash),
	}
	c.Assert(ValidateSpentOutputRefs(XMRChain, TxID(testSpendTxHash), refs), IsNil)
}

// helpers for consensus-equality tests

func makeXMRObservedTx(c *C, refs []OutputRef) ObservedTx {
	xmrAddr := XMRSeededDummyAddress(0x12, []byte("from-seed"))
	to := XMRSeededDummyAddress(0x12, []byte("to-seed"))
	id, err := NewTxID(testSpendTxHash)
	c.Assert(err, IsNil)
	tx := NewTx(
		id,
		Address(xmrAddr),
		Address(to),
		Coins{NewCoin(XMRAsset, cosmos.NewUint(One))},
		Gas{NewCoin(XMRAsset, cosmos.NewUint(1000))},
		"=:THOR.RUNE",
	)
	tx.Chain = XMRChain
	otx := NewObservedTx(tx, 100, testPubKey, 100)
	otx.SpentOutputRefs = refs
	return otx
}

func (s ObservedTxSuite) TestEqualsConsensus_SameRefsDifferentOrder(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	b := makeRef(testRefTxHashB, 1, testKeyImageB, testSpendTxHash)
	tx1 := makeXMRObservedTx(c, []OutputRef{a, b})
	tx2 := makeXMRObservedTx(c, []OutputRef{b, a})
	c.Assert(tx1.EqualsConsensus(tx2), Equals, true)
	c.Assert(tx1.EqualsConsensusIgnoreGas(tx2), Equals, true)
	c.Assert(tx1.HasEquivalentSpentOutputRefs(tx2), Equals, true)
}

func (s ObservedTxSuite) TestEqualsConsensus_OneRefDiffers(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	b := makeRef(testRefTxHashB, 1, testKeyImageB, testSpendTxHash)
	other := makeRef(testRefTxHashB, 1, "ki03000000000000000000000000000000000000000000000000000000000001", testSpendTxHash)
	tx1 := makeXMRObservedTx(c, []OutputRef{a, b})
	tx2 := makeXMRObservedTx(c, []OutputRef{a, other})
	c.Assert(tx1.EqualsConsensus(tx2), Equals, false)
	c.Assert(tx1.HasEquivalentSpentOutputRefs(tx2), Equals, false)
}

func (s ObservedTxSuite) TestEqualsConsensus_BothNonXMREmpty(c *C) {
	id, err := NewTxID(testSpendTxHash)
	c.Assert(err, IsNil)
	tx := NewTx(id,
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a"),
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635b"),
		Coins{NewCoin(ETHAsset, cosmos.NewUint(5*One))},
		Gas{NewCoin(ETHAsset, cosmos.NewUint(10000))},
		"=:THOR.RUNE",
	)
	pk := testPubKey
	a := NewObservedTx(tx, 100, pk, 100)
	b := NewObservedTx(tx, 100, pk, 100)
	c.Assert(a.EqualsConsensus(b), Equals, true)
	c.Assert(a.HasEquivalentSpentOutputRefs(b), Equals, true)
}

func (s ObservedTxSuite) TestAttestationUniqueHash_NonXMREqualsBase(c *C) {
	id, err := NewTxID(testSpendTxHash)
	c.Assert(err, IsNil)
	tx := NewTx(id,
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a"),
		Address("0x90f2b1ae50e6018230e90a33f98c7844a0ab635b"),
		Coins{NewCoin(ETHAsset, cosmos.NewUint(5*One))},
		Gas{NewCoin(ETHAsset, cosmos.NewUint(10000))},
		"=:THOR.RUNE",
	)
	otx := NewObservedTx(tx, 100, testPubKey, 100)
	c.Assert(otx.AttestationUniqueHash(), Equals, otx.Tx.Hash(otx.BlockHeight))
}

func (s ObservedTxSuite) TestAttestationUniqueHash_XMREmptyRefsEqualsBase(c *C) {
	otx := makeXMRObservedTx(c, nil)
	c.Assert(otx.AttestationUniqueHash(), Equals, otx.Tx.Hash(otx.BlockHeight))
}

func (s ObservedTxSuite) TestAttestationUniqueHash_XMRWithRefsDiffersFromBase(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	otx := makeXMRObservedTx(c, []OutputRef{a})
	c.Assert(otx.AttestationUniqueHash() != otx.Tx.Hash(otx.BlockHeight), Equals, true)
}

func (s ObservedTxSuite) TestAttestationUniqueHash_DeterministicAcrossOrder(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	b := makeRef(testRefTxHashB, 1, testKeyImageB, testSpendTxHash)
	tx1 := makeXMRObservedTx(c, []OutputRef{a, b})
	tx2 := makeXMRObservedTx(c, []OutputRef{b, a})
	c.Assert(tx1.AttestationUniqueHash(), Equals, tx2.AttestationUniqueHash())
}

func (s ObservedTxSuite) TestAttestationUniqueHash_DiffersOnDifferentRefs(c *C) {
	a := makeRef(testRefTxHashA, 0, testKeyImageA, testSpendTxHash)
	b := makeRef(testRefTxHashB, 1, testKeyImageB, testSpendTxHash)
	tx1 := makeXMRObservedTx(c, []OutputRef{a})
	tx2 := makeXMRObservedTx(c, []OutputRef{b})
	c.Assert(tx1.AttestationUniqueHash() != tx2.AttestationUniqueHash(), Equals, true)
}
