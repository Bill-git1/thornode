package common

import (
	"os"
	"path/filepath"

	. "gopkg.in/check.v1"
)

type SeedPhraseSuite struct{}

var _ = Suite(&SeedPhraseSuite{})

const testMnemonic = "dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog dog fossil"

func (s *SeedPhraseSuite) SetUpTest(c *C) {
	c.Assert(os.Unsetenv("SIGNER_SEED_PHRASE"), IsNil)
	c.Assert(os.Unsetenv("SIGNER_SEED_PHRASE_FILE"), IsNil)
}

func (s *SeedPhraseSuite) TearDownTest(c *C) {
	c.Assert(os.Unsetenv("SIGNER_SEED_PHRASE"), IsNil)
	c.Assert(os.Unsetenv("SIGNER_SEED_PHRASE_FILE"), IsNil)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_EnvVarOnly(c *C) {
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE", testMnemonic), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, testMnemonic)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileOnly(c *C) {
	path := filepath.Join(c.MkDir(), "seed-phrase")
	c.Assert(os.WriteFile(path, []byte("  "+testMnemonic+"\n"), 0o600), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", path), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, testMnemonic)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileTakesPriority(c *C) {
	fileMnemonic := "cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat cat crawl"
	path := filepath.Join(c.MkDir(), "seed-phrase")
	c.Assert(os.WriteFile(path, []byte(fileMnemonic+"\n"), 0o600), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", path), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE", testMnemonic), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, fileMnemonic)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_BothMissingReturnsError(c *C) {
	got, err := GetSignerSeedPhrase()
	c.Check(got, Equals, "")
	c.Assert(err, NotNil)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileMissingFallsBackToEnvVar(c *C) {
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", "/nonexistent/seed-phrase-file"), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE", testMnemonic), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, testMnemonic)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileMissingNoEnvVarReturnsError(c *C) {
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", "/nonexistent/seed-phrase-file"), IsNil)

	_, err := GetSignerSeedPhrase()
	c.Assert(err, NotNil)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileCorruptFallsBackToEnvVar(c *C) {
	path := filepath.Join(c.MkDir(), "seed-phrase")
	c.Assert(os.WriteFile(path, []byte("not a valid mnemonic"), 0o600), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", path), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE", testMnemonic), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, testMnemonic)
}

func (s SeedPhraseSuite) TestGetSignerSeedPhrase_FileEmptyFallsBackToEnvVar(c *C) {
	path := filepath.Join(c.MkDir(), "seed-phrase")
	c.Assert(os.WriteFile(path, []byte("   \n"), 0o600), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE_FILE", path), IsNil)
	c.Assert(os.Setenv("SIGNER_SEED_PHRASE", testMnemonic), IsNil)

	got, err := GetSignerSeedPhrase()
	c.Assert(err, IsNil)
	c.Check(got, Equals, testMnemonic)
}
