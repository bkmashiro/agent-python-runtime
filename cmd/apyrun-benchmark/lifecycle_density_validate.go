package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	maximumLifecycleDensityEvidenceBytes = 32 * 1024 * 1024
	maximumLifecycleDensitySchemaBytes   = 1 * 1024 * 1024
)

type lifecycleDensityValidationVerdict struct {
	Valid          bool   `json:"valid"`
	SchemaVersion  int    `json:"schema_version"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	Strategy       string `json:"strategy"`
	Samples        int    `json:"samples"`
}

func runLifecycleDensityValidationMain(options benchmarkOptions) error {
	verdict, err := validateLifecycleDensityInput(options)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(verdict)
}

func validateLifecycleDensityInput(options benchmarkOptions) (lifecycleDensityValidationVerdict, error) {
	if options.InputPath == "" || options.SchemaPath == "" || options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath != "" || options.LifecycleDensityChild {
		return lifecycleDensityValidationVerdict{}, errors.New("validate-lifecycle-density requires -input, -schema, -artifact, and -manifest only")
	}
	evidenceBytes, err := readRegularFileBounded(options.InputPath, maximumLifecycleDensityEvidenceBytes)
	if err != nil {
		return lifecycleDensityValidationVerdict{}, fmt.Errorf("read lifecycle-density evidence: %w", err)
	}
	schemaBytes, err := readRegularFileBounded(options.SchemaPath, maximumLifecycleDensitySchemaBytes)
	if err != nil {
		return lifecycleDensityValidationVerdict{}, fmt.Errorf("read lifecycle-density schema: %w", err)
	}
	if err := validateLifecycleDensitySchema(evidenceBytes, schemaBytes); err != nil {
		return lifecycleDensityValidationVerdict{}, err
	}
	if err := runtimeevidence.ValidateLifecycleDensityJSON(evidenceBytes); err != nil {
		return lifecycleDensityValidationVerdict{}, err
	}
	var evidence runtimeevidence.LifecycleDensityEvidence
	if err := json.Unmarshal(evidenceBytes, &evidence); err != nil {
		return lifecycleDensityValidationVerdict{}, fmt.Errorf("decode validated lifecycle-density evidence: %w", err)
	}
	artifact, artifactBytes, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return lifecycleDensityValidationVerdict{}, err
	}
	if err := evidence.ValidateArtifactBytes(artifactBytes); err != nil {
		return lifecycleDensityValidationVerdict{}, err
	}
	if evidence.Artifact.Filename != artifact.Filename || evidence.Artifact.SHA256 != artifact.SHA256 ||
		evidence.Artifact.SizeBytes != uint64(artifact.Size) || evidence.Artifact.SourceCommit != artifact.SourceCommit ||
		evidence.Artifact.ArtifactProfile != artifact.ArtifactProfile || evidence.Artifact.Target != artifact.Target ||
		evidence.Artifact.ExecutionModel != artifact.Execution {
		return lifecycleDensityValidationVerdict{}, errors.New("lifecycle-density evidence artifact manifest identity drifted")
	}
	return lifecycleDensityValidationVerdict{
		Valid: true, SchemaVersion: evidence.SchemaVersion, ArtifactSHA256: evidence.Artifact.SHA256,
		Strategy: evidence.Strategy.Active, Samples: len(evidence.Samples),
	}, nil
}

func validateLifecycleDensitySchema(evidenceBytes, schemaBytes []byte) error {
	if err := rejectDuplicateJSONDocument(schemaBytes); err != nil {
		return fmt.Errorf("lifecycle-density schema JSON is invalid: %w", err)
	}
	if err := rejectDuplicateJSONDocument(evidenceBytes); err != nil {
		return fmt.Errorf("lifecycle-density evidence JSON is invalid: %w", err)
	}
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		return fmt.Errorf("decode lifecycle-density schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://agent-python-runtime.invalid/benchmark/v1/lifecycle-density.schema.json"
	if err := compiler.AddResource(schemaURL, schemaDocument); err != nil {
		return fmt.Errorf("load lifecycle-density schema: %w", err)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("compile lifecycle-density schema: %w", err)
	}
	var document any
	if err := json.Unmarshal(evidenceBytes, &document); err != nil {
		return fmt.Errorf("decode lifecycle-density evidence: %w", err)
	}
	if err := compiled.Validate(document); err != nil {
		return fmt.Errorf("validate lifecycle-density JSON Schema: %w", err)
	}
	return nil
}
