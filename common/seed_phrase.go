package common

import (
	"errors"
	"os"
	"strings"

	"github.com/cosmos/go-bip39"
	"github.com/rs/zerolog/log"
)

// GetSignerSeedPhrase returns the signer seed phrase, preferring the file
// path in SIGNER_SEED_PHRASE_FILE over the SIGNER_SEED_PHRASE env var.
//
// The file path is preferred so node-launcher can mount the seed phrase as a
// tmpfs volume instead of passing it through the container env, which causes
// containerd to persist the secret to the worker node's disk.
func GetSignerSeedPhrase() (string, error) {
	if path := os.Getenv("SIGNER_SEED_PHRASE_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("failed to read SIGNER_SEED_PHRASE_FILE, falling back to env var")
		} else if phrase := strings.TrimSpace(string(data)); !isValidMnemonic(phrase) {
			log.Error().Str("path", path).Msg("SIGNER_SEED_PHRASE_FILE content is not a valid 24-word mnemonic, falling back to env var")
		} else {
			return phrase, nil
		}
	}
	if phrase := os.Getenv("SIGNER_SEED_PHRASE"); phrase != "" {
		return phrase, nil
	}
	return "", errors.New("neither SIGNER_SEED_PHRASE_FILE nor SIGNER_SEED_PHRASE is set")
}

func isValidMnemonic(phrase string) bool {
	return len(strings.Split(phrase, " ")) == 24 && bip39.IsMnemonicValid(phrase)
}
