package effectgraph

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

const ManifestSchemaVersion = "pysolate.effectgraph-manifest.v0"

type ManifestProgram struct {
	ID                   string      `json:"id"`
	Provenance           string      `json:"provenance"`
	SourcePath           string      `json:"source_path"`
	OracleClass          string      `json:"oracle_class"`
	StructuralCandidates []Candidate `json:"structural_candidates"`
	InputsCanonical      bool        `json:"inputs_canonical"`
	OutputsCanonical     bool        `json:"outputs_canonical"`
}

type Manifest struct {
	SchemaVersion   string            `json:"schema_version"`
	Programs        []ManifestProgram `json:"programs"`
	HistoricalSeeds []HistoricalSeed  `json:"historical_seeds"`
}

func DecodeManifest(reader io.Reader) (Manifest, error) {
	var manifest Manifest
	if decodeDocument(reader, &manifest) != nil || manifest.Validate() != nil {
		return Manifest{}, ErrInvalidCorpus
	}
	return manifest, nil
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion || len(manifest.Programs) == 0 || len(manifest.Programs) > maxPrograms || len(manifest.HistoricalSeeds) > maxHistoricalSeeds {
		return ErrInvalidCorpus
	}
	lastID := ""
	for _, program := range manifest.Programs {
		if !identifierPattern.MatchString(program.ID) || program.ID <= lastID ||
			(program.Provenance != ProvenancePublicSynthetic && program.Provenance != ProvenancePublicRuntimeWorkload) ||
			!validRelativeSourcePath(program.SourcePath) || !validOracle(program.OracleClass) || !sortedUniqueCandidates(program.StructuralCandidates) {
			return ErrInvalidCorpus
		}
		lastID = program.ID
	}
	lastIdentity := ""
	for _, seed := range manifest.HistoricalSeeds {
		if !digestPattern.MatchString(seed.IdentitySHA256) || seed.IdentitySHA256 <= lastIdentity ||
			!identifierPattern.MatchString(seed.TaskShape) || seed.SourceStatus != SourceNotGenerated || !seed.ProspectiveReplayRequired {
			return ErrInvalidCorpus
		}
		lastIdentity = seed.IdentitySHA256
	}
	return nil
}

func BuildCorpus(manifest Manifest, root string, target Target) (Corpus, error) {
	if manifest.Validate() != nil || !validTarget(target) || root == "" {
		return Corpus{}, ErrInvalidCorpus
	}
	programs := make([]Program, len(manifest.Programs))
	for index, row := range manifest.Programs {
		source, err := readCorpusSource(root, row.SourcePath)
		if err != nil {
			return Corpus{}, err
		}
		digest := sha256.Sum256(source)
		programs[index] = Program{
			ID: row.ID, Provenance: row.Provenance, SourcePath: row.SourcePath,
			SourceSHA256: fmt.Sprintf("sha256:%x", digest[:]), OracleClass: row.OracleClass,
			StructuralCandidates: append([]Candidate(nil), row.StructuralCandidates...),
			InputsCanonical:      row.InputsCanonical, OutputsCanonical: row.OutputsCanonical,
		}
	}
	corpus := Corpus{
		SchemaVersion: CorpusSchemaVersion, Target: target, Programs: programs,
		HistoricalSeeds: append([]HistoricalSeed(nil), manifest.HistoricalSeeds...),
	}
	if corpus.Validate() != nil {
		return Corpus{}, ErrInvalidCorpus
	}
	return corpus, nil
}

func readCorpusSource(root, relative string) ([]byte, error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, ErrInvalidCorpus
	}
	defer directory.Close()
	file, err := directory.Open(relative)
	if err != nil {
		return nil, ErrInvalidCorpus
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, ErrInvalidCorpus
	}
	source, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(source) == 0 || len(source) > 1<<20 {
		return nil, ErrInvalidCorpus
	}
	return source, nil
}

func EncodeCorpus(corpus Corpus) ([]byte, error) {
	if corpus.Validate() != nil {
		return nil, ErrInvalidCorpus
	}
	encoded, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return nil, ErrInvalidCorpus
	}
	return append(encoded, '\n'), nil
}
