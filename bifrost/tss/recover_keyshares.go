package tss

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ctypes "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/tx"
	"github.com/itchio/lzma"
	"github.com/rs/zerolog/log"

	"gitlab.com/thorchain/thornode/v3/app"
	"gitlab.com/thorchain/thornode/v3/bifrost/p2p/storage"
	"gitlab.com/thorchain/thornode/v3/bifrost/thorclient"
	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
	"gitlab.com/thorchain/thornode/v3/config"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/ebifrost"
	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

var (
	keyshareRecoveryNodeHome = app.DefaultNodeHome
	recoveryArtifactMu       sync.Mutex
)

const (
	maxFrostRecoveryFileBytes  = int64(1024 * 1024)
	frostRecoveryEnvelopeMagic = "THOR-XMR-FROST-RECOVERY\x02"
	frostRecoveryProtocol      = "thorchain-xmr-frost-recovery"
	frostRecoveryVersion       = 2
)

type frostRecoveryEnvelope struct {
	Protocol    string `json:"protocol"`
	Version     int    `json:"version"`
	Network     string `json:"network"`
	VaultPubKey string `json:"vault_pub_key"`
	GroupKey    string `json:"group_key"`
	KeyBytesB64 string `json:"key_bytes_b64"`
}

func RecoverKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge) error {
	return recoverKeyShares(conf, thorchain, nil)
}

// RecoverLegacyKeyShares restores key material used by the process-wide TSS
// stack. Bifrost must not start that stack while this recovery is incomplete.
func RecoverLegacyKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge) error {
	kind := recoveryTargetLegacy
	return recoverKeyShares(conf, thorchain, &kind)
}

// RecoverFrostKeyShares restores XMR vault material consumed by the sidecar.
// Exact-vault readiness can isolate a missing FROST share, so callers may retry
// this scope without holding all of Bifrost at startup.
func RecoverFrostKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge) error {
	kind := recoveryTargetFrost
	return recoverKeyShares(conf, thorchain, &kind)
}

func recoverKeyShares(conf config.Bifrost, thorchain thorclient.ThorchainBridge, selectedKind *recoveryTargetKind) error {
	tctx := thorchain.GetContext()

	// fetch the node account
	na, err := thorchain.GetNodeAccount(tctx.FromAddress.String())
	if err != nil {
		return fmt.Errorf("fail to get node account: %w", err)
	}

	// Recover every membership still assigned to this node. A retiring XMR vault
	// may still need migration or refund signatures after its member leaves the
	// active set, so node status must not suppress recovery.
	membership := na.GetSignerMembership()
	if len(membership) == 0 {
		log.Info().Msgf("no signer membership for %s, skipping key shares recovery", na.NodeAddress)
		return nil
	}
	// get all vaults
	vaults, err := thorchain.GetAsgards()
	if err != nil {
		return fmt.Errorf("fail to get asgards: %w", err)
	}

	targets, recoveryErrs := keyshareRecoveryTargets(membership, vaults, selectedKind)
	for _, target := range targets {
		var targetErr error
		if target.kind == recoveryTargetFrost {
			targetErr = recoverFrostKeyShares(conf, thorchain, na.NodeAddress, target)
		} else {
			targetErr = recoverLegacyKeyShares(conf, thorchain, na.NodeAddress, target, vaults)
		}
		if targetErr != nil {
			recoveryErrs = append(recoveryErrs, fmt.Errorf("recover keyshares for %s: %w", target.pubKey, targetErr))
		}
	}
	return errors.Join(recoveryErrs...)
}

