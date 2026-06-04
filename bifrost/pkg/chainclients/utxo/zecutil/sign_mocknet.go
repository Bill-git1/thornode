//go:build mocknet
// +build mocknet

package zecutil

// https://github.com/zcash/zcash/blob/89f5ee5dec3fdfd70202baeaf74f09fa32bfb1a8/src/chainparams.cpp#L99
// https://github.com/zcash/zcash/blob/master/src/consensus/upgrades.cpp#L11
// activation levels are used for testnet because mainnet is already updated
// TODO: need implement own complete chain params and use them
var upgradeParams = []upgradeParam{
	// Zcash chain in mocknet starts with NU6.2 from block 0
	{0, []byte{0x30, 0xF3, 0x37, 0x54}},
	// Test in sign_mocknet.go expects overwinter hash for height 215039
	// Reaching that block height by regularly running the mocknet container
	// is highly unlikely
	{207500, []byte{0x19, 0x1B, 0xA8, 0x5B}},
}
