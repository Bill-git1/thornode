// TEMP: keyverify — remove this entire file with the keyverify rollout.
package ecdsa

import (
	"math/big"
	"testing"

	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"
	btss "gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"
)

// TestKeyverify_NoProofGuardsAreActive_InV3 documents and pins the v3
// invariant that backs the abort guards in tss_keyverify.go:
//
//	if params.NoProofMod() {
//	    return errors.New("keyverify aborted: Paillier-Blum ...")
//	}
//	if params.NoProofFac() {
//	    return errors.New("keyverify aborted: Π-fac ...")
//	}
//
// In v3, both methods always return false — the modproof and facproof gates
// are mandatory. So the guards never fire under v3 and our keyverify always
// runs with both proofs validated. A future tss-lib release that flipped the
// bypass back on (and made the methods return true) would silently re-enable
// running keyverify without those proofs — a real safety hole. This test
// catches that regression at unit-test time.
//
// If this test ever fails, do NOT just update the assertion — investigate
// why tss-lib re-introduced the bypass and whether the abort guards in
// VerifyKey are still the right behavior.
func TestKeyverify_NoProofGuardsAreActive_InV3(t *testing.T) {
	// Construct a minimal Parameters; field values don't matter to the
	// NoProofMod / NoProofFac accessors (they're hardcoded false in v3).
	partyIDs := []*btss.PartyID{
		btss.NewPartyID("0", "p0", big.NewInt(1)),
		btss.NewPartyID("1", "p1", big.NewInt(2)),
		btss.NewPartyID("2", "p2", big.NewInt(3)),
	}
	peerCtx := btss.NewPeerContext(partyIDs)
	params := btss.NewParameters(tss.S256(), peerCtx, partyIDs[0], 3, 1)

	if params.NoProofMod() {
		t.Fatal("Parameters.NoProofMod() = true — tss-lib has re-introduced the modproof bypass. The keyverify abort guard in tss_keyverify.go will now fire and the protocol will refuse to run. Investigate whether the bypass is intentional and update both the guard and this test deliberately.")
	}
	if params.NoProofFac() {
		t.Fatal("Parameters.NoProofFac() = true — tss-lib has re-introduced the facproof bypass. The keyverify abort guard in tss_keyverify.go will now fire and the protocol will refuse to run. Investigate whether the bypass is intentional and update both the guard and this test deliberately.")
	}
}
