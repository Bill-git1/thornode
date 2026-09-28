package tss

import (
	"math/big"
	"testing"
)

func TestPartyIDStructuralCompatibility(t *testing.T) {
	second := NewPartyID("second", "", big.NewInt(2))
	first := NewPartyID("first", "", big.NewInt(1))
	if !first.ValidateBasic() || !second.ValidateBasic() {
		t.Fatal("valid structural party rejected")
	}

	sorted := SortPartyIDs([]*PartyID{second, first})
	if len(sorted) != 2 || sorted[0] != first || sorted[1] != second {
		t.Fatal("parties were not sorted by identity key")
	}
	if sorted[0].Index != 0 || sorted[1].Index != 1 {
		t.Fatal("sorted party indexes were not assigned")
	}
	if ErrRPCOnlyShim == nil {
		t.Fatal("RPC-only sentinel error is nil")
	}
}

func TestPartyIDInvalidWithoutIdentityKey(t *testing.T) {
	if NewPartyID("party", "", nil).ValidateBasic() {
		t.Fatal("party without an identity key was accepted")
	}
}
