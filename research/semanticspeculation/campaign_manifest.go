package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"sort"
)

type CampaignCoordinate struct {
	CaseID     string `json:"case_id"`
	TrialIndex uint32 `json:"trial_index"`
}

// Phase3CampaignCoordinates returns the full 7x5 preregistered grid in a
// version-independent seeded order.
func Phase3CampaignCoordinates() []CampaignCoordinate {
	type rankedCoordinate struct {
		coordinate CampaignCoordinate
		rank       [sha256.Size]byte
	}
	ranked := make([]rankedCoordinate, 0, len(Phase3SyntheticCases())*5)
	for _, fixture := range Phase3SyntheticCases() {
		for trial := uint32(1); trial <= 5; trial++ {
			coordinate := CampaignCoordinate{CaseID: fixture.ID, TrialIndex: trial}
			ranked = append(ranked, rankedCoordinate{
				coordinate: coordinate,
				rank:       sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%d\x00coordinate", phase3ShuffleSeed, fixture.ID, trial))),
			})
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return bytes.Compare(ranked[i].rank[:], ranked[j].rank[:]) < 0 })
	result := make([]CampaignCoordinate, len(ranked))
	for index, coordinate := range ranked {
		result[index] = coordinate.coordinate
	}
	return result
}

func FrozenPhase3Case(caseID string) (SyntheticCase, bool) {
	for _, fixture := range Phase3SyntheticCases() {
		if fixture.ID == caseID {
			return fixture, true
		}
	}
	return SyntheticCase{}, false
}
