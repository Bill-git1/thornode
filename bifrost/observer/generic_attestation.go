package observer

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"gitlab.com/thorchain/thornode/v3/common"
)

// AttestableItem represents any data type that can be attested
type AttestableItem interface {
	// Marshal serializes the item for signature verification
	GetSignablePayload() ([]byte, error)
}

// AttestMessage is an interface for messages containing an attestation
type AttestMessage interface {
	// GetAttestation returns the attestation from the message
	GetAttestation() *common.Attestation

	// GetSignablePayload returns the data that is signed for verification
	GetSignablePayload() ([]byte, error)
}

// Ensure all required types implement AttestableItem
var (
	_ AttestableItem = &common.ObservedTx{}
	_ AttestableItem = &common.NetworkFee{}
	_ AttestableItem = &common.Solvency{}
	_ AttestableItem = &common.ErrataTx{}
)

// ProcessAttestation processes an attestation message by checking for duplicates,
// verifying the signature against the payload, and adding it to the attestation state
func ProcessAttestation[T AttestMessage](attestations *[]attestationSentState, msg T) error {
	attestation := msg.GetAttestation()

	// Check for duplicates
	for _, item := range *attestations {
		if bytes.Equal(item.attestation.Signature, attestation.Signature) {
			// already have the signature, ignore
			return nil
		}
		if bytes.Equal(item.attestation.PubKey, attestation.PubKey) {
			// Unexpected: we should never have a different signature from the same pubkey for the same tx.
			return fmt.Errorf("signature already present for %s", attestation.PubKey)
		}
	}

	// Get the data to verify
	signBz, err := msg.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to get signable payload: %w", err)
	}

	// Verify the signature
	if err := verifySignature(signBz, attestation.Signature, attestation.PubKey); err != nil {
		return fmt.Errorf("signature verification failed for %s - %w", attestation.PubKey, err)
	}

	// Add the attestation
	*attestations = append(*attestations, attestationSentState{attestation: attestation, sent: false})
	return nil
}

// attestationSentState tracks an attestation and whether it has been sent
type attestationSentState struct {
	attestation *common.Attestation
	sent        bool
}

// AttestationState is a generic attestation state for any attestable item
type AttestationState[T AttestableItem] struct {
	// The item being attested
	Item T

	// List of attestations that have been collected
	attestations []attestationSentState

	// Timing information
	firstAttestationObserved time.Time
	initialAttestationsSent  time.Time
	quorumAttestationsSent   time.Time
	lastAttestationsSent     time.Time

	mu sync.Mutex
}

// NewAttestationState creates a new attestation state for the given item
func NewAttestationState[T AttestableItem](item T) *AttestationState[T] {
	return &AttestationState[T]{
		Item:                     item,
		attestations:             make([]attestationSentState, 0),
		firstAttestationObserved: time.Now(),
	}
}

// AddAttestation adds a new attestation to the state
func (s *AttestationState[T]) AddAttestation(attestation *common.Attestation) error {
	// Check for duplicates
	for _, item := range s.attestations {
		if bytes.Equal(item.attestation.Signature, attestation.Signature) {
			// already have the signature, ignore
			return nil
		}
		if bytes.Equal(item.attestation.PubKey, attestation.PubKey) {
			// Unexpected: we should never have a different signature from the same pubkey for the same item
			return fmt.Errorf("signature already present for %s", attestation.PubKey)
		}
	}

	// Get the marshaled data for signature verification
	signBz, err := s.Item.GetSignablePayload()
	if err != nil {
		return fmt.Errorf("fail to marshal item: %w", err)
	}

	// Verify the signature
	if err := verifySignature(signBz, attestation.Signature, attestation.PubKey); err != nil {
		return fmt.Errorf("signature verification failed for %s - %w", attestation.PubKey, err)
	}

	// Add the attestation
	s.attestations = append(s.attestations, attestationSentState{attestation: attestation, sent: false})
	return nil
}

// UnsentAttestations returns all attestations that have not been sent
func (s *AttestationState[T]) UnsentAttestations() []*common.Attestation {
	unsent := make([]*common.Attestation, 0)
	for _, item := range s.attestations {
		if !item.sent {
			unsent = append(unsent, item.attestation)
		}
	}
	return unsent
}

// AttestationsCopy returns a deep copy of all attestations
func (s *AttestationState[T]) AttestationsCopy() []*common.Attestation {
	atts := make([]*common.Attestation, len(s.attestations))
	for i, item := range s.attestations {
		atts[i] = &common.Attestation{
			PubKey:    append([]byte(nil), item.attestation.PubKey...),
			Signature: append([]byte(nil), item.attestation.Signature...),
		}
	}
	return atts
}

// UnsentCount returns the number of attestations that have not been sent
func (s *AttestationState[T]) UnsentCount() int {
	count := 0
	for _, item := range s.attestations {
		if !item.sent {
			count++
		}
	}
	return count
}

// AttestationCount returns the total number of attestations
func (s *AttestationState[T]) AttestationCount() int {
	return len(s.attestations)
}

// ShouldSendLate determines if late attestations should be sent
func (s *AttestationState[T]) ShouldSendLate(minTimeBetweenAttestations time.Duration) bool {
	if s.UnsentCount() == 0 {
		// nothing to send
		return false
	}

	if !s.lastAttestationsSent.IsZero() && time.Since(s.lastAttestationsSent) > minTimeBetweenAttestations {
		// we have sent attestations before, and it's been long enough to send again
		return true
	}

	if s.initialAttestationsSent.IsZero() && time.Since(s.firstAttestationObserved) > minTimeBetweenAttestations {
		// we haven't sent any attestations yet, and it's been long enough to send the first batch
		return true
	}

	return false
}

// ExpiredAfterQuorum determines if the attestation state can be pruned
func (s *AttestationState[T]) ExpiredAfterQuorum(lateObserveTimeout, nonQuorumTimeout time.Duration) bool {
	if !s.lastAttestationsSent.IsZero() && time.Since(s.lastAttestationsSent) > nonQuorumTimeout {
		// we haven't received a new attestation in a long time, stop tracking this item
		return true
	}

	if s.quorumAttestationsSent.IsZero() {
		// we haven't reached quorum yet, so we can't expire
		return false
	}

	if time.Since(s.quorumAttestationsSent) > lateObserveTimeout {
		// we have reached quorum, but it's been too long. Stop tracking this item.
		return true
	}

	return false
}

func (s *AttestationState[T]) State() string {
	return fmt.Sprintf("sent: %d, total: %d post-quorum: %t", s.UnsentCount(), len(s.attestations), !s.quorumAttestationsSent.IsZero())
}

// MarkAttestationsSent marks all attestations as sent and updates timestamps
func (s *AttestationState[T]) MarkAttestationsSent(isQuorum bool) {
	timestamp := time.Now()

	s.lastAttestationsSent = timestamp
	if s.initialAttestationsSent.IsZero() {
		s.initialAttestationsSent = timestamp
	}
	if isQuorum && s.quorumAttestationsSent.IsZero() {
		s.quorumAttestationsSent = timestamp
	}

	// Mark attestations as sent
	for i := range s.attestations {
		s.attestations[i].sent = true
	}
}

// verifySignature verifies that a signature is valid for a specific public key and data
var verifySignature = func(signBz []byte, signature []byte, attester []byte) error {
	pub := secp256k1.PubKey{Key: attester}
	if !pub.VerifySignature(signBz, signature) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}
