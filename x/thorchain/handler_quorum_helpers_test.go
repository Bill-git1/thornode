package thorchain

import (
	"testing"

	"gitlab.com/thorchain/thornode/v3/common"
)

func TestDeduplicateAttestations(t *testing.T) {
	att1 := &common.Attestation{PubKey: []byte("pubkey1"), Signature: []byte("sig1")}
	att2 := &common.Attestation{PubKey: []byte("pubkey2"), Signature: []byte("sig2")}
	att3 := &common.Attestation{PubKey: []byte("pubkey3"), Signature: []byte("sig3")}

	// Test empty input
	result := deduplicateAttestations(nil, 10)
	if len(result) != 0 {
		t.Fatalf("expected 0 attestations, got %d", len(result))
	}

	// Test no duplicates
	result = deduplicateAttestations([]*common.Attestation{att1, att2, att3}, 10)
	if len(result) != 3 {
		t.Fatalf("expected 3 attestations, got %d", len(result))
	}

	// Test deduplication of identical pubkeys
	dup1 := &common.Attestation{PubKey: []byte("pubkey1"), Signature: []byte("sig1_dup")}
	result = deduplicateAttestations([]*common.Attestation{att1, dup1, att2, att1, att3}, 10)
	if len(result) != 3 {
		t.Fatalf("expected 3 unique attestations, got %d", len(result))
	}

	// Test maxCount cap
	result = deduplicateAttestations([]*common.Attestation{att1, att2, att3}, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 attestations (capped), got %d", len(result))
	}

	// Test maxCount cap with duplicates - should cap after dedup
	result = deduplicateAttestations([]*common.Attestation{att1, att1, att1, att2, att3}, 2)
	if len(result) != 2 {
		t.Fatalf("expected 2 attestations (deduped+capped), got %d", len(result))
	}

	// Test nil attestations are skipped
	result = deduplicateAttestations([]*common.Attestation{nil, att1, nil, att2}, 10)
	if len(result) != 2 {
		t.Fatalf("expected 2 attestations (nils skipped), got %d", len(result))
	}

	// Test all duplicates of one pubkey
	result = deduplicateAttestations([]*common.Attestation{att1, att1, att1, att1}, 10)
	if len(result) != 1 {
		t.Fatalf("expected 1 unique attestation, got %d", len(result))
	}
}
