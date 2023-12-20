//go:build !stagenet && !mocknet
// +build !stagenet,!mocknet

package bitcoin

import (
	"github.com/btcsuite/btcd/chaincfg"
	. "gopkg.in/check.v1"
)

func (s *BitcoinSignerSuite) TestGetChainCfg(c *C) {
	param := s.client.getChainCfg()
	c.Assert(param, Equals, &chaincfg.MainNetParams)
}
