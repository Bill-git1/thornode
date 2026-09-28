// Package tss contains only the non-operational party and routing data shapes
// required by the THORNode RPC binary's public P2P packages.
//
// Key generation, signing, party execution, MPC rounds, and proofs are
// intentionally not implemented.
package tss

import (
	"errors"
	"math/big"
	"sort"
)

// ErrRPCOnlyShim is the sentinel error for unavailable operational TSS APIs.
var ErrRPCOnlyShim = errors.New("tss-lib-private unavailable: RPC-only THORNode shim invoked")

// PartyID is the minimal public party identity used by THORNode's P2P helpers.
type PartyID struct {
	Id      string `json:"id"`
	Moniker string `json:"moniker"`
	Key     []byte `json:"key"`
	Index   int    `json:"index"`
}

// NewPartyID constructs structural party metadata. It performs no TSS work.
func NewPartyID(id, moniker string, key *big.Int) *PartyID {
	party := &PartyID{Id: id, Moniker: moniker}
	if key != nil {
		party.Key = append([]byte(nil), key.Bytes()...)
	}
	return party
}

// ValidateBasic reports whether the identity has the fields required by the
// public P2P conversion helpers.
func (p *PartyID) ValidateBasic() bool {
	return p != nil && p.Id != "" && len(p.Key) != 0
}

// KeyInt returns a copy of the party's public identity key as an integer.
func (p *PartyID) KeyInt() *big.Int {
	if p == nil {
		return new(big.Int)
	}
	return new(big.Int).SetBytes(p.Key)
}

// GetKey returns a copy of the party's public identity key bytes.
func (p *PartyID) GetKey() []byte {
	if p == nil {
		return nil
	}
	return append([]byte(nil), p.Key...)
}

// SortedPartyIDs is a deterministically ordered collection of party IDs.
type SortedPartyIDs []*PartyID

// SortPartyIDs orders parties by public identity key, using ID as a stable
// tie-breaker, and records each party's resulting index.
func SortPartyIDs(parties []*PartyID) SortedPartyIDs {
	sorted := append(SortedPartyIDs(nil), parties...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i], sorted[j]
		if left == nil {
			return right != nil
		}
		if right == nil {
			return false
		}
		if comparison := left.KeyInt().Cmp(right.KeyInt()); comparison != 0 {
			return comparison < 0
		}
		return left.Id < right.Id
	})
	for index, party := range sorted {
		if party != nil {
			party.Index = index
		}
	}
	return sorted
}

// MessageRouting is the routing metadata embedded in public THORNode P2P wire
// messages. It contains no party execution behavior.
type MessageRouting struct {
	From        *PartyID   `json:"from"`
	To          []*PartyID `json:"to"`
	IsBroadcast bool       `json:"is_broadcast"`
}
