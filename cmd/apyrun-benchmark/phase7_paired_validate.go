package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type phase7PairedValidationVerdict struct {
	Valid      bool `json:"valid"`
	Schema     int  `json:"schema_version"`
	Pairs      int  `json:"pairs"`
	Boundaries int  `json:"boundaries"`
}

type phase7PairedHeader struct {
	SchemaVersion    int               `json:"schema_version"`
	EvidenceClass    string            `json:"evidence_class"`
	Pairs            []json.RawMessage `json:"pairs"`
	BoundaryOutcomes []json.RawMessage `json:"boundary_outcomes"`
}

func runPhase7PairedValidationMain(options benchmarkOptions) error {
	if options.InputPath == "" || options.SchemaPath == "" || options.ArtifactPath != "" || options.ManifestPath != "" ||
		options.OutputPath != "" || options.LifecycleDensityChild {
		return errors.New("validate-phase7-paired-density requires -input and -schema only")
	}
	document, err := readRegularFileBounded(options.InputPath, maximumLifecycleDensityEvidenceBytes)
	if err != nil {
		return fmt.Errorf("read phase7 paired evidence: %w", err)
	}
	schema, err := readRegularFileBounded(options.SchemaPath, maximumLifecycleDensitySchemaBytes)
	if err != nil {
		return fmt.Errorf("read phase7 paired schema: %w", err)
	}
	if err := validateJSONSchemaDocument(document, schema, "phase7-paired-density"); err != nil {
		return err
	}
	var header phase7PairedHeader
	if err := json.Unmarshal(document, &header); err != nil {
		return fmt.Errorf("decode phase7 paired evidence header: %w", err)
	}
	if header.SchemaVersion != 2 || header.EvidenceClass != "phase7-paired-density" {
		return errors.New("phase7 paired evidence identity drifted")
	}
	return json.NewEncoder(os.Stdout).Encode(phase7PairedValidationVerdict{
		Valid: true, Schema: header.SchemaVersion, Pairs: len(header.Pairs), Boundaries: len(header.BoundaryOutcomes),
	})
}
