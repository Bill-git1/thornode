package tss

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ctypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	"github.com/itchio/lzma"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

var keyshareRecoveryNodeHome = app.DefaultNodeHome

func RecoverKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge) error {
	tctx := thorchain.GetContext()

	// fetch the node account
	na, err := thorchain.GetNodeAccount(tctx.FromAddress.String())
	if err != nil {
		return fmt.Errorf("fail to get node account: %w", err)
	}

	// skip recovery if the current node is not active
	if na.Status != types.NodeStatus_Active {
		log.Info().Msgf("%s is not active, skipping key shares recovery", na.NodeAddress)
		return nil
	}

	// the current vault is the last pub key in the signer membership list
	membership := na.GetSignerMembership()
	if len(membership) == 0 {
		log.Info().Msgf("no signer membership for %s, skipping key shares recovery", na.NodeAddress)
		return fmt.Errorf("fail to get signer membership")
	}
	vault := membership[len(membership)-1]

	// get all vaults
	vaults, err := thorchain.GetAsgards()
	if err != nil {
		return fmt.Errorf("fail to get asgards: %w", err)
	}

	target, ok := findKeyshareRecoveryTarget(vault, vaults)
	if !ok {
		return fmt.Errorf("fail to get creation height of %s", vault)
	}
	if target.kind == recoveryTargetFrost {
		return recoverFrostKeyShares(conf, thorchain, na.NodeAddress, target)
	}

	return recoverLegacyKeyShares(conf, thorchain, na.NodeAddress, target, vaults)
}

type recoveryTargetKind int

const (
	recoveryTargetLegacy recoveryTargetKind = iota
	recoveryTargetFrost
)

type keyshareRecoveryTarget struct {
	kind            recoveryTargetKind
	pubKey          common.PubKey
	blockHeight     int64
	poolPubKeyEddsa common.PubKey
}

func findKeyshareRecoveryTarget(vault common.PubKey, vaults types.Vaults) (keyshareRecoveryTarget, bool) {
	for _, v := range vaults {
		if v.PubKeyFrost.Equals(vault) {
			return keyshareRecoveryTarget{
				kind:        recoveryTargetFrost,
				pubKey:      v.PubKeyFrost,
				blockHeight: v.BlockHeight,
			}, true
		}
		if v.PubKey.Equals(vault) {
			return keyshareRecoveryTarget{
				kind:            recoveryTargetLegacy,
				pubKey:          v.PubKey,
				blockHeight:     v.BlockHeight,
				poolPubKeyEddsa: v.PubKeyEddsa,
			}, true
		}
	}
	return keyshareRecoveryTarget{}, false
}

func recoverFrostKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge, signer ctypes.AccAddress, target keyshareRecoveryTarget) error {
	frostRecoveryPath := frostKeyshareRecoveryPath(target.pubKey)
	exists, err := recoveryFileExists(frostRecoveryPath)
	if err != nil {
		return fmt.Errorf("failed to stat frost key shares for %s: %w", target.pubKey, err)
	}
	if exists {
		log.Info().Msgf("frost key shares for %s already exist, skipping recovery", target.pubKey)
		return nil
	}

	backups, err := findRecoveryBackups(conf, thorchain, signer, target)
	if err != nil {
		return err
	}
	if len(backups.frost) == 0 {
		return fmt.Errorf("frost key shares backup not found for %s", target.pubKey)
	}

	seedPhrase, err := recoverySeedPhrase()
	if err != nil {
		return err
	}
	if err = writeRecoveredRawKeyshares(frostRecoveryPath, backups.frost, seedPhrase); err != nil {
		return fmt.Errorf("failed to recover frost key shares: %w", err)
	}

	log.Info().Str("path", frostRecoveryPath).Msgf("recovered frost key shares for %s", target.pubKey)
	return nil
}

func recoverLegacyKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge, signer ctypes.AccAddress, target keyshareRecoveryTarget, vaults types.Vaults) error {
	state, err := legacyKeyshareRecoveryState(target, vaults)
	if err != nil {
		return err
	}
	logExistingLegacyKeyshares(target, state)
	if state.recovered() {
		return nil
	}

	backups, err := findRecoveryBackups(conf, thorchain, signer, target)
	if err != nil {
		return err
	}
	return recoverLegacyKeySharesFromBackups(signer, target, vaults, backups)
}

type legacyKeyshareState struct {
	ecdsaPath   string
	ecdsaExists bool
	eddsaPath   string
	eddsaExists bool
	hasEddsa    bool
}

