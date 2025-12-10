package dag

import (
	rand "math/rand/v2"
	"testing"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type DAGSuite struct{}

var _ = Suite(&DAGSuite{})

func (s *DAGSuite) TestExecute(c *C, rng *rand.Rand) {
	// bump count once on each operation
	count := 0
	op := func(config *OpConfig) OpResult {
		count++
		return OpResult{Continue: true, Finish: true, Error: nil}
	}

	// create nodes
	root := NewActor("root", rng)
	child1 := NewActor("child1", rng)
	child2 := NewActor("child2", rng)
	child3 := NewActor("child3", rng)
	grandchild1 := NewActor("grandchild1", rng)
	grandchild2 := NewActor("grandchild2", rng)
	grandchild3 := NewActor("grandchild3", rng)

	// add operations
	descendants := []*Actor{child1, child2, child3, grandchild1, grandchild2, grandchild3}
	for _, node := range descendants {
		node.Ops = []Op{op}
	}

	// build dag
	root.Children[child1] = true
	root.Children[child2] = true
	root.Children[child3] = true
	child1.Children[grandchild1] = true
	child2.Children[grandchild2] = true
	child3.Children[grandchild3] = true

	// execute
	Execute(nil, root, 1, rng)

	// should have executed op 6 times
	c.Assert(count, Equals, 6)
}
