package tss

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const (
	frostTranscriptHashDomain   = "frost_transcript_v1"
	frostTranscriptHashHexChars = 64
)

type frostTranscriptFailure struct {
	reason      string
	blamePubKey []string
}

func (f *frostTranscriptFailure) Error() string {
	return f.reason
}

func newFrostTranscriptFailure(reason string, blamePubKeys []string) *frostTranscriptFailure {
	return &frostTranscriptFailure{
		reason:      strings.TrimSpace(reason),
		blamePubKey: mergePubKeyLists(blamePubKeys),
	}
}

type frostTranscriptVerifier struct {
	round               string
	activeSet           *activeSignerSet
	participantIndexMap map[string]uint32
	totalParticipants   int

	payloadHashByTarget map[string]string
	votesByTarget       map[string]map[string]string
	verifiedTargets     map[string]struct{}
}

func newFrostTranscriptVerifier(
	round string,
	activeSet *activeSignerSet,
	participantIndexMap map[string]uint32,
) (*frostTranscriptVerifier, error) {
	if activeSet == nil || len(activeSet.originalByNormalized) == 0 {
		return nil, fmt.Errorf("active signer set is empty")
	}
	if len(participantIndexMap) == 0 {
		return nil, fmt.Errorf("participant index map is empty")
	}
	round = strings.TrimSpace(round)
	if round == "" {
		return nil, fmt.Errorf("round is required")
	}
	return &frostTranscriptVerifier{
		round:               round,
		activeSet:           activeSet,
		participantIndexMap: participantIndexMap,
		totalParticipants:   len(activeSet.originalByNormalized),
		payloadHashByTarget: make(map[string]string, len(activeSet.originalByNormalized)),
		votesByTarget:       make(map[string]map[string]string, len(activeSet.originalByNormalized)),
		verifiedTargets:     make(map[string]struct{}, len(activeSet.originalByNormalized)),
	}, nil
}

func (v *frostTranscriptVerifier) isComplete() bool {
	return len(v.verifiedTargets) == v.totalParticipants
}

func (v *frostTranscriptVerifier) recordPayload(
	targetSenderPubKey string,
	targetParticipantIndex uint32,
	payloadHash string,
) (bool, *frostTranscriptFailure) {
	targetNorm, targetOriginal, failure := v.validateTarget(targetSenderPubKey, targetParticipantIndex)
	if failure != nil {
		return false, failure
	}
	normalizedHash, err := normalizeTranscriptHash(payloadHash)
	if err != nil {
		return false, newFrostTranscriptFailure("invalid transcript payload hash", []string{targetOriginal})
	}

	if existing, ok := v.payloadHashByTarget[targetNorm]; ok {
		if existing != normalizedHash {
			return false, newFrostTranscriptFailure("sender equivocation detected for round payload", []string{targetOriginal})
		}
		return false, nil
	}
	v.payloadHashByTarget[targetNorm] = normalizedHash
	if failure := v.evaluateTarget(targetNorm); failure != nil {
		return true, failure
	}
	return true, nil
}

func (v *frostTranscriptVerifier) recordVerification(
	attesterPubKey string,
	targetSenderPubKey string,
	targetParticipantIndex uint32,
	payloadHash string,
) (bool, *frostTranscriptFailure) {
	attesterNorm, attesterOriginal, ok := v.resolveActivePubKey(attesterPubKey)
	if !ok {
		return false, newFrostTranscriptFailure("verification attester is not in active participant set", []string{attesterPubKey})
	}
	targetNorm, _, targetErr := v.validateTarget(targetSenderPubKey, targetParticipantIndex)
	if targetErr != nil {
		return false, newFrostTranscriptFailure("verification target is not in active participant set", []string{attesterOriginal})
	}
	normalizedHash, err := normalizeTranscriptHash(payloadHash)
	if err != nil {
		return false, newFrostTranscriptFailure("verification payload hash is invalid", []string{attesterOriginal})
	}

	votesByAttester, ok := v.votesByTarget[targetNorm]
	if !ok {
		votesByAttester = make(map[string]string, v.totalParticipants)
		v.votesByTarget[targetNorm] = votesByAttester
	}
	if existing, exists := votesByAttester[attesterNorm]; exists {
		if existing != normalizedHash {
			return false, newFrostTranscriptFailure("verification attester equivocation detected", []string{attesterOriginal})
		}
		return false, nil
	}
	votesByAttester[attesterNorm] = normalizedHash
	if failure := v.evaluateTarget(targetNorm); failure != nil {
		return true, failure
	}
	return true, nil
}

