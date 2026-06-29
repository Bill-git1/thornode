package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/thorchain/thornode/v3/common"
)

// GetValidateSeedPhraseCmd returns a side-effect-free command that validates a
// 24-word BIP39 mnemonic read from stdin, exiting non-zero if it is invalid.
// It is used by the entrypoint scripts to fail fast on a missing, empty, or
// corrupt signer seed phrase before any key material is provisioned.
func GetValidateSeedPhraseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate-seed-phrase",
		Short: "Validate a 24-word BIP39 mnemonic read from stdin",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := io.ReadAll(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("fail to read mnemonic: %w", err)
			}
			if !common.IsValidMnemonic(strings.TrimSpace(string(data))) {
				return fmt.Errorf("invalid seed phrase: must be a valid 24-word BIP39 mnemonic")
			}
			return nil
		},
	}
}
