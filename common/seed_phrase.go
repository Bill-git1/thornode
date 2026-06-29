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
// containerd to persist the secret to the worker node's disk. The
// SIGNER_SEED_PHRASE env var is only honored on mocknet for local development;
// on all other networks the phrase must come from SIGNER_SEED_PHRASE_FILE.
func GetSignerSeedPhrase() (string, error) {
	if path := os.Getenv("SIGNER_SEED_PHRASE_FILE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Error().Err(err).Str("path", path).Msg("failed to read SIGNER_SEED_PHRASE_FILE")
		} else if phrase := strings.TrimSpace(string(data)); !IsValidMnemonic(phrase) {
			log.Error().Str("path", path).Msg("SIGNER_SEED_PHRASE_FILE content is not a valid 24-word mnemonic")
		} else {
			return phrase, nil
		}
	}
	if CurrentChainNetwork == MockNet {
		if phrase := os.Getenv("SIGNER_SEED_PHRASE"); phrase != "" {
			return phrase, nil
		}
	}
	return "", errors.New("could not load signer seed phrase: SIGNER_SEED_PHRASE_FILE is unset or invalid (SIGNER_SEED_PHRASE env var is only honored on mocknet)")
}

// IsValidMnemonic reports whether phrase is a valid 24-word BIP39 mnemonic.
func IsValidMnemonic(phrase string) bool {
	return len(strings.Split(phrase, " ")) == 24 && bip39.IsMnemonicValid(phrase)
}
