package keeperv1

import "strings"

func isOperationalMimirV133(key string) bool {
	exactMatches := []string{
		"BurnSynths",
		"MintSynths",
	}
	for i := range exactMatches {
		if strings.EqualFold(key, exactMatches[i]) {
			return true
		}
	}

	// Past this point, compare only upper-case strings due to case sensitivity.
	key = strings.ToUpper(key)
	partialMatches := []string{
		"HALT",
		"PAUSE",
		"STOPSOLVENCYCHECK",
	}
	for i := range partialMatches {
		// Contains rather than HasPrefix to include cases like StreamingSwapPause.
		if strings.Contains(key, partialMatches[i]) {
			return true
		}
	}

	return false
}
