package thorclient

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	. "gopkg.in/check.v1"
)

type FreshQuerySuite struct{}

var _ = Suite(&FreshQuerySuite{})

func newFreshQueryBridge(c *C, server *httptest.Server) *thorchainBridge {
	cfg, _, kb := SetupThorchainForTest(c)
	cfg.ChainHost = strings.TrimPrefix(server.URL, "http://")
	cfg.ChainRPC = cfg.ChainHost

	bridge, err := NewThorchainBridge(cfg, GetMetricForTest(c), NewKeysWithKeybase(kb, cfg.SignerName, cfg.SignerPasswd))
	c.Assert(err, IsNil)
	concrete, ok := bridge.(*thorchainBridge)
	c.Assert(ok, Equals, true)
	concrete.httpClient.RetryMax = 0
	return concrete
}

func (s *FreshQuerySuite) TestFreshVoterBypassesButDoesNotReplaceCache(c *C) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		requests++
		reverted := requests > 1
		_, err := fmt.Fprintf(rw, `{"tx_id":"%s","reverted":%t}`, strings.Repeat("A", 64), reverted)
		c.Assert(err, IsNil)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	txID := strings.Repeat("A", 64)

	cached, err := bridge.GetObservedTxVoter(txID)
	c.Assert(err, IsNil)
	c.Assert(cached.Reverted, Equals, false)

	fresh, found, err := bridge.GetObservedTxVoterUncached(txID)
	c.Assert(err, IsNil)
	c.Assert(found, Equals, true)
	c.Assert(fresh, NotNil)
	c.Assert(fresh.Reverted, Equals, true)

	stillCached, err := bridge.GetObservedTxVoter(txID)
	c.Assert(err, IsNil)
	c.Assert(stillCached.Reverted, Equals, false)
	c.Assert(requests, Equals, 2)
}

func (s *FreshQuerySuite) TestFreshVoterReportsBareNotFoundAsFailure(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	voter, found, err := bridge.GetObservedTxVoterUncached(strings.Repeat("B", 64))
	c.Assert(err, NotNil)
	c.Assert(found, Equals, false)
	c.Assert(voter, IsNil)
}

func (s *FreshQuerySuite) TestFreshVoterReportsTransactionSpecificNotFoundAsAbsent(c *C) {
	txID := strings.Repeat("B", 64)
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusNotFound)
		_, err := fmt.Fprintf(rw, `{"message":"tx: %s doesn't exist"}`, txID)
		c.Assert(err, IsNil)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	voter, found, err := bridge.GetObservedTxVoterUncached(txID)
	c.Assert(err, IsNil)
	c.Assert(found, Equals, false)
	c.Assert(voter, IsNil)
}

func (s *FreshQuerySuite) TestFreshVoterReportsCosmosMissingQueryAsAbsent(c *C) {
	txID := strings.Repeat("D", 64)
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusInternalServerError)
		_, err := fmt.Fprintf(rw, `{"code":13,"message":"tx: %s doesn't exist"}`, txID)
		c.Assert(err, IsNil)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	voter, found, err := bridge.GetObservedTxVoterUncached(txID)
	c.Assert(err, IsNil)
	c.Assert(found, Equals, false)
	c.Assert(voter, IsNil)
}

func (s *FreshQuerySuite) TestFreshVoterReportsServerFailure(c *C) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	voter, found, err := bridge.GetObservedTxVoterUncached(strings.Repeat("C", 64))
	c.Assert(err, NotNil)
	c.Assert(found, Equals, false)
	c.Assert(voter, IsNil)
}

func (s *FreshQuerySuite) TestFreshBlockHeightBypassesButDoesNotReplaceCache(c *C) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		requests++
		_, err := fmt.Fprintf(rw, `[{"chain":"THOR","thorchain":%d}]`, 40+requests)
		c.Assert(err, IsNil)
	}))
	defer server.Close()

	bridge := newFreshQueryBridge(c, server)
	cached, err := bridge.GetBlockHeight()
	c.Assert(err, IsNil)
	c.Assert(cached, Equals, int64(41))

	fresh, err := bridge.GetBlockHeightUncached()
	c.Assert(err, IsNil)
	c.Assert(fresh, Equals, int64(42))

	stillCached, err := bridge.GetBlockHeight()
	c.Assert(err, IsNil)
	c.Assert(stillCached, Equals, int64(41))
	c.Assert(requests, Equals, 2)
}
