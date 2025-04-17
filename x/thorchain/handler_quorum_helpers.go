package thorchain

import (
	"fmt"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"gitlab.com/thorchain/thornode/v3/common"
	"gitlab.com/thorchain/thornode/v3/common/cosmos"
)

func verifyQuorumAttestation(activeNodeAccounts NodeAccounts, signBz []byte, att *common.Attestation) (cosmos.AccAddress, error) {
	if att == nil {
		return nil, fmt.Errorf("attestation is nil")
	}
	if len(att.PubKey) == 0 {
		return nil, fmt.Errorf("pubkey is empty")
	}
	if len(att.Signature) == 0 {
		return nil, fmt.Errorf("signature is empty")
	}

	pk := secp256k1.PubKey{Key: att.PubKey}

	bech32Pub, err := cosmos.Bech32ifyPubKey(cosmos.Bech32PubKeyTypeAccPub, &pk)
	if err != nil {
		return nil, fmt.Errorf("fail to get bech32 pub key: %w", err)
	}

	// check if the signer is an active node account
	found := false
	for _, validator := range activeNodeAccounts {
		if bech32Pub == validator.PubKeySet.Secp256k1.String() {
			found = true
			break
		}
	}
	if !found {
		// can occur if a node account churns out before the tx is processed.
		return nil, fmt.Errorf("signer is not an active node account: %s", pk)
	}

	if !pk.VerifySignature(signBz, att.Signature) {
		return nil, fmt.Errorf("failed to verify signature: %s", pk)
	}

	return cosmos.AccAddress(pk.Address()), nil
}
