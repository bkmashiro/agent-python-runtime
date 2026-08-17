package agenttrajectory_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

const dayTripFixtureRoot = "testdata/day-trip-planning"

func TestDayTripFixtureIsCompletePublicAndIdentityPinned(t *testing.T) {
	fixture, err := agenttrajectory.LoadFixture(dayTripFixtureRoot)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.System == "" || len(fixture.Skills) != 3 || fixture.User.DepartureCity != "London" {
		t.Fatalf("incomplete fixture: %+v", fixture)
	}
	for _, skillID := range []string{"travel-research", "budget-checking", "itinerary-formatting"} {
		if fixture.Skills[skillID] == "" {
			t.Fatalf("missing skill %q", skillID)
		}
	}
	if fixture.Workspace.Request.CandidateIDs[0] != agenttrajectory.CandidateBrighton || fixture.Workspace.Request.CandidateIDs[1] != agenttrajectory.CandidateOxford {
		t.Fatalf("candidate order = %v", fixture.Workspace.Request.CandidateIDs)
	}
	if fixture.AggregateSHA256 != agenttrajectory.DayTripFixtureAggregateSHA256 {
		t.Fatalf("fixture aggregate = %s, want %s", fixture.AggregateSHA256, agenttrajectory.DayTripFixtureAggregateSHA256)
	}
	if fixture.SystemSHA256 != "sha256:07278e03b08e6bc41d7326b7dd76adb1a29237fe215a4b93775ddc1b06030ad3" || fixture.UserSHA256 != "sha256:f1d2510e58e74d6c2c1953fa92fe62313561f05917327a741ec69734ccf1e20b" {
		t.Fatalf("fixture component identities were not pinned: %+v", fixture)
	}
	for name, body := range fixture.Files {
		if bytes.Contains(body, []byte("PRIVATE")) || bytes.Contains(body, []byte("api_key")) || bytes.Contains(body, []byte("Authorization")) || bytes.Contains(body, []byte("/Users/")) {
			t.Fatalf("public fixture %s contains a private marker", name)
		}
	}
}

func TestDayTripFixtureRejectsMissingOrUnexpectedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skills", "extra"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"system.md",
		"skills/travel-research.md",
		"skills/budget-checking.md",
		"skills/itinerary-formatting.md",
		"user.json",
		"workspace/request.json",
		"workspace/deterministic-api-fixture.json",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "extra", "unexpected.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := agenttrajectory.LoadFixture(root); !errors.Is(err, agenttrajectory.ErrInvalidFixture) {
		t.Fatalf("invalid fixture accepted: %v", err)
	}
}

