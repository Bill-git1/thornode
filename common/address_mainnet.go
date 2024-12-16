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

	return NoAddress, fmt.Errorf("address format not supported: %s", address)
}
