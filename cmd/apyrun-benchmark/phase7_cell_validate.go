package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type phase7CellValidationVerdict struct {
	Valid          bool   `json:"valid"`
	SchemaVersion  int    `json:"schema_version"`
	ArtifactSHA256 string `json:"artifact_sha256"`
	JobID          string `json:"job_id"`
	ArrayJobID     string `json:"array_job_id"`
	ArrayTaskID    uint32 `json:"array_task_id"`
	ArmOrder       string `json:"arm_order"`
	SampleIndex    uint32 `json:"sample_index"`
	RequestedSlots uint32 `json:"requested_slots"`
	RepeatIndex    uint32 `json:"repeat_index"`
}

func runPhase7CellValidationMain(options benchmarkOptions) error {
	verdict, err := validatePhase7CellInput(options)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(verdict)
}

func validatePhase7CellInput(options benchmarkOptions) (phase7CellValidationVerdict, error) {
	if options.InputPath == "" || options.SchemaPath == "" || options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath != "" || options.LifecycleDensityChild {
		return phase7CellValidationVerdict{}, errors.New("validate-phase7-density-cell requires -input, -schema, -artifact, and -manifest only")
	}
	document, err := readRegularFileBounded(options.InputPath, maximumLifecycleDensityEvidenceBytes)
	if err != nil {
		return phase7CellValidationVerdict{}, fmt.Errorf("read Phase 7 cell fragment: %w", err)
	}
	schema, err := readRegularFileBounded(options.SchemaPath, maximumLifecycleDensitySchemaBytes)
	if err != nil {
		return phase7CellValidationVerdict{}, fmt.Errorf("read Phase 7 cell schema: %w", err)
	}
	if err := validateJSONSchemaDocument(document, schema, "phase7-paired-density-cell"); err != nil {
		return phase7CellValidationVerdict{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var fragment phase7PairedCellFragment
	if err := decoder.Decode(&fragment); err != nil {
		return phase7CellValidationVerdict{}, fmt.Errorf("decode Phase 7 cell fragment: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return phase7CellValidationVerdict{}, errors.New("Phase 7 cell fragment has trailing JSON")
	}
	if err := validatePhase7PairedCellFragment(fragment); err != nil {
		return phase7CellValidationVerdict{}, err
	}
	artifact, artifactBytes, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return phase7CellValidationVerdict{}, err
	}
	if uint64(len(artifactBytes)) != fragment.Artifact.SizeBytes || artifact.Filename != fragment.Artifact.Filename || artifact.SHA256 != fragment.Artifact.SHA256 || artifact.SourceCommit != fragment.Artifact.SourceCommit || artifact.ArtifactProfile != fragment.Artifact.ArtifactProfile || artifact.Target != fragment.Artifact.Target || artifact.Execution != fragment.Artifact.ExecutionModel {
		return phase7CellValidationVerdict{}, errors.New("Phase 7 cell artifact manifest identity drifted")
	}
	return phase7CellValidationVerdict{Valid: true, SchemaVersion: fragment.SchemaVersion, ArtifactSHA256: fragment.Artifact.SHA256, JobID: fragment.Allocation.JobID, ArrayJobID: fragment.Allocation.ArrayJobID, ArrayTaskID: fragment.Allocation.ArrayTaskID, ArmOrder: fragment.Allocation.ArmOrder, SampleIndex: fragment.Cell.SampleIndex, RequestedSlots: fragment.Cell.RequestedSlots, RepeatIndex: fragment.Cell.RepeatIndex}, nil
}
