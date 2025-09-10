package types

import (
	"testing"

	. "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) { TestingT(t) }

type PubKeyTestSuite struct{}

var _ = Suite(&PubKeyTestSuite{})

func (s *PubKeyTestSuite) TestPubKeyFromString(c *C) {
	pkString := "BEcrPLugbAY1zEJGUNYgzcsxMi72rPeVwG6qKm96LK5g"
	pk, err := PublicKeyFromString(pkString)
	c.Assert(err, IsNil)
	c.Assert(pk.String(), Equals, "BEcrPLugbAY1zEJGUNYgzcsxMi72rPeVwG6qKm96LK5g")
}