func (s legacyKeyshareState) recovered() bool {
	return s.ecdsaExists && (!s.hasEddsa || s.eddsaExists)
}

func recoverLegacyKeySharesFromBackups(signer ctypes.AccAddress, target keyshareRecoveryTarget, vaults types.Vaults, backups recoveryBackups) error {
	state, err := legacyKeyshareRecoveryState(target, vaults)
	if err != nil {
		return err
	}
	logExistingLegacyKeyshares(target, state)
	if state.recovered() {
		return nil
	}

	needsSeed := (!state.ecdsaExists && len(backups.secp256k1) > 0) ||
		(state.hasEddsa && !state.eddsaExists && len(backups.eddsa) > 0)
	seedPhrase := ""
	if needsSeed {
		seedPhrase, err = recoverySeedPhrase()
		if err != nil {
			return err
		}
	}

	var resultErr error
	if !state.ecdsaExists {
		if len(backups.secp256k1) == 0 {
			resultErr = errors.Join(resultErr, fmt.Errorf("ecdsa key shares backup not found for %s", target.pubKey))
		} else if err = writeRecoveredDecompressedKeyshares(state.ecdsaPath, backups.secp256k1, seedPhrase); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("failed to recover ecdsa key shares: %w", err))
		} else {
			log.Info().Str("path", state.ecdsaPath).Msgf("recovered ecdsa key shares for %s", signer)
		}
	}

	if state.hasEddsa && !state.eddsaExists {
		if len(backups.eddsa) == 0 {
			resultErr = errors.Join(resultErr, fmt.Errorf("eddsa key shares backup not found for %s", target.poolPubKeyEddsa))
		} else if err = writeRecoveredDecompressedKeyshares(state.eddsaPath, backups.eddsa, seedPhrase); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("failed to recover eddsa key shares: %w", err))
		} else {
			log.Info().Str("path", state.eddsaPath).Msgf("recovered eddsa key shares for %s", signer)
		}
	}

	return resultErr
}

func legacyKeyshareRecoveryState(target keyshareRecoveryTarget, vaults types.Vaults) (legacyKeyshareState, error) {
	state := legacyKeyshareState{
		ecdsaPath: localstateKeysharePath(target.pubKey),
	}

	var err error
	state.ecdsaExists, err = recoveryFileExists(state.ecdsaPath)
	if err != nil {
		return legacyKeyshareState{}, fmt.Errorf("failed to stat ecdsa key shares for %s: %w", target.pubKey, err)
	}

	if target.poolPubKeyEddsa.IsEmpty() {
		return state, nil
	}
	state.hasEddsa = true
	state.eddsaPath = localstateKeysharePath(target.poolPubKeyEddsa)

	lastVaultHeight := int64(0)
	for _, v := range vaults {
		if v.PubKeyEddsa.Equals(target.poolPubKeyEddsa) {
			lastVaultHeight = v.BlockHeight
			break
		}
	}
	if lastVaultHeight == 0 {
		return legacyKeyshareState{}, fmt.Errorf("fail to get creation height of %s", target.poolPubKeyEddsa)
	}

	state.eddsaExists, err = recoveryFileExists(state.eddsaPath)
	if err != nil {
		return legacyKeyshareState{}, fmt.Errorf("failed to stat eddsa key shares for %s: %w", target.poolPubKeyEddsa, err)
	}

	return state, nil
}

func logExistingLegacyKeyshares(target keyshareRecoveryTarget, state legacyKeyshareState) {
	if state.ecdsaExists {
		log.Info().Msgf("ecdsa key shares for %s already exist, skipping recovery", target.pubKey)
	}
	if state.hasEddsa && state.eddsaExists {
		log.Info().Msgf("eddsa key shares for %s already exist, skipping recovery", target.poolPubKeyEddsa)
	}
}

func localstateKeysharePath(pubKey common.PubKey) string {
	return filepath.Join(keyshareRecoveryNodeHome, fmt.Sprintf("localstate-%s.json", pubKey))
}

func frostKeyshareRecoveryPath(pubKey common.PubKey) string {
	return filepath.Join(keyshareRecoveryNodeHome, fmt.Sprintf("frost-recovery-%s.hex", pubKey))
}

func recoveryFileExists(path string) (bool, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if fi.IsDir() {
		return false, fmt.Errorf("recovery path is a directory: %s", path)
	}
	return fi.Size() > 0, nil
}

type recoveryBackups struct {
	secp256k1 []byte
	eddsa     []byte
	frost     []byte
}

