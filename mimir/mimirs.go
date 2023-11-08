package mimir

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
		defaultValue: 10_000,
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