// keyshareRecoveryTargets resolves every distinct membership still assigned to
// the node. It deliberately does not collapse to the newest target of each key
// type: an older retiring vault may still need migration or refund signatures.
func keyshareRecoveryTargets(membership common.PubKeys, vaults types.Vaults, selectedKind *recoveryTargetKind) ([]keyshareRecoveryTarget, []error) {
	seen := make(map[string]struct{}, len(membership))
	targets := make([]keyshareRecoveryTarget, 0, len(membership))
	var recoveryErrs []error
	for _, vault := range membership {
		target, ok := findKeyshareRecoveryTarget(vault, vaults)
		if !ok {
			// The legacy startup barrier owns unknown memberships because it is
			// unsafe to assume an unclassified share is XMR-only.
			if selectedKind == nil || *selectedKind == recoveryTargetLegacy {
				recoveryErrs = append(recoveryErrs, fmt.Errorf("fail to get creation height of %s", vault))
			}
			continue
		}
		if selectedKind != nil && target.kind != *selectedKind {
			continue
		}
		key := fmt.Sprintf("%d:%s", target.kind, target.pubKey)
		if _, ok = seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	return targets, recoveryErrs
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
	network, err := frostRecoveryNetwork(conf)
	if err != nil {
		return err
	}
	seedPhrase, err := recoverySeedPhrase()
	if err != nil {
		return err
	}
	exists, err := recoveryFileExists(frostRecoveryPath)
	if err != nil {
		return fmt.Errorf("failed to stat frost key shares for %s: %w", target.pubKey, err)
	}
	if exists {
		if validateErr := validateFrostRecoveryFile(frostRecoveryPath, seedPhrase, network, target.pubKey); validateErr == nil {
			log.Info().Msgf("valid encrypted frost key shares for %s already exist, skipping recovery", target.pubKey)
			return nil
		} else {
			log.Error().Err(validateErr).Str("path", frostRecoveryPath).Msg("quarantining invalid frost recovery artifact")
			if quarantineErr := quarantineRecoveryFile(frostRecoveryPath); quarantineErr != nil {
				return fmt.Errorf("failed to quarantine invalid frost recovery artifact: %w", quarantineErr)
			}
		}
	}

	backups, err := findRecoveryBackups(conf, thorchain, signer, target)
	if isFrostRecoveryBackupMissing(backups, err) {
		log.Warn().
			Err(err).
			Msgf("frost key shares backup for %s not found; continuing because local signer state may already exist", target.pubKey)
		return nil
	}
	if err != nil {
		return err
	}

	if err = writeRecoveredRawKeyshares(frostRecoveryPath, backups.frost, seedPhrase, network, target.pubKey); err != nil {
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
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !fi.Mode().IsRegular() {
		return false, fmt.Errorf("recovery path is not a regular file: %s", path)
	}
	return fi.Size() > 0, nil
}

func validateFrostRecoveryFile(path, seedPhrase, network string, vaultPubKey common.PubKey) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("recovery artifact is not a regular file")
	}
	if fi.Size() <= 0 || fi.Size() > maxFrostRecoveryFileBytes {
		return fmt.Errorf("recovery artifact size %d is outside 1..%d", fi.Size(), maxFrostRecoveryFileBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return fmt.Errorf("recovery artifact changed during validation")
	}
	data := make([]byte, opened.Size())
	if _, err = io.ReadFull(f, data); err != nil {
		return err
	}
	plaintext, err := storage.DecryptAtRest(storage.DeriveAtRestKey(seedPhrase), data)
	if err != nil {
		return fmt.Errorf("recovery artifact authentication failed: %w", err)
	}
	defer func() {
		for i := range plaintext {
			plaintext[i] = 0
		}
	}()
	keyBytes, err := decodeFrostRecoveryEnvelope(plaintext, network, vaultPubKey)
	zeroBytes(keyBytes)
	return err
}

func quarantineRecoveryFile(path string) error {
	recoveryArtifactMu.Lock()
	defer recoveryArtifactMu.Unlock()
	return quarantineRecoveryFileLocked(path)
}

func quarantineRecoveryFileLocked(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("recovery artifact is not a regular file")
	}
	quarantined := fmt.Sprintf("%s.invalid-%d", path, time.Now().UnixNano())
	if renameErr := os.Rename(path, quarantined); renameErr != nil {
		return renameErr
	}
	moved, err := os.Lstat(quarantined)
	if err != nil {
		return fmt.Errorf("failed to inspect quarantined recovery artifact: %w", err)
	}
	if !moved.Mode().IsRegular() || !os.SameFile(info, moved) {
		return fmt.Errorf("recovery artifact changed while being quarantined")
	}
	return syncRecoveryDirectory(filepath.Dir(path))
}

type recoveryBackups struct {
	secp256k1 []byte
	eddsa     []byte
	frost     []byte
}

var errRecoveryBackupNotFound = errors.New("key share recovery backup not found")

func findRecoveryBackups(conf config.Bifrost, thorchain thorclient.ThorchainBridge, signer ctypes.AccAddress, target keyshareRecoveryTarget) (recoveryBackups, error) {
	cdc := thorclient.MakeCodec()
	dec := ebifrost.TxDecoder(cdc, tx.DefaultTxDecoder(cdc))
	currentHeight, err := thorchain.GetBlockHeight()
	if err != nil {
		log.Warn().Err(err).Msg("fail to get current thorchain height for key share recovery; scanning from vault height")
		currentHeight = target.blockHeight
	}
	scanLowerBound, scanUpperBound := recoverScanBounds(target.blockHeight, currentHeight, conf.TSS.MaxKeyshareRecoverScanBlocks)
	for i := scanUpperBound; i > scanLowerBound; i-- {
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
		"%w: "+
			"TssPool message from %s not found within %d blocks before or after vault height %d",
		errRecoveryBackupNotFound,
		signer,
		conf.TSS.MaxKeyshareRecoverScanBlocks,
		target.blockHeight,
	)
}

