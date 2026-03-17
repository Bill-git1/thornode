package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	gethrpc "github.com/ethereum/go-ethereum/rpc"
	. "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) { TestingT(t) }

type RPCSuite struct{}

var _ = Suite(&RPCSuite{})

type mockRPCClient struct {
	batchCallCount              int
	batchCallContextCount       int
	batchCallContextHadDeadline bool
}

func (m *mockRPCClient) Call(result any, method string, args ...any) error {
	return nil
}

func (m *mockRPCClient) BatchCall(batch []gethrpc.BatchElem) error {
	m.batchCallCount++
	return nil
}

func (m *mockRPCClient) CallContext(ctx context.Context, result any, method string, args ...any) error {
	return nil
}

func (m *mockRPCClient) BatchCallContext(ctx context.Context, batch []gethrpc.BatchElem) error {
	m.batchCallContextCount++
	_, m.batchCallContextHadDeadline = ctx.Deadline()
	return nil
}

func (s *RPCSuite) TestRetry(c *C) {
	cl := Client{maxRetries: 3}
	called := 0
	err := cl.retry(func() error {
		called++
		return nil
	})
	c.Assert(err, IsNil)
	c.Assert(called, Equals, 1)

	called = 0
	err = cl.retry(func() error {
		called++
		return errors.New("error")
	})
	c.Assert(err, NotNil)
	c.Assert(called, Equals, 1)

	called = 0
	err = cl.retry(func() error {
		called++
		return errors.New("500 Internal Server Error: work queue depth exceeded")
	})
	c.Assert(err, NotNil)
	c.Assert(called, Equals, 4)

	called = 0
	err = cl.retry(func() error {
		called++
		if called < 2 {
			return errors.New("500 Internal Server Error: work queue depth exceeded")
		}
		return nil
	})
	c.Assert(err, IsNil)
	c.Assert(called, Equals, 2)
}

func (s *RPCSuite) TestBatchGetMempoolEntryUsesTimeoutAwareBatchCall(c *C) {
	mockClient := &mockRPCClient{}
	cl := Client{
		client:  mockClient,
		timeout: 50 * time.Millisecond,
	}

	results, errs, err := cl.BatchGetMempoolEntry([]string{"txid"})
	c.Assert(err, IsNil)
	c.Assert(results, HasLen, 1)
	c.Assert(errs, HasLen, 1)
	c.Assert(errs[0], IsNil)
	c.Assert(mockClient.batchCallCount, Equals, 0)
	c.Assert(mockClient.batchCallContextCount, Equals, 1)
	c.Assert(mockClient.batchCallContextHadDeadline, Equals, true)
}

func (s *RPCSuite) TestBatchGetRawTransactionVerboseUsesTimeoutAwareBatchCall(c *C) {
	mockClient := &mockRPCClient{}
	cl := Client{
		client:  mockClient,
		timeout: 50 * time.Millisecond,
	}

	results, errs, err := cl.BatchGetRawTransactionVerbose([]string{"txid"})
	c.Assert(err, IsNil)
	c.Assert(results, HasLen, 1)
	c.Assert(errs, HasLen, 1)
	c.Assert(errs[0], IsNil)
	c.Assert(mockClient.batchCallCount, Equals, 0)
	c.Assert(mockClient.batchCallContextCount, Equals, 1)
	c.Assert(mockClient.batchCallContextHadDeadline, Equals, true)
}