func findRecoveryBackups(conf config.Bifrost, thorchain thorclient.ThorchainBridge, signer ctypes.AccAddress, target keyshareRecoveryTarget) (recoveryBackups, error) {
	cdc := thorclient.MakeCodec()
	dec := ebifrost.TxDecoder(cdc, tx.DefaultTxDecoder(cdc))
	scanLowerBound := recoverScanLowerBound(target.blockHeight, conf.TSS.MaxKeyshareRecoverScanBlocks)
	for i := target.blockHeight; i > scanLowerBound; i-- {
		if i%1000 == 0 {
			log.Info().Msgf("scanning block %d for TssPool message to recover key shares", i)
		}

		b, err := thorchain.GetContext().Client.Block(context.Background(), &i)
		if err != nil {
			return recoveryBackups{}, fmt.Errorf("fail to get block: %w", err)
		}

		for _, txb := range b.Block.Txs {
			var tx ctypes.Tx
			tx, err = dec(txb)
			if err != nil {
				return recoveryBackups{}, fmt.Errorf("fail to decode tx: %w", err)
			}
			for _, msg := range tx.GetMsgs() {
				switch m := msg.(type) {
				case *types.MsgTssPool:
					if msgMatchesRecoveryTarget(m, signer, target) {
						return recoveryBackups{
							secp256k1: m.KeysharesBackup,
							eddsa:     m.KeysharesBackupEddsa,
							frost:     m.KeysharesBackupFrost,
						}, nil
					}
				default:
				}
			}
		}
	}

	return recoveryBackups{}, fmt.Errorf(
		"TssPool message from %s not found within %d blocks of vault height %d",
		signer,
		conf.TSS.MaxKeyshareRecoverScanBlocks,
		target.blockHeight,
	)
}

func msgMatchesRecoveryTarget(m *types.MsgTssPool, signer ctypes.AccAddress, target keyshareRecoveryTarget) bool {
	if !m.Signer.Equals(signer) {
		return false
	}
	switch target.kind {
	case recoveryTargetFrost:
		return m.KeygenType == types.KeygenType_XMRMonovaultKeygen && m.PoolPubKeyFrost.Equals(target.pubKey)
	case recoveryTargetLegacy:
		return m.PoolPubKey.Equals(target.pubKey)
	default:
		return false
	}
}

func recoverScanLowerBound(lastVaultHeight, maxScanBlocks int64) int64 {
	if lastVaultHeight <= 0 {
		return 0
	}
	lowerBound := lastVaultHeight - maxScanBlocks
	if lowerBound < 0 {
		return 0
	}
	return lowerBound
}

func recoverySeedPhrase() (string, error) {
	seedPhrase, err := common.GetSignerSeedPhrase()
	if err != nil {
		return "", fmt.Errorf("failed to get signer seed phrase: %w", err)
	}
	seedPhrase = strings.TrimSpace(seedPhrase)
	if seedPhrase == "" {
		return "", fmt.Errorf("signer seed phrase is empty")
	}
	return seedPhrase, nil
}

func writeRecoveredDecompressedKeyshares(path string, encrypted []byte, seedPhrase string) error {
	if len(encrypted) == 0 {
		return fmt.Errorf("encrypted key shares backup is empty")
	}

	decrypted, err := DecryptKeyshares(encrypted, seedPhrase)
	if err != nil {
		return fmt.Errorf("failed to decrypt key shares: %w", err)
	}

	rawKeyshares, err := io.ReadAll(lzma.NewReader(bytes.NewReader(decrypted)))
	if err != nil {
		return fmt.Errorf("failed to decompress key shares: %w", err)
	}

	return writeRecoveredBytes(path, rawKeyshares)
}

func writeRecoveredRawKeyshares(path string, encrypted []byte, seedPhrase string) error {
	if len(encrypted) == 0 {
		return fmt.Errorf("encrypted raw key shares backup is empty")
	}

	rawKeyshares, err := DecryptRawKeyshares(encrypted, seedPhrase)
	if err != nil {
		return fmt.Errorf("failed to decrypt raw key shares: %w", err)
	}

	return writeRecoveredBytes(path, rawKeyshares)
}

func writeRecoveredBytes(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to open recovery temp file: %w", err)
	}

	tmpPath := tmp.Name()
	success := false
	defer func() {
		_ = tmp.Close()
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("failed to set recovery temp file permissions: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("failed to write recovery temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close recovery temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to move recovery file into place: %w", err)
	}

	success = true
	return nil
}
