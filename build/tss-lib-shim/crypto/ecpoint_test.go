package crypto

import "testing"

func TestECPointZeroValueFailsClosed(t *testing.T) {
	var point ECPoint
	if point.X() != nil || point.Y() != nil {
		t.Fatal("zero-value RPC-only ECPoint unexpectedly contains coordinates")
	}
	if ErrRPCOnlyShim == nil {
		t.Fatal("RPC-only sentinel error is nil")
	}
}
