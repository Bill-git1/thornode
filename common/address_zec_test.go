//go:build mocknet
// +build mocknet

package common

import (
	"strings"

	. "gopkg.in/check.v1"
)

type ZECAddressSuite struct{}

var _ = Suite(&ZECAddressSuite{})

func (s *ZECAddressSuite) TestZECMainnetAddresses(c *C) {
	// Mainnet Sapling address (z) - in mocknet build, just check format
	addr, err := NewAddress("zU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vL")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.IsChain(BTCChain), Equals, false)
	c.Check(addr.IsChain(LTCChain), Equals, false)
	c.Check(addr.IsChain(ETHChain), Equals, false)

	// Mainnet Unified address (u)
	addr, err = NewAddress("uU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vL")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.IsChain(BTCChain), Equals, false)
	c.Check(addr.IsChain(LTCChain), Equals, false)
}

func (s *ZECAddressSuite) TestZECTestnetAddresses(c *C) {
	// Testnet P2PKH address (tm)
	addr, err := NewAddress("tmU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.IsChain(BTCChain), Equals, false)
	c.Check(addr.IsChain(ETHChain), Equals, false)
	c.Check(addr.GetNetwork(ZECChain), Equals, MockNet)

	// Testnet P2SH address (tn)
	addr, err = NewAddress("tnU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.IsChain(BTCChain), Equals, false)
	c.Check(addr.GetNetwork(ZECChain), Equals, MockNet)

	// Testnet Sapling address (ztestsapling)
	addr, err = NewAddress("ztestsaplingU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vL")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.GetNetwork(ZECChain), Equals, MockNet)

	// Testnet Unified address (utest)
	addr, err = NewAddress("utestU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.GetNetwork(ZECChain), Equals, MockNet)

	// Testnet Unified regtest address (uregtest)
	addr, err = NewAddress("uregtestU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(addr.GetNetwork(ZECChain), Equals, MockNet)
}

func (s *ZECAddressSuite) TestZECShieldedAddresses(c *C) {
	// Shielded addresses work in both mainnet and testnet mode - testing format
	addr, err := NewAddress("zU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vL")
	c.Check(err, IsNil)
	c.Check(addr.IsChain(ZECChain), Equals, true)
	c.Check(len(addr.String()) >= 26 && len(addr.String()) <= 95, Equals, true)

	// Verify the address type
	newAddr, _ := NewAddress(addr.String())
	c.Check(newAddr.IsChain(ZECChain), Equals, true)
}

func (s *ZECAddressSuite) TestZECAddressValidation(c *C) {
	// Too short - should fail in NewAddress
	_, err := NewAddress("t1a")
	c.Check(err, NotNil) // Error expected for short address

	// Too long - should fail (max 95 characters)
	longAddr := "tm" + strings.Repeat("A", 100) // Create address longer than 95 chars
	_, err = NewAddress(longAddr)
	c.Check(err, NotNil) // Error expected for long address

	// Invalid prefix - should fail
	_, err = NewAddress("tx123456789012345678901234567890123456789")
	c.Check(err, NotNil) // Error expected for invalid prefix
}

func (s *ZECAddressSuite) TestZECChainIdentification(c *C) {
	// Test that ZECChain addresses are correctly identified in GetChain()
	addr, err := NewAddress("tmU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5")
	c.Check(err, IsNil)
	chain := addr.GetChain()
	c.Check(chain, Equals, ZECChain)

	// Shielded ZEC should also be identified as ZECChain
	addr, err = NewAddress("ztestsaplingU6Z7Z5DZ5vLZ5vLZ5vLZ5vLZ5vLZ5vLZ5vL")
	c.Check(err, IsNil)
	chain = addr.GetChain()
	c.Check(chain, Equals, ZECChain)
}

func (s *ZECAddressSuite) TestZECNotChain(c *C) {
	// Test that non-ZEC addresses don't match ZECChain
	ethAddr, _ := NewAddress("0x90f2b1ae50e6018230e90a33f98c7844a0ab635a")
	c.Check(ethAddr.IsChain(ZECChain), Equals, false)

	btcAddr, _ := NewAddress("1MirQ9bwyQcGVJPwKUgapu5ouK2E2Ey4gX")
	c.Check(btcAddr.IsChain(ZECChain), Equals, false)

	thorAddr, _ := NewAddress("thor1kljxxccrheghavaw97u78le6yy3sdj7h696nl4")
	c.Check(thorAddr.IsChain(ZECChain), Equals, false)
}
