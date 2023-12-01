package mimir

import (
	"strconv"
	"strings"
)

type Id int32

const (
	UnknownId Id = iota
	AffiliateFeeBasisPointsMaxId
	BondPauseId
	ConfMultiplierBasisPointsId // https://gitlab.com/thorchain/thornode/-/issues/1599
	MaxConfirmationsId          // https://gitlab.com/thorchain/thornode/-/issues/1761
)

var StringToId = map[string]Id{
	"unknown":                    UnknownId,
	"AffiliateFeeBasisPointsMax": AffiliateFeeBasisPointsMaxId,
	"BondPause":                  BondPauseId,
	"ConfMultiplierBasisPoints":  ConfMultiplierBasisPointsId,
	"MaxConfirmations":           MaxConfirmationsId,
}

var mimirRefToStringMap = map[Id]string{
	UnknownId:                    "unknown",
	AffiliateFeeBasisPointsMaxId: "AffiliateFeeBasisPointsMax",
	BondPauseId:                  "BondPause",
	ConfMultiplierBasisPointsId:  "ConfMultiplierBasisPoints",
	MaxConfirmationsId:           "MaxConfirmations",
}

// GetMimir fetches a mimir by id number
func GetMimir(id Id, ref string) (Mimir, bool) {
	switch id {
	case AffiliateFeeBasisPointsMaxId:
		return NewAffiliateFeeBasisPointsMax(ref), true
	case BondPauseId:
		return NewBondPause(ref), true
	case ConfMultiplierBasisPointsId:
		return NewConfBasisPointValue(ref), true
	case MaxConfirmationsId:
		return NewMaxConfValue(ref), true
	default:
		return nil, false
	}
}

// GetMimirByKey fetches a mimir by key
func GetMimirByKey(key string) (Mimir, bool) {
	idAndRef := strings.Split(key, "-")
	if len(idAndRef) != 2 {
		return nil, false
	}
	id, err := strconv.Atoi(idAndRef[0])
	if err != nil {
		return nil, false
	}
	return GetMimir(Id(id), idAndRef[1])
}
