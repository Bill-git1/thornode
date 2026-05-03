package cosmos

import (
	"math/big"
	"testing"

	sdkmath "cosmossdk.io/math"
)

func TestParseUint128(t *testing.T) {
	tests := []struct {
		name   string
		data   []byte
		expect Uint
		err    bool
	}{
		{
			name:   "zero",
			data:   make([]byte, 16),
			expect: ZeroUint(),
		},
		{
			name: "one",
			data: append(
				[]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
				make([]byte, 8)...,
			),
			expect: NewUint(1),
		},
		{
			name: "max u64 fits in low half",
			data: append(
				[]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
				make([]byte, 8)...,
			),
			expect: NewUint(^uint64(0)),
		},
		{
			name: "value above 2^64 (high=1, low=0)",
			data: append(
				make([]byte, 8),
				[]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}...,
			),
			expect: sdkmath.NewUintFromBigInt(new(big.Int).Lsh(big.NewInt(1), 64)),
		},
		{
			name: "max u128 (all 0xff)",
			data: []byte{
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
				0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
			},
			expect: sdkmath.NewUintFromBigInt(
				new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1)),
			),
		},
		{
			name: "wrong length (15 bytes)",
			data: make([]byte, 15),
			err:  true,
		},
		{
			name: "wrong length (17 bytes)",
			data: make([]byte, 17),
			err:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseUint128(tc.data)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tc.expect) {
				t.Fatalf("ParseUint128(%x) = %s, want %s", tc.data, got, tc.expect)
			}
		})
	}
}

func TestMaxUint(t *testing.T) {
	a := NewUint(5)
	b := NewUint(7)
	if !MaxUint(a, b).Equal(b) {
		t.Fatalf("MaxUint(5, 7) want 7")
	}
	if !MaxUint(b, a).Equal(b) {
		t.Fatalf("MaxUint(7, 5) want 7")
	}
	if !MaxUint(a, a).Equal(a) {
		t.Fatalf("MaxUint(5, 5) want 5")
	}
	if !MaxUint(ZeroUint(), a).Equal(a) {
		t.Fatalf("MaxUint(0, 5) want 5")
	}
}

func TestPowerOfTen(t *testing.T) {
	if !PowerOfTen(0).Equal(NewUint(1)) {
		t.Fatalf("10^0 want 1")
	}
	if !PowerOfTen(1).Equal(NewUint(10)) {
		t.Fatalf("10^1 want 10")
	}
	if !PowerOfTen(8).Equal(NewUint(1e8)) {
		t.Fatalf("10^8 want 1e8")
	}
	if !PowerOfTen(18).Equal(sdkmath.NewUintFromString("1000000000000000000")) {
		t.Fatalf("10^18 mismatch")
	}
}
