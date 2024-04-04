package dag

import (
	"testing"

	. "gitlab.com/thorchain/thornode/test/simulation/pkg/types"
	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type DAGSuite struct{}

var _ = Suite(&DAGSuite{})

func (s *DAGSuite) TestExecute(c *C) {
	// bump count once on each operation
	count := 0
	op := func(config *OpConfig) OpResult {
		count++
		return OpResult{Continue: true, Finish: true, Error: nil}
	}

	// create nodes
	root := &Actor{Name: "root"}
	child1 := &Actor{Name: "child1", Ops: []Op{op}}
	child2 := &Actor{Name: "child2", Ops: []Op{op}}
	child3 := &Actor{Name: "child3", Ops: []Op{op}}
	grandchild1 := &Actor{Name: "grandchild1", Ops: []Op{op}}
	grandchild2 := &Actor{Name: "grandchild2", Ops: []Op{op}}
	grandchild3 := &Actor{Name: "grandchild3", Ops: []Op{op}}

	// build dag
	root.Children = []*Actor{child1, child2, child3}
	child1.Children = []*Actor{grandchild1}
	child2.Children = []*Actor{grandchild2}
	child3.Children = []*Actor{grandchild3}

	// execute
	Execute(nil, root, 1)

	// should have executed op 6 times
	c.Assert(count, Equals, 6)
}
