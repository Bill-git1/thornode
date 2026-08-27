package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSeedPhraseIgnoresUnrelatedConfig(t *testing.T) {
	const validMnemonic = "abandon abandon abandon abandon abandon abandon abandon abandon " +
		"abandon abandon abandon abandon abandon abandon abandon abandon " +
		"abandon abandon abandon abandon abandon abandon abandon art"

	binary := filepath.Join(t.TempDir(), "thornode")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build thornode: %v\n%s", err, output)
	}

	tests := []struct {
		name       string
		config     string
		mnemonic   string
		wantErr    bool
		wantOutput string
	}{
		{name: "scientific integer", config: "1.14092546e+08", mnemonic: validMnemonic},
		{name: "invalid config", config: "not-an-integer", mnemonic: validMnemonic},
		{
			name:       "valid 12-word",
			config:     "1",
			mnemonic:   "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
			wantOutput: "NO keyshare disaster-recovery backup",
		},
		{
			// 24 wordlist words, invalid checksum: the shape the pre-checksum
			// validator accepted, so already-bonded nodes may run with it — must
			// stay accepted (exit 0) with a warning, not block startup.
			name:   "legacy checksum-invalid 24-word",
			config: "1",
			mnemonic: "abandon abandon abandon abandon abandon abandon abandon abandon " +
				"abandon abandon abandon abandon abandon abandon abandon abandon " +
				"abandon abandon abandon abandon abandon abandon abandon abandon",
			wantOutput: "INVALID BIP39 checksum",
		},
		{
			name:       "invalid mnemonic",
			config:     "not-an-integer",
			mnemonic:   "not a valid mnemonic",
			wantErr:    true,
			wantOutput: "invalid seed phrase",
		},
		{
			// Shorter lengths are newly accepted, so they get no checksum-compat
			// path: 12 wordlist words with a bad checksum stay rejected.
			name:       "checksum-invalid 12-word stays rejected",
			config:     "1",
			mnemonic:   "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon",
			wantErr:    true,
			wantOutput: "invalid seed phrase",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			validate := exec.Command(binary, "validate-seed-phrase")
			validate.Stdin = strings.NewReader(test.mnemonic)
			validate.Env = append(
				os.Environ(),
				"HOME="+t.TempDir(),
				"BIFROST_CHAINS_BSC_BLOCK_SCANNER_START_BLOCK_HEIGHT="+test.config,
			)
			output, err := validate.CombinedOutput()
			if test.wantErr {
				if err == nil {
					t.Fatal("validate seed phrase unexpectedly succeeded")
				}
				if !strings.Contains(string(output), test.wantOutput) {
					t.Fatalf("validate seed phrase output %q does not contain %q", output, test.wantOutput)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate seed phrase: %v\n%s", err, output)
			}
			if test.wantOutput != "" && !strings.Contains(string(output), test.wantOutput) {
				t.Fatalf("validate seed phrase output %q does not contain %q", output, test.wantOutput)
			}
		})
	}
}
