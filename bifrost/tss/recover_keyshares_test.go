package tss

import (
	"os"
	"path/filepath"

	. "gopkg.in/check.v1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

type RecoverKeysharesSuite struct{}

var _ = Suite(&RecoverKeysharesSuite{})

func (s *RecoverKeysharesSuite) TestRecoverScanLowerBound(c *C) {
	c.Assert(recoverScanLowerBound(0, 100), Equals, int64(0))
	c.Assert(recoverScanLowerBound(50, 100), Equals, int64(0))
	c.Assert(recoverScanLowerBound(500, 100), Equals, int64(400))
}

func (s *RecoverKeysharesSuite) TestFindKeyshareRecoveryTargetPrefersFrostPubKey(c *C) {
	legacyPubKey := types.GetRandomPubKey()
	frostPubKey := types.GetRandomEd25519PubKey()
	eddsaPubKey := types.GetRandomEd25519PubKey()
	vaults := types.Vaults{
		{
			PubKey:      legacyPubKey,
			PubKeyEddsa: eddsaPubKey,
			PubKeyFrost: frostPubKey,
			BlockHeight: 123,
		},
	}

	target, ok := findKeyshareRecoveryTarget(frostPubKey, vaults)
	c.Assert(ok, Equals, true)
	c.Check(target.kind, Equals, recoveryTargetFrost)
	c.Check(target.pubKey.Equals(frostPubKey), Equals, true)
	c.Check(target.blockHeight, Equals, int64(123))

	target, ok = findKeyshareRecoveryTarget(legacyPubKey, vaults)
	c.Assert(ok, Equals, true)
	c.Check(target.kind, Equals, recoveryTargetLegacy)
	c.Check(target.pubKey.Equals(legacyPubKey), Equals, true)
	c.Check(target.poolPubKeyEddsa.Equals(eddsaPubKey), Equals, true)

	_, ok = findKeyshareRecoveryTarget(types.GetRandomPubKey(), vaults)
	c.Check(ok, Equals, false)
}

func (s *RecoverKeysharesSuite) TestMsgMatchesRecoveryTargetRequiresExactKeyType(c *C) {
	signer, err := types.GetRandomPubKey().GetThorAddress()
	c.Assert(err, IsNil)
	otherSigner, err := types.GetRandomPubKey().GetThorAddress()
	c.Assert(err, IsNil)
	legacyPubKey := types.GetRandomPubKey()
	frostPubKey := types.GetRandomEd25519PubKey()

	xmrMsg := &types.MsgTssPool{
		Signer:          signer,
		KeygenType:      types.KeygenType_XMRMonovaultKeygen,
		PoolPubKey:      legacyPubKey,
		PoolPubKeyFrost: frostPubKey,
	}
	frostTarget := keyshareRecoveryTarget{kind: recoveryTargetFrost, pubKey: frostPubKey}
	legacyTarget := keyshareRecoveryTarget{kind: recoveryTargetLegacy, pubKey: legacyPubKey}

	c.Check(msgMatchesRecoveryTarget(xmrMsg, signer, frostTarget), Equals, true)
	c.Check(msgMatchesRecoveryTarget(xmrMsg, signer, legacyTarget), Equals, true)
	c.Check(msgMatchesRecoveryTarget(xmrMsg, otherSigner, frostTarget), Equals, false)
	c.Check(msgMatchesRecoveryTarget(xmrMsg, signer, keyshareRecoveryTarget{kind: recoveryTargetFrost, pubKey: common.EmptyPubKey}), Equals, false)

	legacyMsg := &types.MsgTssPool{
		Signer:     signer,
		KeygenType: types.KeygenType_AsgardKeygen,
		PoolPubKey: legacyPubKey,
	}
	c.Check(msgMatchesRecoveryTarget(legacyMsg, signer, legacyTarget), Equals, true)
	c.Check(msgMatchesRecoveryTarget(legacyMsg, signer, frostTarget), Equals, false)
}

func (s *RecoverKeysharesSuite) TestRecoverySeedPhraseUsesSharedSignerSeed(c *C) {
	originalSeedPhrase, hadOriginalSeedPhrase := os.LookupEnv("SIGNER_SEED_PHRASE")
	originalSeedPhraseFile, hadOriginalSeedPhraseFile := os.LookupEnv("SIGNER_SEED_PHRASE_FILE")
	defer func() {
		if hadOriginalSeedPhrase {
			_ = os.Setenv("SIGNER_SEED_PHRASE", originalSeedPhrase)
		} else {
			_ = os.Unsetenv("SIGNER_SEED_PHRASE")
		}
		if hadOriginalSeedPhraseFile {
			_ = os.Setenv("SIGNER_SEED_PHRASE_FILE", originalSeedPhraseFile)
		} else {
			_ = os.Unsetenv("SIGNER_SEED_PHRASE_FILE")
		}
	}()

	_ = os.Unsetenv("SIGNER_SEED_PHRASE")
	_ = os.Unsetenv("SIGNER_SEED_PHRASE_FILE")
	seedPhrase, err := recoverySeedPhrase()
	c.Assert(err, NotNil)
	c.Assert(err.Error(), Equals, "failed to get signer seed phrase: neither SIGNER_SEED_PHRASE_FILE nor SIGNER_SEED_PHRASE is set")
	c.Assert(seedPhrase, Equals, "")

	err = os.Setenv("SIGNER_SEED_PHRASE", "  "+Mnemonic+"  ")
	c.Assert(err, IsNil)
	seedPhrase, err = recoverySeedPhrase()
	c.Assert(err, IsNil)
	c.Assert(seedPhrase, Equals, Mnemonic)
}

func (s *RecoverKeysharesSuite) TestWriteRecoveredDecompressedKeysharesWritesOwnerOnlyFile(c *C) {
	encrypted, err := EncryptKeyshares(LocalStateTestFile, Mnemonic)
	c.Assert(err, IsNil)

	dir, err := os.MkdirTemp("", "recover-keyshares-*")
	c.Assert(err, IsNil)
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "localstate-recovered.json")
	err = writeRecoveredDecompressedKeyshares(path, encrypted, Mnemonic)
	c.Assert(err, IsNil)

	fi, err := os.Stat(path)
	c.Assert(err, IsNil)
	c.Assert(fi.Mode().Perm(), Equals, os.FileMode(0o600))

	original, err := os.ReadFile(LocalStateTestFile)
	c.Assert(err, IsNil)
	recovered, err := os.ReadFile(path)
	c.Assert(err, IsNil)
	c.Assert(recovered, DeepEquals, original)
}

