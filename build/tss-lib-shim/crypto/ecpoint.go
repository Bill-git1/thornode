// Package crypto contains the non-operational ECPoint shape required by the
// THORNode RPC binary's public conversion helpers.
//
// This package implements no cryptographic operations.
package crypto

import (
	"errors"
	"math/big"
)

// ErrRPCOnlyShim is the sentinel error for unavailable operational TSS APIs.
var ErrRPCOnlyShim = errors.New("tss-lib-private unavailable: RPC-only THORNode shim invoked")

// ECPoint is the minimal structural representation referenced by THORNode's
// public P2P conversion package. The RPC build has no constructor for populated
// points; its zero value is intentionally unusable for key conversion.
type ECPoint struct {
	x *big.Int
	y *big.Int
}

// X returns the point's X coordinate.
func (p *ECPoint) X() *big.Int {
	if p == nil {
		return nil
	}
	return p.x
}

// Y returns the point's Y coordinate.
func (p *ECPoint) Y() *big.Int {
	if p == nil {
		return nil
	}
	return p.y
}
