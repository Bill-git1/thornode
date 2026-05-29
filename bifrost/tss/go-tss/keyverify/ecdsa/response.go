// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

// Response carries the outcome of a verify attempt. On success Err is nil; on
// failure the caller is expected to errors.As to *btss.Error to extract culprits.
type Response struct {
	PoolPubKey string
	Err        error
}
