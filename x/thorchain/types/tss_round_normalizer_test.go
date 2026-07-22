package types

import (
	. "gopkg.in/check.v1"

	tssMessages "gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"
)

type TssRoundNormalizerSuite struct{}

var _ = Suite(&TssRoundNormalizerSuite{})

func (s *TssRoundNormalizerSuite) TestNormalizeTssKeysignRoundAcceptsFrostRounds(c *C) {
	validRounds := []string{
		tssMessages.FROSTKEYSIGNPREPROCESS,
		tssMessages.FROSTKEYSIGNSHARE,
		tssMessages.FROSTKEYSIGNRESULT,
		tssMessages.KEYSIGN9,
		tssMessages.EDDSAKEYSIGN3,
	}

	for _, round := range validRounds {
		normalized, err := NormalizeTssKeysignRound(round)
		c.Assert(err, IsNil)
		c.Assert(normalized, Equals, round)
	}

	normalized, err := NormalizeTssKeysignRound("bogus_round")
	c.Assert(err, NotNil)
	c.Assert(normalized, Equals, "")
}
