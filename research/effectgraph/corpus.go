package effectgraph

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const CorpusSchemaVersion = "pysolate.effectgraph-corpus.v0"

var ErrInvalidCorpus = errors.New("invalid effect-aware opportunity corpus")

const (
	SourceNotGenerated = "not_generated"

	ProvenancePublicSynthetic       = "public_synthetic"
	ProvenancePublicRuntimeWorkload = "public_runtime_workload"

	OraclePureResult = "pure_result"
	OracleLiveRead   = "live_read"
	OracleWrite      = "write_effect"
	OracleException  = "exception"
	OracleOpaque     = "opaque"

	CandidatePreDispatch      = "pre_dispatch"
	CandidateOverlapWindow    = "overlap_window"
	CandidateExactRegionReuse = "exact_region_reuse"
	CandidateWASMPlacement    = "wasm_placement"
	CandidateNativePlacement  = "native_placement"
)

var (
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

const maxPrograms = 256
const maxHistoricalSeeds = 256
const maxCandidateOccurrences = 1024

type Target struct {
	ArtifactSourceCommit   string `json:"artifact_source_commit"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ImportClosureSHA256    string `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	ContractSetSHA256      string `json:"contract_set_sha256"`
}

type Candidate struct {
	Kind        string `json:"kind"`
	Occurrences uint32 `json:"occurrences"`
}

type Program struct {
	ID                   string      `json:"id"`
	Provenance           string      `json:"provenance"`
	SourcePath           string      `json:"source_path"`
	SourceSHA256         string      `json:"source_sha256"`
	OracleClass          string      `json:"oracle_class"`
	StructuralCandidates []Candidate `json:"structural_candidates"`
	InputsCanonical      bool        `json:"inputs_canonical"`
	OutputsCanonical     bool        `json:"outputs_canonical"`
}

type HistoricalSeed struct {
	IdentitySHA256            string `json:"identity_sha256"`
	TaskShape                 string `json:"task_shape"`
	SourceStatus              string `json:"source_status"`
	ProspectiveReplayRequired bool   `json:"prospective_replay_required"`
}

type Corpus struct {
	SchemaVersion   string           `json:"schema_version"`
	Target          Target           `json:"target"`
	Programs        []Program        `json:"programs"`
	HistoricalSeeds []HistoricalSeed `json:"historical_seeds"`
}

func DecodeCorpus(reader io.Reader) (Corpus, error) {
	var corpus Corpus
	if decodeDocument(reader, &corpus) != nil || corpus.Validate() != nil {
		return Corpus{}, ErrInvalidCorpus
	}
	return corpus, nil
}

func (corpus Corpus) Validate() error {
	if corpus.SchemaVersion != CorpusSchemaVersion || len(corpus.Programs) == 0 || len(corpus.Programs) > maxPrograms ||
		len(corpus.HistoricalSeeds) > maxHistoricalSeeds || !validTarget(corpus.Target) {
		return ErrInvalidCorpus
	}
	lastID := ""
	for _, program := range corpus.Programs {
		if !identifierPattern.MatchString(program.ID) || program.ID <= lastID ||
			(program.Provenance != ProvenancePublicSynthetic && program.Provenance != ProvenancePublicRuntimeWorkload) ||
			!validRelativeSourcePath(program.SourcePath) || !digestPattern.MatchString(program.SourceSHA256) ||
			!validOracle(program.OracleClass) || !sortedUniqueCandidates(program.StructuralCandidates) {
			return ErrInvalidCorpus
		}
		lastID = program.ID
	}
	lastIdentity := ""
	for _, seed := range corpus.HistoricalSeeds {
		if !digestPattern.MatchString(seed.IdentitySHA256) || seed.IdentitySHA256 <= lastIdentity ||
			!identifierPattern.MatchString(seed.TaskShape) || seed.SourceStatus != SourceNotGenerated || !seed.ProspectiveReplayRequired {
			return ErrInvalidCorpus
		}
		lastIdentity = seed.IdentitySHA256
	}
	return nil
}

func decodeDocument(reader io.Reader, destination any) error {
	data, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil || len(data) == 0 || len(data) > 1<<20 {
		return ErrInvalidCorpus
	}
	if rejectDuplicateJSON(data) != nil {
		return ErrInvalidCorpus
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidCorpus
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidCorpus
	}
	return nil
}

func rejectDuplicateJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	if consumeUniqueJSON(decoder, 0, &nodes) != nil {
		return ErrInvalidCorpus
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidCorpus
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 16384 {
		return ErrInvalidCorpus
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidCorpus
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	if delimiter == '{' {
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return ErrInvalidCorpus
			}
			seen[key] = true
			if consumeUniqueJSON(decoder, depth+1, nodes) != nil {
				return ErrInvalidCorpus
			}
		}
	} else if delimiter == '[' {
		for decoder.More() {
			if consumeUniqueJSON(decoder, depth+1, nodes) != nil {
				return ErrInvalidCorpus
			}
		}
	} else {
		return ErrInvalidCorpus
	}
	closing, err := decoder.Token()
	if err != nil || (delimiter == '{' && closing != json.Delim('}')) || (delimiter == '[' && closing != json.Delim(']')) {
		return ErrInvalidCorpus
	}
	return nil
}

func validTarget(target Target) bool {
	return commitPattern.MatchString(target.ArtifactSourceCommit) &&
		digestPattern.MatchString(target.ArtifactSHA256) &&
		digestPattern.MatchString(target.ExecutionProfileSHA256) &&
		digestPattern.MatchString(target.ImportClosureSHA256) &&
		digestPattern.MatchString(target.CapabilityPlanSHA256) &&
		digestPattern.MatchString(target.ContractSetSHA256)
}

func validRelativeSourcePath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") || filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	return value != ".." && !strings.HasPrefix(value, ".."+string(filepath.Separator)) && strings.HasSuffix(value, ".py")
}

func validOracle(value string) bool {
	switch value {
	case OraclePureResult, OracleLiveRead, OracleWrite, OracleException, OracleOpaque:
		return true
	default:
		return false
	}
}

func sortedUniqueCandidates(values []Candidate) bool {
	if values == nil || !sort.SliceIsSorted(values, func(i, j int) bool { return values[i].Kind < values[j].Kind }) {
		return false
	}
	for index, value := range values {
		switch value.Kind {
		case CandidatePreDispatch, CandidateOverlapWindow, CandidateExactRegionReuse, CandidateWASMPlacement, CandidateNativePlacement:
		default:
			return false
		}
		if value.Occurrences == 0 || value.Occurrences > maxCandidateOccurrences || index > 0 && values[index-1].Kind == value.Kind {
			return false
		}
	}
	return true
}
