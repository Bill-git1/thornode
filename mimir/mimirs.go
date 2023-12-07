package mimir

import (
	"gitlab.com/thorchain/thornode/constants"
)

func getRef(refs []string) (reference string) {
	for _, ref := range refs {
		if len(ref) > 0 {
			reference = ref
		}
	}
	return
}

func NewAffiliateFeeBasisPointsMax(refs ...string) Mimir {
	id := AffiliateFeeBasisPointsMaxId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: int64(constants.MaxBasisPts),
		mimirType:    EconomicMimir,
		reference:    getRef(refs),
		tags:         []string{"economic", "affiliate fee"},
		description:  "Maximum fee to allow affiliates to set",
		legacyMimirKey: func(_ string) string {
			return "MaxAffiliateFeeBasisPoints"
		},
	}
}

func NewBondPause(refs ...string) Mimir {
	id := BondPauseId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: 0,
		reference:    getRef(refs),
		mimirType:    OperationalMimir,
		tags:         []string{"operational", "bond"},
		description:  "Pauses bonding (unbonding is still allowed)",
		legacyMimirKey: func(_ string) string {
			return "PauseBond"
		},
	}
}

func NewConfBasisPointValue(refs ...string) Mimir {
	id := ConfMultiplierBasisPointsId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: int64(constants.MaxBasisPts),
		reference:    getRef(refs),
		mimirType:    EconomicMimir,
		tags:         []string{"economic", "chain-client"},
		description:  "adjusts confirmation multiplier for chain client",
		legacyMimirKey: func(_ string) string {
			return ""
		},
	}
}

func NewMaxConfValue(refs ...string) Mimir {
	id := MaxConfirmationsId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: 0,
		reference:    getRef(refs),
		mimirType:    EconomicMimir,
		tags:         []string{"economic", "chain-client"},
		description:  "max confirmations for chain client",
		legacyMimirKey: func(_ string) string {
			return ""
		},
	}
}

func NewSwapperCloutLimit(refs ...string) Mimir {
	id := CloutSwapperLimitId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: 0,
		mimirType:    EconomicMimir,
		reference:    getRef(refs),
		tags:         []string{"economic", "clout"},
		description:  "Maximum clout applicable to an outbound txn",
		legacyMimirKey: func(_ string) string {
			return "CloutLimit"
		},
	}
}

func NewSwapperCloutReset(refs ...string) Mimir {
	id := CloutSwapperResetId
	return &mimir{
		id:           id,
		name:         mimirRefToStringMap[id],
		defaultValue: 720,
		mimirType:    EconomicMimir,
		reference:    getRef(refs),
		tags:         []string{"economic", "clout"},
		description:  "Amount of blocks before pending clout spent is reset",
		legacyMimirKey: func(_ string) string {
			return "CloutReset"
		},
	}
}
