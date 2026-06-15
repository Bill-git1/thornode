package common

import (
	"time"
)

type TssConfig struct {
	// Party Timeout defines how long do we wait for the party to form
	PartyTimeout time.Duration
	// KeyGenTimeoutSeconds defines how long do we wait the keygen parties to pass messages along
	KeyGenTimeout time.Duration
	// FrostKeygenTimeout defines how long do we wait the FROST DKG to complete
	// each round. FROST is a 3-round p2p protocol and needs its own timeout,
	// independent of KeyGenTimeout which controls ECDSA/EDDSA keygen timing.
	FrostKeygenTimeout time.Duration
	// KeySignTimeoutSeconds defines how long do we wait keysign
	KeySignTimeout time.Duration
	// Pre-parameter define the pre-parameter generations timeout
	PreParamTimeout time.Duration
	// KeyverifyRoundTimeout bounds the inner per-round wait inside the
	// keyverify processVerify dispatch (bifrost/tss/go-tss/keyverify/ecdsa).
	// Decoupled from KeyGenTimeout because keyverify is a transitional path
	// with different deployment-latency expectations (notably operators
	// running bifrost behind proxy/WireGuard hairpins, where 4-of-N TSS
	// coordination over the public-internet hop exceeds the keygen budget).
	// Zero falls back to DefaultKeyverifyRoundTimeout in the ecdsa package.
	KeyverifyRoundTimeout time.Duration
	// KeyverifyRetryInterval is the cadence of the keyverify per-attempt
	// recovery ticker. Zero falls back to DefaultKeyverifyRetryInterval in
	// the keyverify/ecdsa package. TEMP: keyverify.
	KeyverifyRetryInterval time.Duration
	// KeyverifyMaxRetries caps recovery re-sends per artifact within one
	// keyverify attempt. Zero falls back to DefaultKeyverifyMaxRetries.
	// TEMP: keyverify.
	KeyverifyMaxRetries int
	// enable the tss monitor
	EnableMonitor bool
}
