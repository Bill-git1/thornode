package common

import "math/rand"

const hexBytes = "abcdefABCDEF0123456789"
const (
	hexIdxBits = 5                 // 5 bits to represent a hex index
	hexIdxMask = 1<<hexIdxBits - 1 // All 1-bits, as many as hexIdxBits
)

// RandHexString generates random hex string used for test purpose
func RandHexString(n int) string {
	b := make([]byte, n)
	for i := 0; i < n; {
		// #nosec G404 this is a method only used for test purpose
		if idx := int(rand.Int31() & hexIdxMask); idx < len(hexBytes) {
			b[i] = hexBytes[idx]
			i++
		}
	}
	return string(b)
}
