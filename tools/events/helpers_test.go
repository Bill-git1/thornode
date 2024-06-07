package main

import (
	"testing"

	. "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) { TestingT(t) }

type Test struct{}

var _ = Suite(&Test{})

func (t *Test) TestStripMarkdownLinks(c *C) {
	// only show the links
	c.Check(StripMarkdownLinks("[link](http://example.com)"), Equals, "http://example.com")
	c.Check(StripMarkdownLinks("[link](http://example.com) [link2](http://example2.com)"), Equals, "http://example.com http://example2.com")
	c.Check(StripMarkdownLinks("[link](http://example.com) [link2](http://example2.com) [link3](http://example3.com)"), Equals, "http://example.com http://example2.com http://example3.com")

	// also handle spaces in title
	c.Check(StripMarkdownLinks("[Foo Bar](http://example.com) | [Bar Baz](http://example1.com)"), Equals, "http://example.com | http://example1.com")
}
