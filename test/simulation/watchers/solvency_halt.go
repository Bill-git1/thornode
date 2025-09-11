package watchers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	. "gitlab.com/thorchain/thornode/v3/test/simulation/pkg/types"
)

func NewSolvencyHalt() *Watcher {
	cl := log.With().Str("watcher", "solvency").Logger()

	return &Watcher{
		Name:     "SolvencyHalt",
		Interval: time.Second,
		Fn: func(config *OpConfig) error {
			endpoint := fmt.Sprintf("%s/thorchain/mimir", thornodeURL)

			resp, err := httpClient.Get(endpoint)
			if err != nil {
				cl.Error().Err(err).Msg("failed to get mimir response")
				return nil
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				cl.Error().
					Int("status", resp.StatusCode).
					Msg("mimir response returned non-200 status")
				return nil
			}
			mimirRes := make(map[string]int64)
			if err = json.NewDecoder(resp.Body).Decode(&mimirRes); err != nil {
				cl.Error().Err(err).
					Msg("failed to decode mimir response")
				return nil
			}
			for k, v := range mimirRes {
				if strings.HasPrefix(k, "SOLVENCYHALT") {
					cl.Error().Str("mimir", k).Int64("value", v).Msg("solvency halt detected")
					return fmt.Errorf("solvency halt detected: %s=%d", k, v)
				}
			}
			return nil
		},
	}
}
