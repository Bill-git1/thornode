// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

// Request is the input to TssKeyVerify.VerifyKey.
type Request struct {
	PoolPubKey  string   // vault pubkey whose keyshare to re-attest
	Members     []string // thorpubs of every participant (must include localNodePubKey)
	BlockHeight int64    // thornode height the attempt is anchored to (used in msgID)
}
