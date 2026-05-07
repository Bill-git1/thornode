package watchers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	keeperv1 "gitlab.com/thorchain/thornode/v3/x/thorchain/keeper/v1"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

var (
	invariantRecheckAttempts = 2
	invariantRecheckDelay    = 2 * time.Second
)

type invariantResponse struct {
	Broken    bool
	Invariant string
	Msg       []string
}

func fetchInvariant(cl *http.Client, endpoint string) (invariantResponse, error) {
	resp, err := cl.Get(endpoint)
	if err != nil {
		return invariantResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return invariantResponse{}, fmt.Errorf("invariant returned non-200 status: %d", resp.StatusCode)
	}

	invRes := invariantResponse{}
	if err = json.NewDecoder(resp.Body).Decode(&invRes); err != nil {
		return invariantResponse{}, err
	}

	return invRes, nil
}

func NewInvariants() *Watcher {
	cl := log.With().Str("watcher", "invariants").Logger()

	// gather the list of all invariants to watch
	invariants := []string{}
	k := keeperv1.KVStore{}
	for _, ir := range k.InvariantRoutes() {
		invariants = append(invariants, ir.Route)
	}

	return &Watcher{
		Name:     "Invariants",
		Interval: 10 * time.Second,
		Fn: func(config *OpConfig) error {
			for _, invariant := range invariants {
				endpoint := fmt.Sprintf("%s/thorchain/invariant/%s", thornodeURL, invariant)

				invRes, err := fetchInvariant(httpClient, endpoint)
				if err != nil {
					cl.Error().Err(err).Str("invariant", invariant).Msg("failed to get invariant")
					continue
				}

				if invRes.Broken {
					transient := false
					for attempt := 0; attempt < invariantRecheckAttempts; attempt++ {
						time.Sleep(invariantRecheckDelay)

						recheck, recheckErr := fetchInvariant(httpClient, endpoint)
						if recheckErr != nil {
							cl.Error().Err(recheckErr).
								Str("invariant", invariant).
								Int("attempt", attempt+1).
								Msg("failed to recheck invariant")
							continue
						}
						if !recheck.Broken {
							transient = true
							cl.Warn().
								Str("invariant", invariant).
								Int("attempt", attempt+1).
								Msg("transient invariant failure cleared on recheck")
							break
						}
						invRes = recheck
					}
					if transient {
						continue
					}

					msg := strings.Join(invRes.Msg, ", ")
					err = fmt.Errorf("invariant %s is broken: %s", invRes.Invariant, msg)
					cl.Error().Err(err).Msg("invariant is broken")
					return err
				}
			}
			return nil
		},
	}
}
