package ecdsa

import (
	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
)

// ValidateForKeysign returns true when the keyshare carries every field the
// v3 ECDSA signing protocol actually reads.
//
// This is strictly weaker than keygen.LocalPreParams.ValidateWithProof — that
// function additionally requires PaillierSK.P, PaillierSK.Q, the top-level
// Alpha, Beta, P, Q used by ModProof and FacProof at keygen time. None of
// those are referenced from vendor/.../v3/ecdsa/signing/. Using
// ValidateWithProof at the keysign entry point rejects legacy keyshares for
// no protocol reason.
//
// If a future v3 signing change starts consuming a new field, add it here.
func ValidateForKeysign(d keygen.LocalPartySaveData) bool {
	if d.PaillierSK == nil ||
		d.PaillierSK.N == nil ||
		d.PaillierSK.LambdaN == nil ||
		d.PaillierSK.PhiN == nil {
		return false
	}
	if d.NTildei == nil || d.H1i == nil || d.H2i == nil {
		return false
	}
	if d.Xi == nil || d.ShareID == nil {
		return false
	}
	if d.ECDSAPub == nil {
		return false
	}
	n := len(d.Ks)
	if n == 0 ||
		len(d.NTildej) != n ||
		len(d.H1j) != n ||
		len(d.H2j) != n ||
		len(d.BigXj) != n ||
		len(d.PaillierPKs) != n {
		return false
	}
	for i := 0; i < n; i++ {
		if d.Ks[i] == nil ||
			d.NTildej[i] == nil ||
			d.H1j[i] == nil ||
			d.H2j[i] == nil ||
			d.BigXj[i] == nil ||
			d.PaillierPKs[i] == nil ||
			d.PaillierPKs[i].N == nil {
			return false
		}
	}
	return true
}
