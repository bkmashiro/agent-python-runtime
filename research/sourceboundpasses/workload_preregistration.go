package sourceboundpasses

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
)

const AuthoredWorkloadPreregistrationSchemaVersion = "pysolate.source-bound-pass-authored-workload-preregistration.v2"

var ErrInvalidAuthoredWorkloadPreregistration = errors.New("invalid authored source-bound pass workload preregistration")

type AuthoredWorkloadSource struct {
	ID              string
	Category        string
	Source          string
	RequiredImports []string
}

type AuthoredWorkloadCase struct {
	ID              string   `json:"id"`
	Category        string   `json:"category"`
	SourceSHA256    string   `json:"source_sha256"`
	SourceBytes     uint32   `json:"source_bytes"`
	RequiredImports []string `json:"required_imports"`
}

type AuthoredWorkloadPreregistration struct {
	SchemaVersion       string                 `json:"schema_version"`
	Classification      string                 `json:"classification"`
	IdentitySHA256      string                 `json:"identity_sha256"`
	Cases               []AuthoredWorkloadCase `json:"cases"`
	Treatments          []string               `json:"treatments"`
	MatchedDimensions   []string               `json:"matched_dimensions"`
	Oracles             []string               `json:"oracles"`
	NaturalCorpusAnchor string                 `json:"natural_corpus_anchor"`
	PrivateBodies       bool                   `json:"private_bodies_included"`
	ClaimBoundary       []string               `json:"claim_boundary"`
}

func AuthoredWorkloadSourcesV2() []AuthoredWorkloadSource {
	return []AuthoredWorkloadSource{
		{
			ID: "A01", Category: "repeated_repository_reads",
			Source: "first = workspace.read_text(\"alpha.py\")\nsecond = workspace.read_text(\"alpha.py\")\nresult = {\"first\": first, \"second\": second}\n",
		},
		{
			ID: "A02", Category: "bounded_projection",
			Source: "content = workspace.read_text(\"alpha.py\")\nresult = content.splitlines()[1:3]\n",
		},
		{
			ID: "A03", Category: "batch_reads",
			Source: "paths = [\"alpha.py\", \"beta.py\"]\nresult = [workspace.read_text(path) for path in paths]\n",
		},
		{
			ID: "A04", Category: "independent_reads",
			Source: "left = workspace.read_text(\"alpha.py\")\nright = workspace.read_text(\"beta.py\")\nresult = left + right\n",
		},
		{
			ID: "A05", Category: "pure_parsing",
			Source: "text = workspace.read_text(\"alpha.py\")\nlines = text.splitlines()\nresult = {\"count\": len(lines), \"first\": lines[0]}\n",
		},
		{
			ID: "A06", Category: "prepared_array_setup",
			Source:          "import numpy as np\ndataset = np.array([[1, 2], [3, 4]], dtype=np.int64)\nresult = {\"sum\": int(dataset.sum())}\n",
			RequiredImports: []string{"numpy"},
		},
		{
			ID: "S01", Category: "synthetic_predispatch_positive",
			Source: "weather = sources.read(\"weather\")\nrail = sources.read(\"rail\")\nattractions = sources.read(\"attractions\")\nresult = {\"weather\": weather[\"value\"], \"rail\": rail[\"value\"], \"attractions\": attractions[\"value\"]}\n",
		},
	}
}

func AuthoredWorkloadPreregistrationV2() AuthoredWorkloadPreregistration {
	sources := AuthoredWorkloadSourcesV2()
	cases := make([]AuthoredWorkloadCase, len(sources))
	for index, item := range sources {
		cases[index] = AuthoredWorkloadCase{
			ID: item.ID, Category: item.Category, SourceSHA256: digestWorkloadSource(item.Source),
			SourceBytes: uint32(len([]byte(item.Source))), RequiredImports: append([]string{}, item.RequiredImports...),
		}
	}
	value := AuthoredWorkloadPreregistration{
		SchemaVersion:  AuthoredWorkloadPreregistrationSchemaVersion,
		Classification: "AUTHORED_AND_SYNTHETIC_MECHANISM_CORPUS_NOT_NATURAL_PREVALENCE_OR_PERFORMANCE_EVIDENCE",
		Cases:          cases,
		Treatments:     []string{"pass_off", "semantic_pre_dispatch_only", "prepared_pure_region_only", "all_admitted"},
		MatchedDimensions: []string{
			"guest_artifact", "capability_plan", "workspace_seed", "inputs", "host_delay", "case_source",
		},
		Oracles: []string{
			"result_sha256", "exception_class_and_order", "logical_trace", "physical_dispatch_count",
			"terminal_disposition", "workspace_before_after_sha256",
		},
		NaturalCorpusAnchor: "docs/evidence/source-prefix-opportunity-census-v1.json",
		PrivateBodies:       false,
		ClaimBoundary: []string{
			"authored structural pass opportunity", "synthetic retained-mechanism capacity", "retained mechanism parity", "no deferred-pass speedup",
			"no natural prevalence inference", "no production performance inference",
		},
	}
	value.IdentitySHA256 = authoredWorkloadIdentity(value)
	return value
}

func (value AuthoredWorkloadPreregistration) Validate() error {
	expected := AuthoredWorkloadPreregistrationV2()
	if !reflect.DeepEqual(value, expected) || value.IdentitySHA256 != authoredWorkloadIdentity(value) {
		return ErrInvalidAuthoredWorkloadPreregistration
	}
	return nil
}

func EncodeAuthoredWorkloadPreregistration(value AuthoredWorkloadPreregistration) ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func authoredWorkloadIdentity(value AuthoredWorkloadPreregistration) string {
	value.IdentitySHA256 = ""
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestWorkloadSource(source string) string {
	digest := sha256.Sum256([]byte(source))
	return "sha256:" + hex.EncodeToString(digest[:])
}
