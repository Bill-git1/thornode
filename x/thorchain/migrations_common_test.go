package thorchain

import (
	. "gopkg.in/check.v1"
)

type MigrationCommonSuite struct{}

var _ = Suite(&MigrationCommonSuite{})

func (s *MigrationCommonSuite) TestMigrate7to8Debug(c *C) {
	ctx, mgr := setupManagerForTest(c)
	migrator := NewMigrator(mgr)

	err := migrator.Migrate7to8(ctx)
	c.Assert(err, IsNil)
}