func isFrostRecoveryBackupMissing(backups recoveryBackups, err error) bool {
	if err != nil {
		return errors.Is(err, errRecoveryBackupNotFound)
	}
	return len(backups.frost) == 0
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

func recoverScanBounds(lastVaultHeight, currentHeight, maxScanBlocks int64) (int64, int64) {
	if lastVaultHeight <= 0 {
		return 0, 0
	}
	lowerBound := recoverScanLowerBound(lastVaultHeight, maxScanBlocks)
	if maxScanBlocks <= 0 {
		return lowerBound, lastVaultHeight
	}

	upperBound := lastVaultHeight
	if currentHeight > lastVaultHeight {
		upperBound = currentHeight
		maxUpperBound := lastVaultHeight + maxScanBlocks
		if upperBound > maxUpperBound {
			upperBound = maxUpperBound
		}
	}
	return lowerBound, upperBound
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

	encryptedAtRest, err := encryptAtRestForRecovery(rawKeyshares, seedPhrase)
	if err != nil {
		return err
	}
	return writeRecoveredBytes(path, encryptedAtRest)
}

func writeRecoveredRawKeyshares(path string, encrypted []byte, seedPhrase, network string, vaultPubKey common.PubKey) error {
	if len(encrypted) == 0 {
		return fmt.Errorf("encrypted raw key shares backup is empty")
	}

	rawKeyshares, err := DecryptRawKeyshares(encrypted, seedPhrase)
	if err != nil {
		return fmt.Errorf("failed to decrypt raw key shares: %w", err)
	}
	defer zeroBytes(rawKeyshares)

	envelope, err := encodeFrostRecoveryEnvelope(rawKeyshares, network, vaultPubKey)
	if err != nil {
		return err
	}
	defer zeroBytes(envelope)

	encryptedAtRest, err := encryptAtRestForRecovery(envelope, seedPhrase)
	if err != nil {
		return err
	}
	return writeRecoveredBytes(path, encryptedAtRest)
}

func frostRecoveryNetwork(conf config.Bifrost) (string, error) {
	network := strings.TrimSpace(conf.GetChains()[common.XMRChain].SignerExpectedNetwork)
	switch network {
	case "mocknet", "chainnet", "stagenet", "mainnet":
		return network, nil
	default:
		return "", fmt.Errorf("xmr signer expected network must be one of mocknet, chainnet, stagenet, or mainnet")
	}
}

func frostRecoveryGroupKey(vaultPubKey common.PubKey) (string, error) {
	if vaultPubKey.IsEmpty() {
		return "", fmt.Errorf("frost recovery vault pubkey is empty")
	}
	pk, err := cosmos.GetPubKeyFromBech32(cosmos.Bech32PubKeyTypeAccPub, vaultPubKey.String())
	if err != nil {
		return "", fmt.Errorf("fail to decode frost recovery vault pubkey: %w", err)
	}
	raw := pk.Bytes()
	if len(raw) != 32 {
		return "", fmt.Errorf("unexpected frost recovery group key length %d", len(raw))
	}
	return hex.EncodeToString(raw), nil
}

func encodeFrostRecoveryEnvelope(keyBytes []byte, network string, vaultPubKey common.PubKey) ([]byte, error) {
	if len(keyBytes) == 0 {
		return nil, fmt.Errorf("frost recovery key material is empty")
	}
	if err := validateFrostRecoveryNetwork(network); err != nil {
		return nil, err
	}
	groupKey, err := frostRecoveryGroupKey(vaultPubKey)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(frostRecoveryEnvelope{
		Protocol:    frostRecoveryProtocol,
		Version:     frostRecoveryVersion,
		Network:     network,
		VaultPubKey: vaultPubKey.String(),
		GroupKey:    groupKey,
		KeyBytesB64: base64.StdEncoding.EncodeToString(keyBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode frost recovery envelope: %w", err)
	}
	return append([]byte(frostRecoveryEnvelopeMagic), payload...), nil
}

func decodeFrostRecoveryEnvelope(data []byte, expectedNetwork string, expectedVault common.PubKey) ([]byte, error) {
	if !bytes.HasPrefix(data, []byte(frostRecoveryEnvelopeMagic)) {
		return nil, fmt.Errorf("frost recovery envelope magic/version mismatch")
	}
	if err := validateFrostRecoveryNetwork(expectedNetwork); err != nil {
		return nil, err
	}
	groupKey, err := frostRecoveryGroupKey(expectedVault)
	if err != nil {
		return nil, err
	}

	var envelope frostRecoveryEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data[len(frostRecoveryEnvelopeMagic):]))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("invalid frost recovery envelope: %w", err)
	}
	var extra any
	if trailingErr := decoder.Decode(&extra); !errors.Is(trailingErr, io.EOF) {
		if trailingErr == nil {
			return nil, fmt.Errorf("invalid frost recovery envelope: trailing JSON value")
		}
		return nil, fmt.Errorf("invalid frost recovery envelope trailer: %w", trailingErr)
	}
	if envelope.Protocol != frostRecoveryProtocol || envelope.Version != frostRecoveryVersion {
		return nil, fmt.Errorf("frost recovery protocol/version mismatch")
	}
	if envelope.Network != expectedNetwork {
		return nil, fmt.Errorf("frost recovery network mismatch")
	}
	if envelope.VaultPubKey != expectedVault.String() {
		return nil, fmt.Errorf("frost recovery vault pubkey mismatch")
	}
	if envelope.GroupKey != groupKey {
		return nil, fmt.Errorf("frost recovery group key mismatch")
	}
	keyBytes, err := base64.StdEncoding.Strict().DecodeString(envelope.KeyBytesB64)
	if err != nil {
		return nil, fmt.Errorf("invalid frost recovery key material encoding: %w", err)
	}
	if len(keyBytes) == 0 || int64(len(keyBytes)) > maxFrostRecoveryFileBytes {
		zeroBytes(keyBytes)
		return nil, fmt.Errorf("frost recovery key material size is outside bounds")
	}
	return keyBytes, nil
}

