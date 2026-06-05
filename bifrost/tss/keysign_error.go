package tss

import (
	"errors"
	"fmt"

	tssMessages "gitlab.com/thorchain/thornode/v3/bifrost/p2p/messages"

	"gitlab.com/thorchain/thornode/v3/x/thorchain/types"
)

var ErrNotSelectedForKeysign = errors.New("not selected for keysign party")

// KeysignError is a custom error create to include which party to blame
type KeysignError struct {
	Blame types.Blame
}

// NewKeysignError create a new instance of KeysignError
func NewKeysignError(blame types.Blame) KeysignError {
	return KeysignError{
		Blame: blame,
	}
}

// Error implement error interface
func (k KeysignError) Error() string {
	return fmt.Sprintf("fail to complete TSS keysign, reason:%s, round:%s, culprit:%+v", k.Blame.FailReason, k.Blame.Round, k.Blame.BlameNodes)
}

// IsTerminalRound reports whether the blame round was the terminal keysign
// phase for the algo — secp256k1 KEYSIGN9 (the round that emits the signature
// share in tss-lib v3) or ed25519 EDDSAKEYSIGN3. Only the terminal round
// matters for the signer's vault-stick retry path: a withholding-exploit
// node has the signature at hand only after the terminal-round message has
// been computed locally, so failures earlier in the protocol can safely
// reschedule to a different vault. Pre-v3 KEYSIGN7 blames are not accepted
// — a node running v3 will only ever emit KEYSIGN9 for its own terminal
// failures, and on-chain history with KEYSIGN7 markers is consumed by the
// chain-side freeze handler, not the per-signer retry path.
func (k KeysignError) IsTerminalRound() bool {
	round := types.CanonicalTssRound(k.Blame.Round, "")
	return round == tssMessages.KEYSIGN9 || round == tssMessages.EDDSAKEYSIGN3
}