func TestContractRoundTripsAreCanonicalAndIdentityStable(t *testing.T) {
	brief := agenttrajectory.PlanningBrief{
		SchemaVersion: agenttrajectory.PlanningBriefSchemaVersion,
		Task:          "Plan a Saturday day trip for two people from London within a GBP 100 total budget.",
		CandidateIDs:  []string{agenttrajectory.CandidateBrighton, agenttrajectory.CandidateOxford},
	}
	encoded, identity, err := agenttrajectory.EncodePlanningBrief(brief)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := agenttrajectory.DecodePlanningBrief(encoded)
	if err != nil || !reflect.DeepEqual(decoded, brief) || decodedIdentity != identity || identity == "" {
		t.Fatalf("decoded=%+v identity=%s/%s err=%v", decoded, identity, decodedIdentity, err)
	}
	candidate := agenttrajectory.CandidateResponse{
		SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion,
		CandidateID:   agenttrajectory.CandidateBrighton,
		Summary:       "Brighton is the lower-cost coastal option with a dry forecast and an open pavilion.",
		PythonSource:  "import json\nforecast = travel.weather(\"brighton\")\ntrain = travel.rail(\"brighton\", travellers=2)\nsite = travel.attractions(\"brighton\")\nresult = {\"candidate_id\": \"brighton\", \"forecast\": forecast, \"train\": train, \"site\": site}",
	}
	candidateBytes, _, err := agenttrajectory.EncodeCandidateResponse(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agenttrajectory.DecodeCandidateResponse(candidateBytes); err != nil {
		t.Fatal(err)
	}
	selection := agenttrajectory.SelectionResponse{
		SchemaVersion:       agenttrajectory.SelectionResponseSchemaVersion,
		SelectedCandidateID: agenttrajectory.CandidateBrighton,
		Justification:       "Brighton stays comfortably within the fixed budget and has the better weather fixture.",
	}
	selectionBytes, _, err := agenttrajectory.EncodeSelectionResponse(selection)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agenttrajectory.DecodeSelectionResponse(selectionBytes); err != nil {
		t.Fatal(err)
	}
	final := agenttrajectory.FinalResponse{
		SchemaVersion:       agenttrajectory.FinalResponseSchemaVersion,
		SelectedCandidateID: agenttrajectory.CandidateBrighton,
		Itinerary:           "08:15 train London to Brighton; pavilion visit; seafront walk; 18:20 return train.",
		TotalCostGBP:        96.40,
	}
	finalBytes, _, err := agenttrajectory.EncodeFinalResponse(final)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := agenttrajectory.DecodeFinalResponse(finalBytes); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil || !bytes.Equal(compact.Bytes(), encoded) {
		t.Fatalf("canonical JSON has formatting whitespace: %s", encoded)
	}
}

func TestContractsRejectDuplicateUnknownNullTrailingAndOversizeJSON(t *testing.T) {
	brief := []byte(`{"schema_version":"pysolate.day-trip-planning-brief.v1","task":"Plan a Saturday day trip for two people from London within a GBP 100 total budget.","candidate_ids":["brighton","oxford"]}`)
	cases := map[string][]byte{
		"duplicate key": []byte(`{"schema_version":"pysolate.day-trip-planning-brief.v1","schema_version":"pysolate.day-trip-planning-brief.v1","task":"Plan a Saturday day trip for two people from London within a GBP 100 total budget.","candidate_ids":["brighton","oxford"]}`),
		"unknown field": append(append([]byte(nil), brief[:len(brief)-1]...), []byte(`,"unexpected":true}`)...),
		"null":          []byte(`{"schema_version":"pysolate.day-trip-planning-brief.v1","task":null,"candidate_ids":["brighton","oxford"]}`),
		"trailing":      append(append([]byte(nil), brief...), []byte("{}")...),
		"whitespace":    append([]byte(" "), brief...),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := agenttrajectory.DecodePlanningBrief(raw); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
				t.Fatalf("accepted %s: %v", name, err)
			}
		})
	}
	oversize := agenttrajectory.PlanningBrief{
		SchemaVersion: agenttrajectory.PlanningBriefSchemaVersion,
		Task:          strings.Repeat("x", agenttrajectory.MaxContractTextBytes+1),
		CandidateIDs:  []string{agenttrajectory.CandidateBrighton, agenttrajectory.CandidateOxford},
	}
	if _, _, err := agenttrajectory.EncodePlanningBrief(oversize); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
		t.Fatalf("oversize brief accepted: %v", err)
	}
}

func TestContractsRejectUnknownCandidatesAndMalformedFinalCosts(t *testing.T) {
	if err := agenttrajectory.ValidateSkillID("unknown-skill"); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
		t.Fatalf("unknown skill accepted: %v", err)
	}
	if err := agenttrajectory.ValidateSkillID("travel-research"); err != nil {
		t.Fatalf("known skill rejected: %v", err)
	}
	brief := agenttrajectory.PlanningBrief{SchemaVersion: agenttrajectory.PlanningBriefSchemaVersion, Task: "Plan a Saturday day trip for two people from London within a GBP 100 total budget.", CandidateIDs: []string{"brighton", "unknown"}}
	if _, _, err := agenttrajectory.EncodePlanningBrief(brief); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
		t.Fatalf("unknown brief candidate accepted: %v", err)
	}
	candidate := agenttrajectory.CandidateResponse{SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion, CandidateID: "unknown", Summary: "x", PythonSource: "result = {}"}
	if _, _, err := agenttrajectory.EncodeCandidateResponse(candidate); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
		t.Fatalf("unknown response candidate accepted: %v", err)
	}
	selection := agenttrajectory.SelectionResponse{SchemaVersion: agenttrajectory.SelectionResponseSchemaVersion, SelectedCandidateID: "unknown", Justification: "x"}
	if _, _, err := agenttrajectory.EncodeSelectionResponse(selection); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
		t.Fatalf("unknown selection candidate accepted: %v", err)
	}
	for _, cost := range []float64{-1, 100.01} {
		final := agenttrajectory.FinalResponse{SchemaVersion: agenttrajectory.FinalResponseSchemaVersion, SelectedCandidateID: agenttrajectory.CandidateOxford, Itinerary: "x", TotalCostGBP: cost}
		if _, _, err := agenttrajectory.EncodeFinalResponse(final); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
			t.Fatalf("invalid cost %.2f accepted: %v", cost, err)
		}
	}
}

