package suites

import (
	rand "math/rand/v2"
	"testing"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

func TestXMRCoverageIncludesExpectedActors(t *testing.T) {
	names := actorNames(XMRCoverage(rand.New(rand.NewPCG(1, 2))))

	for _, want := range []string{
		"VerifyLP-XMR.XMR",
		"Swap XMR.XMR => BTC.BTC",
		"Swap BTC.BTC => XMR.XMR",
		"SwapMemoless XMR.XMR => ETH.ETH",
		"WithdrawLP-XMR.XMR",
	} {
		if !names[want] {
			t.Fatalf("expected actor %q in XMR coverage suite", want)
		}
	}
}

func TestXMRPostChurnIncludesExpectedActor(t *testing.T) {
	names := actorNames(XMRPostChurn(rand.New(rand.NewPCG(3, 4))))
	if !names["XMRPostChurn BTC.BTC => XMR.XMR"] {
		t.Fatal("expected post-churn XMR swap actor in cluster suite")
	}
}

func actorNames(root interface{ WalkDepthFirst(func(*Actor) bool) bool }) map[string]bool {
	names := map[string]bool{}
	root.WalkDepthFirst(func(a *Actor) bool {
		names[a.Name] = true
		return true
	})
	return names
}