func (s *RecoverKeysharesSuite) TestWriteRecoveredDecompressedKeysharesDoesNotLeaveOrphanOnDecryptFailure(c *C) {
	dir, err := os.MkdirTemp("", "recover-keyshares-*")
	c.Assert(err, IsNil)
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "localstate-recovered.json")
	err = writeRecoveredDecompressedKeyshares(path, []byte("broken"), Mnemonic)
	c.Assert(err, NotNil)

	_, err = os.Stat(path)
	c.Assert(os.IsNotExist(err), Equals, true)

	entries, err := os.ReadDir(dir)
	c.Assert(err, IsNil)
	c.Assert(entries, HasLen, 0)
}

func (s *RecoverKeysharesSuite) TestWriteRecoveredRawKeysharesWritesOwnerOnlyFile(c *C) {
	rawKeyshares := []byte("deadbeefcafebabe")
	encrypted, err := EncryptRawKeyshares(rawKeyshares, Mnemonic)
	c.Assert(err, IsNil)

	dir, err := os.MkdirTemp("", "recover-keyshares-*")
	c.Assert(err, IsNil)
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "frost-recovery.hex")
	err = writeRecoveredRawKeyshares(path, encrypted, Mnemonic)
	c.Assert(err, IsNil)

	fi, err := os.Stat(path)
	c.Assert(err, IsNil)
	c.Assert(fi.Mode().Perm(), Equals, os.FileMode(0o600))

	recovered, err := os.ReadFile(path)
	c.Assert(err, IsNil)
	c.Assert(recovered, DeepEquals, rawKeyshares)
}