func TestCandidatePythonPolicyRejectsDangerousCodeButAllowsBoundedTravelCapabilities(t *testing.T) {
	base := agenttrajectory.CandidateResponse{SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion, CandidateID: agenttrajectory.CandidateBrighton, Summary: "A bounded public proposal."}
	for name, source := range map[string]string{
		"network import":        "import requests\nresult = {}",
		"os import":             "import os\nresult = {}",
		"subprocess":            "from subprocess import run\nresult = {}",
		"absolute path":         "data = open(\"/etc/passwd\")\nresult = {}",
		"absolute path literal": "data = \"/tmp/output\"\nresult = {}",
		"parent path":           "data = travel.read(\"../secret\")\nresult = {}",
		"socket call":           "socket.connect((\"example.com\", 443))\nresult = {}",
		"eval":                  "result = eval(\"1+1\")",
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			candidate.PythonSource = source
			if _, _, err := agenttrajectory.EncodeCandidateResponse(candidate); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
				t.Fatalf("dangerous Python accepted: %v", err)
			}
		})
	}
	allowed := base
	allowed.PythonSource = "import json\nforecast = travel.weather(\"brighton\")\ntrains = travel.rail(\"brighton\", travellers=2)\nsite = travel.attractions(\"brighton\")\nresult = {\"forecast\": forecast, \"trains\": trains, \"site\": site}"
	if _, _, err := agenttrajectory.EncodeCandidateResponse(allowed); err != nil {
		t.Fatalf("bounded travel Python rejected: %v", err)
	}
}

func TestSelectionAndFinalRequireKnownCandidateAndNonEmptyOutput(t *testing.T) {
	for _, raw := range []string{
		`{"schema_version":"pysolate.day-trip-selection.v1","selected_candidate_id":"brighton","justification":""}`,
		`{"schema_version":"pysolate.day-trip-final.v1","selected_candidate_id":"brighton","itinerary":"","total_cost_gbp":90}`,
	} {
		if strings.Contains(raw, "selection") {
			if _, _, err := agenttrajectory.DecodeSelectionResponse([]byte(raw)); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
				t.Fatalf("empty selection justification accepted: %v", err)
			}
		} else if _, _, err := agenttrajectory.DecodeFinalResponse([]byte(raw)); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
			t.Fatalf("empty itinerary accepted: %v", err)
		}
	}
}

func TestStrictDecoderDoesNotAcceptJSONNullAtRoot(t *testing.T) {
	for name, decode := range map[string]func([]byte) error{
		"brief":     func(raw []byte) error { _, _, err := agenttrajectory.DecodePlanningBrief(raw); return err },
		"candidate": func(raw []byte) error { _, _, err := agenttrajectory.DecodeCandidateResponse(raw); return err },
		"selection": func(raw []byte) error { _, _, err := agenttrajectory.DecodeSelectionResponse(raw); return err },
		"final":     func(raw []byte) error { _, _, err := agenttrajectory.DecodeFinalResponse(raw); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := decode([]byte("null")); !errors.Is(err, agenttrajectory.ErrInvalidContract) {
				t.Fatalf("root null accepted: %v", err)
			}
		})
	}
}

func TestContractJSONTagsRemainMachineReadable(t *testing.T) {
	candidate := agenttrajectory.CandidateResponse{SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion, CandidateID: agenttrajectory.CandidateBrighton, Summary: "summary", PythonSource: "result = {}"}
	data, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"schema_version"`, `"candidate_id"`, `"summary"`, `"python_source"`} {
		if !bytes.Contains(data, []byte(field)) {
			t.Fatalf("missing JSON field %s in %s", field, data)
		}
	}
}