func (v *frostTranscriptVerifier) validateTarget(
	targetSenderPubKey string,
	targetParticipantIndex uint32,
) (string, string, *frostTranscriptFailure) {
	targetNorm, targetOriginal, ok := v.resolveActivePubKey(targetSenderPubKey)
	if !ok {
		return "", "", newFrostTranscriptFailure("target sender is not in active participant set", []string{targetSenderPubKey})
	}
	expectedIndex, ok := v.participantIndexMap[targetNorm]
	if !ok || expectedIndex != targetParticipantIndex {
		return "", "", newFrostTranscriptFailure("target participant index mismatch in transcript message", []string{targetOriginal})
	}
	return targetNorm, targetOriginal, nil
}

func (v *frostTranscriptVerifier) resolveActivePubKey(pubKey string) (string, string, bool) {
	norm := normalizePubKey(pubKey)
	if norm == "" {
		return "", "", false
	}
	original, ok := v.activeSet.originalByNormalized[norm]
	if !ok {
		return "", "", false
	}
	return norm, original, true
}

func (v *frostTranscriptVerifier) evaluateTarget(targetNorm string) *frostTranscriptFailure {
	payloadHash, hasPayload := v.payloadHashByTarget[targetNorm]
	if !hasPayload {
		return nil
	}
	votesByAttester, hasVotes := v.votesByTarget[targetNorm]
	if !hasVotes || len(votesByAttester) < v.totalParticipants {
		return nil
	}

	hashSet := make(map[string]struct{}, len(votesByAttester))
	var unanimousHash string
	for _, hash := range votesByAttester {
		hashSet[hash] = struct{}{}
		unanimousHash = hash
	}
	targetPubKey := v.activeSet.originalByNormalized[targetNorm]
	if len(hashSet) != 1 {
		return newFrostTranscriptFailure("transcript verification hash disagreement", []string{targetPubKey})
	}
	if unanimousHash != payloadHash {
		return newFrostTranscriptFailure("unanimous transcript hash mismatch for sender payload", []string{targetPubKey})
	}
	v.verifiedTargets[targetNorm] = struct{}{}
	return nil
}

func (v *frostTranscriptVerifier) missingPayloadPubKeys() []string {
	missing := make([]string, 0)
	for norm, original := range v.activeSet.originalByNormalized {
		if _, ok := v.payloadHashByTarget[norm]; ok {
			continue
		}
		missing = append(missing, original)
	}
	sort.Strings(missing)
	return missing
}

func (v *frostTranscriptVerifier) missingVerifierPubKeys() []string {
	missingByNorm := make(map[string]struct{})
	for targetNorm := range v.payloadHashByTarget {
		votesByAttester := v.votesByTarget[targetNorm]
		for attesterNorm := range v.activeSet.originalByNormalized {
			if votesByAttester != nil {
				if _, ok := votesByAttester[attesterNorm]; ok {
					continue
				}
			}
			missingByNorm[attesterNorm] = struct{}{}
		}
	}
	missing := make([]string, 0, len(missingByNorm))
	for norm := range missingByNorm {
		missing = append(missing, v.activeSet.originalByNormalized[norm])
	}
	sort.Strings(missing)
	return missing
}

func normalizeTranscriptHash(hash string) (string, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if len(hash) != frostTranscriptHashHexChars {
		return "", fmt.Errorf("hash must be %d hex chars", frostTranscriptHashHexChars)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("hash is not valid hex")
	}
	return hash, nil
}

func mergePubKeyLists(lists ...[]string) []string {
	seen := make(map[string]struct{})
	merged := make([]string, 0)
	for _, list := range lists {
		for _, pubKey := range list {
			pubKey = strings.TrimSpace(pubKey)
			if pubKey == "" {
				continue
			}
			if _, ok := seen[pubKey]; ok {
				continue
			}
			seen[pubKey] = struct{}{}
			merged = append(merged, pubKey)
		}
	}
	sort.Strings(merged)
	return merged
}

func transcriptTimeoutReason(payloadLabel string, missingPayloads, missingVerifiers []string) string {
	switch {
	case len(missingPayloads) > 0 && len(missingVerifiers) > 0:
		return fmt.Sprintf("timeout waiting for %s and transcript verifications", payloadLabel)
	case len(missingPayloads) > 0:
		return fmt.Sprintf("timeout waiting for %s", payloadLabel)
	case len(missingVerifiers) > 0:
		return fmt.Sprintf("timeout waiting for %s transcript verifications", payloadLabel)
	default:
		return "timeout waiting for transcript completion"
	}
}
