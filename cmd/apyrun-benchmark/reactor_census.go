package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type reactorCensusBackend struct {
	Name              string `json:"name"`
	ResetMode         string `json:"reset_mode"`
	RequestedStrategy string `json:"requested_strategy"`
	ActiveStrategy    string `json:"active_strategy"`
	Fallback          bool   `json:"fallback"`
	FallbackReason    string `json:"fallback_reason,omitempty"`
}

type reactorCensusEvidence struct {
	SchemaVersion int                             `json:"schema_version"`
	EvidenceKind  string                          `json:"evidence_kind"`
	EvidenceClass string                          `json:"evidence_class"`
	Artifact      artifactIdentity                `json:"artifact"`
	HostSource    hostSourceIdentity              `json:"host_source"`
	Backend       reactorCensusBackend            `json:"backend"`
	State         wazeroengine.ReactorStateCensus `json:"state"`
}

var reactorCensusHostSource = currentHostSource

func (evidence reactorCensusEvidence) Validate() error {
	if evidence.SchemaVersion != 1 || evidence.EvidenceKind != "reactor-state-census" {
		return errors.New("reactor census identity is invalid")
	}
	if err := validateEvidenceClassProfile(evidence.EvidenceClass, evidence.Artifact.ArtifactProfile); err != nil {
		return err
	}
	if evidence.Artifact.Filename == "" || evidence.Artifact.SHA256 == "" || evidence.Artifact.SourceCommit == "" ||
		evidence.Artifact.Target != "wasm32-wasip1" || evidence.Artifact.Execution != "reactor" {
		return errors.New("reactor census artifact identity is incomplete")
	}
	if evidence.HostSource.Revision == "" || evidence.HostSource.Modified || evidence.Backend.Name == "" || evidence.Backend.ResetMode == "" ||
		evidence.Backend.RequestedStrategy == "" || evidence.Backend.ActiveStrategy == "" {
		return errors.New("reactor census Host or backend identity is incomplete")
	}
	if evidence.Backend.Fallback != (evidence.Backend.FallbackReason != "") {
		return errors.New("reactor census fallback identity is inconsistent")
	}
	if err := evidence.State.Validate(); err != nil {
		return fmt.Errorf("validate reactor state census: %w", err)
	}
	return nil
}

func runReactorCensus(options benchmarkOptions) (reactorCensusEvidence, error) {
	artifact, wasm, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return reactorCensusEvidence{}, err
	}
	if err := validateEvidenceClassProfile(options.Class, artifact.ArtifactProfile); err != nil {
		return reactorCensusEvidence{}, err
	}
	hostSource, err := reactorCensusHostSource()
	if err != nil {
		return reactorCensusEvidence{}, err
	}
	engine, err := wazeroengine.New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		return reactorCensusEvidence{}, fmt.Errorf("compile reactor for state census: %w", err)
	}
	defer engine.Close(context.Background())
	properties := engine.Properties()
	if err := properties.Validate(); err != nil {
		return reactorCensusEvidence{}, fmt.Errorf("validate census backend properties: %w", err)
	}
	evidence := reactorCensusEvidence{
		SchemaVersion: 1,
		EvidenceKind:  "reactor-state-census",
		EvidenceClass: options.Class,
		Artifact:      artifact,
		HostSource:    hostSource,
		Backend: reactorCensusBackend{
			Name: properties.Backend, ResetMode: string(properties.ResetMode),
			RequestedStrategy: string(properties.RequestedStrategy), ActiveStrategy: string(properties.ActiveStrategy),
			Fallback: properties.Fallback, FallbackReason: properties.FallbackReason,
		},
		State: engine.StateCensus(),
	}
	if err := evidence.Validate(); err != nil {
		return reactorCensusEvidence{}, err
	}
	return evidence, nil
}

func writeReactorCensus(options benchmarkOptions) (string, error) {
	evidence, err := runReactorCensus(options)
	if err != nil {
		return "", err
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return "", err
	}
	encoded = append(encoded, '\n')
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return "", err
	}
	return evidence.Artifact.SourceCommit, nil
}
