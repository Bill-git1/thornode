package ecdsa

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"

	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/crypto"
	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/crypto/paillier"
	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/ecdsa/keygen"
	"gitlab.com/thorchain/tss/tss-lib-private.git/v3/tss"
)

// completeSaveData returns a LocalPartySaveData that is non-nil in every
// field the v3 signing protocol reads. Suitable as a starting point that
// callers can mutate to express a specific invalid shape.
func completeSaveData(t *testing.T) keygen.LocalPartySaveData {
	t.Helper()
	bi := big.NewInt(1)
	pkN := big.NewInt(2)
	priv := &paillier.PrivateKey{
		PublicKey: paillier.PublicKey{N: pkN},
		LambdaN:   bi,
		PhiN:      bi,
		P:         bi,
		Q:         bi,
	}
	pubX, pubY := tss.S256().ScalarBaseMult(big.NewInt(7).Bytes())
	ecPub, err := crypto.NewECPoint(tss.S256(), pubX, pubY)
	if err != nil {
		t.Fatalf("NewECPoint: %v", err)
	}
	d := keygen.NewLocalPartySaveData(2)
	d.PaillierSK = priv
	d.NTildei, d.H1i, d.H2i = bi, bi, bi
	d.Alpha, d.Beta = bi, bi
	d.P, d.Q = bi, bi
	d.Xi, d.ShareID = bi, bi
	for i := range d.Ks {
		d.Ks[i] = big.NewInt(int64(i + 1))
		d.NTildej[i], d.H1j[i], d.H2j[i] = bi, bi, bi
		d.BigXj[i] = ecPub
		d.PaillierPKs[i] = &paillier.PublicKey{N: pkN}
	}
	d.ECDSAPub = ecPub
	return d
}

func TestValidateForKeysign_V3OK(t *testing.T) {
	d := completeSaveData(t)
	if !ValidateForKeysign(d) {
		t.Fatal("complete v3 save data must be valid for keysign")
	}
}

func TestValidateForKeysign_LegacyOK(t *testing.T) {
	d := completeSaveData(t)
	d.PaillierSK.P = nil
	d.PaillierSK.Q = nil
	if !ValidateForKeysign(d) {
		t.Fatal("legacy-shape save data must be valid for keysign")
	}
}

func TestValidateForKeysign_RejectsNilPaillierSK(t *testing.T) {
	d := completeSaveData(t)
	d.PaillierSK = nil
	if ValidateForKeysign(d) {
		t.Fatal("nil PaillierSK must be rejected")
	}
}

func TestValidateForKeysign_RejectsNilBigXjEntry(t *testing.T) {
	d := completeSaveData(t)
	d.BigXj[0] = nil
	if ValidateForKeysign(d) {
		t.Fatal("nil BigXj entry must be rejected")
	}
}

func TestValidateForKeysign_RejectsLengthMismatch(t *testing.T) {
	d := completeSaveData(t)
	d.PaillierPKs = d.PaillierPKs[:1]
	if ValidateForKeysign(d) {
		t.Fatal("mismatched per-party slice lengths must be rejected")
	}
}

func loadLegacyReference(t *testing.T, idx int) keygen.LocalPartySaveData {
	t.Helper()
	path := fmt.Sprintf("../../test_data/keysign_data/ecdsa_legacy/v0_1_6_reference/keygen_data_%d.json", idx)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var d keygen.LocalPartySaveData
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return d
}

func TestValidateForKeysign_RealLegacyReference(t *testing.T) {
	for i := 0; i < 4; i++ {
		d := loadLegacyReference(t, i)
		if !ValidateForKeysign(d) {
			t.Fatalf("keygen_data_%d: real v0.1.6 keyshare must satisfy ValidateForKeysign", i)
		}
	}
}
