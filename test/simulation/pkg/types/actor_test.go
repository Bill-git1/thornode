package types

import (
	"errors"
	"testing"

	. "gopkg.in/check.v1"
)

func Test(t *testing.T) { TestingT(t) }

type ActorSuite struct{}

var _ = Suite(&ActorSuite{})

func (s *ActorSuite) TestInit(c *C) {
	root := &Actor{Name: "root"}
	child1 := &Actor{Name: "child1"}
	child2 := &Actor{Name: "child2"}
	grandchild1 := &Actor{Name: "grandchild1"}
	grandchild2 := &Actor{Name: "grandchild2"}

	root.Children = []*Actor{child1, child2}
	child1.Children = []*Actor{grandchild1}
	child2.Children = []*Actor{grandchild2}

	root.InitRoot()

	// init the atomics
	c.Assert(child1.started, NotNil)
	c.Assert(child1.finished, NotNil)
	c.Assert(grandchild2.started, NotNil)
	c.Assert(grandchild2.finished, NotNil)

	// ensure parents are set
	c.Assert(child1.parents, DeepEquals, []*Actor{root})
	c.Assert(grandchild2.parents, DeepEquals, []*Actor{child2})
}

func (s *ActorSuite) TestWalkDepthFirst(c *C) {
	root := &Actor{Name: "root"}
	child1 := &Actor{Name: "child1"}
	child2 := &Actor{Name: "child2"}
	grandchild1 := &Actor{Name: "grandchild1"}
	grandchild2 := &Actor{Name: "grandchild2"}

	root.Children = []*Actor{child1, child2}
	child1.Children = []*Actor{grandchild1}
	child2.Children = []*Actor{grandchild2}

	root.InitRoot()

	var visited []string
	root.WalkDepthFirst(func(a *Actor) bool {
		visited = append(visited, a.Name)
		return a.Execute(nil) == nil
	})

	expected := []string{"root", "child1", "grandchild1", "child2", "grandchild2"}
	c.Assert(visited, DeepEquals, expected)
}

func (s *ActorSuite) TestWalkDepthFirstFail(c *C) {
	opFail := func(config *OpConfig) OpResult {
		return OpResult{Continue: false, Finish: true, Error: errors.New("foo")}
	}

	root := &Actor{Name: "root"}
	child1 := &Actor{Name: "child1"}
	child2 := &Actor{Name: "child2", Ops: []Op{opFail}}
	child3 := &Actor{Name: "child3"}
	grandchild1 := &Actor{Name: "grandchild1"}
	grandchild2 := &Actor{Name: "grandchild2"}
	grandchild3 := &Actor{Name: "grandchild3"}

	root.Children = []*Actor{child1, child2, child3}
	child1.Children = []*Actor{grandchild1}
	child2.Children = []*Actor{grandchild2}
	child3.Children = []*Actor{grandchild3}

	root.InitRoot()

	var visited []string
	root.WalkDepthFirst(func(a *Actor) bool {
		visited = append(visited, a.Name)
		return a.Execute(nil) == nil
	})

	expected := []string{"root", "child1", "grandchild1", "child2"}
	c.Assert(visited, DeepEquals, expected)
}

func (s *ActorSuite) TestWalkBreadthFirst(c *C) {
	root := &Actor{Name: "root"}
	child1 := &Actor{Name: "child1"}
	child2 := &Actor{Name: "child2"}
	grandchild1 := &Actor{Name: "grandchild1"}
	grandchild2 := &Actor{Name: "grandchild2"}

	root.Children = []*Actor{child1, child2}
	child1.Children = []*Actor{grandchild1}
	child2.Children = []*Actor{grandchild2}

	root.InitRoot()

	var visited []string
	root.WalkBreadthFirst(func(a *Actor) bool {
		visited = append(visited, a.Name)
		return a.Execute(nil) == nil
	})

	expected := []string{"root", "child1", "child2", "grandchild1", "grandchild2"}
	c.Assert(visited, DeepEquals, expected)
}
