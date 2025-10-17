//go:build !mocknet
// +build !mocknet

package common

import (
	"fmt"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcutil"
	dogchaincfg "github.com/eager7/dogd/chaincfg"
	"github.com/eager7/dogutil"
	bchchaincfg "github.com/gcash/bchd/chaincfg"
	"github.com/gcash/bchutil"
	ltcchaincfg "github.com/ltcsuite/ltcd/chaincfg"
	"github.com/ltcsuite/ltcutil"
	"github.com/mr-tron/base58"
)

// newAddress in this file with build tags checks Mainnet(/Stagenet)-specific addresses.
func newAddress(address string) (Address, error) {
	var outputAddr interface{}

	// Check other BTC address formats with mainnet
	outputAddr, err := btcutil.DecodeAddress(address, &chaincfg.MainNetParams)
	switch outputAddr.(type) {
	case *btcutil.AddressPubKey:
		// AddressPubKey format is not supported by THORChain.
	default:
		if err == nil {
			return Address(address), nil
		}
	}

	// Check other LTC address formats with mainnet
	outputAddr, err = ltcutil.DecodeAddress(address, &ltcchaincfg.MainNetParams)
	switch outputAddr.(type) {
	case *ltcutil.AddressPubKey:
		// AddressPubKey format is not supported by THORChain.
	default:
		if err == nil {
			return Address(address), nil
		}
	}

	// Check BCH address formats with mainnet
	outputAddr, err = bchutil.DecodeAddress(address, &bchchaincfg.MainNetParams)
	switch outputAddr.(type) {
	case *bchutil.AddressPubKey:
		// AddressPubKey format is not supported by THORChain.
	default:
		if err == nil {
			return Address(address), nil
		}
	}

	// Check DOGE address formats with mainnet
	outputAddr, err = dogutil.DecodeAddress(address, &dogchaincfg.MainNetParams)
	switch outputAddr.(type) {
	case *dogutil.AddressPubKey:
		// AddressPubKey format is not supported by THORChain.
	default:
		if err == nil {
			return Address(address), nil
		}
	}

	// Check ED25519 (base58 encoded) addresses - SOL addresses must be 32 bytes long
	res, decodeErr := base58.Decode(address)
	if decodeErr == nil && len(res) == 32 {
		return Address(address), nil
	}

	// Check ZEC address formats (mainnet: t1, t3 prefixes; shielded: z, u)
	if len(address) >= 26 && len(address) <= 95 {
		if (address[:2] == "t1" || address[:2] == "t3") ||
			(address[0] == 'z' || address[0] == 'u') {
			return Address(address), nil
		}
	}

	return NoAddress, fmt.Errorf("address format not supported: %s", address)
}
