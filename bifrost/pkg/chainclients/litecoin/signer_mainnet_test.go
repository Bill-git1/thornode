//go:build !stagenet && !mocknet
// +build !stagenet,!mocknet

package litecoin

import (
	"github.com/ltcsuite/ltcd/chaincfg"
	. "gopkg.in/check.v1"
)

func (s *LitecoinSignerSuite) TestGetChainCfg(c *C) {
	param := s.client.getChainCfg()
	c.Assert(param, Equals, &chaincfg.MainNetParams)
}
