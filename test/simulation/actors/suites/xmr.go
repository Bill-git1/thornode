package suites

import (
	rand "math/rand/v2"

	"gitlab.com/thorchain/thornode/v3/common"
	acommon "gitlab.com/thorchain/thornode/v3/test/simulation/actors/common"
	"gitlab.com/thorchain/thornode/v3/test/simulation/actors/core"
	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

func XMRCoverage(rng *rand.Rand) *Actor {
	a := NewActor("XMRCoverage", rng)
	if !acommon.XMRChainEnabled() {
		return a
	}

	a.Append(core.NewVerifyLPActor(common.XMRAsset, rng))
	a.Append(core.NewSwapActor(common.XMRAsset, common.BTCAsset, rng))
	a.Append(core.NewSwapActor(common.BTCAsset, common.XMRAsset, rng))
	a.Append(core.NewSwapMemolessActor(common.XMRAsset, common.ETHAsset, core.MemolessTypeRef, rng))
	a.Append(core.NewWithdrawLPActor(common.XMRAsset, rng))

	return a
}

func XMRPostChurn(rng *rand.Rand) *Actor {
	a := NewActor("XMRPostChurn", rng)
	if !acommon.XMRChainEnabled() {
		return a
	}
	a.Append(core.NewXMRPostChurnActor(rng))
	return a
}

func XMRKeygenJail(rng *rand.Rand) *Actor {
	a := NewActor("XMRKeygenJail", rng)
	a.Append(core.NewXMRKeygenJailActor(rng))
	return a
}

func XMRSpentRefTracking(rng *rand.Rand) *Actor {
	a := NewActor("XMRSpentRefTracking", rng)
	if !acommon.XMRChainEnabled() {
		return a
	}
	a.Append(core.NewXMRSpentRefVerifyActor(rng))
	return a
}
