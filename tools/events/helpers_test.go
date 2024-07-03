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

func (t *Test) TestFormatLocale(c *C) {
	c.Check(FormatLocale(1), Equals, "1")
	c.Check(FormatLocale(12), Equals, "12")
	c.Check(FormatLocale(123), Equals, "123")
	c.Check(FormatLocale(-123), Equals, "-123")
	c.Check(FormatLocale(-123.000), Equals, "-123.00000000")
	c.Check(FormatLocale(-123.123), Equals, "-123.12300000")
	c.Check(FormatLocale(1234), Equals, "1,234")
	c.Check(FormatLocale(1234.000), Equals, "1,234.00000000")
	c.Check(FormatLocale(1234.123), Equals, "1,234.12300000")
	c.Check(FormatLocale(-1234), Equals, "-1,234")
	c.Check(FormatLocale(-1234.000), Equals, "-1,234.00000000")
	c.Check(FormatLocale(-1234.123), Equals, "-1,234.12300000")
	c.Check(FormatLocale(12345), Equals, "12,345")
	c.Check(FormatLocale(123456), Equals, "123,456")
	c.Check(FormatLocale(1234567), Equals, "1,234,567")
	c.Check(FormatLocale(-1234567), Equals, "-1,234,567")
	c.Check(FormatLocale(12345678), Equals, "12,345,678")
	c.Check(FormatLocale(123456789), Equals, "123,456,789")
	c.Check(FormatLocale(1234567890), Equals, "1,234,567,890")
}