func validateFrostRecoveryNetwork(network string) error {
	switch network {
	case "mocknet", "chainnet", "stagenet", "mainnet":
		return nil
	default:
		return fmt.Errorf("invalid frost recovery network")
	}
}

func zeroBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

// encryptAtRestForRecovery at-rest encrypts recovered key material before it is
// written to disk. It fails hard rather than falling back to plaintext: the xmr
// sidecar rejects plaintext keyshares on restore, and key material must never be
// persisted or transported in the clear.
func encryptAtRestForRecovery(data []byte, seedPhrase string) ([]byte, error) {
	atRestKey := storage.DeriveAtRestKey(seedPhrase)
	encrypted, err := storage.EncryptAtRest(atRestKey, data)
	if err != nil {
		return nil, fmt.Errorf("failed to at-rest encrypt recovered keyshares: %w", err)
	}
	return encrypted, nil
}

func writeRecoveredBytes(path string, data []byte) error {
	recoveryArtifactMu.Lock()
	defer recoveryArtifactMu.Unlock()

	if len(data) == 0 || int64(len(data)) > maxFrostRecoveryFileBytes {
		return fmt.Errorf("recovery artifact size %d is outside 1..%d", len(data), maxFrostRecoveryFileBytes)
	}
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
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("failed to sync recovery temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close recovery temp file: %w", err)
	}

	if fi, statErr := os.Lstat(path); statErr == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("recovery destination is not a regular file: %s", path)
		}
		if fi.Size() != 0 {
			return fmt.Errorf("recovery file already exists: %s", path)
		}
		// A zero-byte regular file is not a recovered keyshare, but leaving it
		// in place makes every retry fail the no-overwrite guard below. Preserve
		// it for inspection, sync the rename, then publish the authenticated
		// replacement through the same temp-file path as a missing artifact.
		if err := quarantineRecoveryFileLocked(path); err != nil {
			return fmt.Errorf("failed to quarantine empty recovery file: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("failed to inspect recovery destination: %w", statErr)
	}
	// Recovery writers in this process are serialized by recoveryArtifactMu;
	// the node home is a single-process ownership boundary. Rename publishes a
	// single durable name, avoiding a crash state that leaves a second hard link
	// to key material under the temporary filename.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to publish recovery file: %w", err)
	}
	if err := syncRecoveryDirectory(filepath.Dir(path)); err != nil {
		return err
	}

	success = true
	return nil
}

func syncRecoveryDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open recovery directory: %w", err)
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return fmt.Errorf("failed to sync recovery directory: %w", err)
	}
	return nil
}
