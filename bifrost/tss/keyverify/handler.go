// TEMP: keyverify — remove this entire file with the keyverify rollout.
package keyverify

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog"
)

// Handler returns an http.HandlerFunc serving GET /status/keyverify.
// Response body: JSON array of VaultStatus, one per pubkey that has been
// attempted (no entry for vaults with neither .ok nor .fail).
//
// When disabled is true the scheduler is not running, so the body would be
// indistinguishable from "enabled but nothing attempted yet" (empty array or
// stale markers). The X-Keyverify-Disabled response header makes the disabled
// state observable without changing the body shape existing consumers parse.
func Handler(store *MarkerStore, logger zerolog.Logger, disabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		statuses, err := store.List()
		if err != nil {
			logger.Error().Err(err).Msg("keyverify: list markers")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if statuses == nil {
			statuses = []VaultStatus{}
		}
		buf, err := json.MarshalIndent(statuses, "", "  ")
		if err != nil {
			logger.Error().Err(err).Msg("keyverify: marshal response")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if disabled {
			w.Header().Set("X-Keyverify-Disabled", "true")
		}
		if _, err := w.Write(buf); err != nil {
			logger.Error().Err(err).Msg("keyverify: write response")
		}
	}
}
