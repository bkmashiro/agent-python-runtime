package mechanismcampaign

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

func candidateSourceChunks(candidateID, modelSource string) ([]string, error) {
	modelSource = strings.TrimSpace(modelSource)
	if modelSource == "" {
		modelSource = fmt.Sprintf("weather = travel.weather(%q)\nrail = travel.rail(%q, travellers=2)\nattraction = travel.attractions(%q)\nresult = {\"candidate_id\": %q, \"origin\": inputs[\"origin\"], \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}", candidateID, candidateID, candidateID, candidateID)
	}
	lines := strings.Split(modelSource, "\n")
	if len(lines) < 4 || strings.TrimSpace(lines[0]) != fmt.Sprintf("weather = travel.weather(%q)", candidateID) ||
		strings.TrimSpace(lines[1]) != fmt.Sprintf("rail = travel.rail(%q, travellers=2)", candidateID) ||
		strings.TrimSpace(lines[2]) != fmt.Sprintf("attraction = travel.attractions(%q)", candidateID) {
		return nil, errors.New("selected model source does not satisfy the preregistered prefix shape")
	}
	chunks := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		chunks = append(chunks, line+"\n")
	}
	chunks = append(chunks, "import json\n", "with open(\"/workspace/candidate-result.json\", \"w\", encoding=\"utf-8\") as handle:\n    json.dump({\"candidate_id\": result[\"candidate_id\"], \"total_cost_gbp\": result[\"total_cost_gbp\"]}, handle, sort_keys=True, separators=(\",\", \":\"))\n")
	return chunks, nil
}

// CandidateSourceChunks reconstructs the exact source chunks used by the
// preregistered day-trip generator. Projectors use it to expose each observed
// source.statement.complete prefix without duplicating the chunking contract.
func CandidateSourceChunks(candidateID, modelSource string) ([]string, error) {
	return candidateSourceChunks(candidateID, modelSource)
}

func configSource(executed string) string {
	marker := "\nimport json\nwith open(\"/workspace/candidate-result.json\""
	if index := strings.LastIndex(executed, marker); index >= 0 {
		return strings.TrimSpace(executed[:index])
	}
	return strings.TrimSpace(executed)
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func digestTextValue(value string) string { return digestBytes([]byte(value)) }
