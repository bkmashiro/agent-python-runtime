package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	fixedParentReportSHA256     = "sha256:5f504a138084f933ba0fd4f3bec7aede7076924ec3c2a5cfb8f05db3dd9a513f"
	fixedPreregistrationSHA256  = "sha256:f824f307a9fc4deaceca150c6f236686b1718c81820cf5362a79c7f256efe3e7"
	fixedArtifactSourceCommit   = "501daef99796c1af7cd7bab1e0ab712a199820b9"
	fixedArtifactSHA256         = "sha256:a443042fb080d22f8e352aca0d0c8a5c87a7801e8afcc603e174d75fbe11c69b"
	fixedArtifactManifestSHA256 = "sha256:c3bae8db19e0a372101dea11c6873f71ce849dd992b92ac3eba4a4352ddb4045"
	fixedAcceptedHarnessCommit  = "61112106e20959e5894414ca991f8bac2699dd92"
)

var eventFilePattern = regexp.MustCompile(`^task-([0-9]+)-turn-([0-9]+)-guest-request\.json$`)

type artifactManifest struct {
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
	} `json:"build"`
	Artifact struct {
		SHA256 string `json:"sha256"`
	} `json:"artifact"`
}

type guestRequest struct {
	RunID  string          `json:"run_id"`
	Code   string          `json:"code"`
	Inputs json.RawMessage `json:"inputs"`
}

type planDocument struct {
	SchemaVersion string                    `json:"schema_version"`
	MaxCalls      uint32                    `json:"max_calls"`
	Capabilities  []capability.Spec         `json:"capabilities"`
	Grants        []capability.GrantBinding `json:"grants"`
}

type cellProjection struct {
	SchemaVersion        string            `json:"schema_version"`
	TaskID               string            `json:"task_id"`
	CapabilityPlanSHA256 string            `json:"capability_plan_sha256"`
	PlanDocument         json.RawMessage   `json:"plan_document"`
	Receipt              receipt.Receipt   `json:"receipt"`
	RawBodies            map[string]string `json:"raw_bodies"`
	Content              json.RawMessage   `json:"content"`
	GrantPolicy          json.RawMessage   `json:"grant_policy"`
	RequestSHA256        string            `json:"request_sha256"`
	ResponseSHA256       string            `json:"response_sha256"`
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func strictDecode(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON contains trailing content")
	}
	return nil
}

func planProjections(raw json.RawMessage, expectedSHA string) ([]semantic.CapabilityProjection, map[string]string, error) {
	var document planDocument
	if err := strictDecode(raw, &document); err != nil || document.SchemaVersion != "pysolate.capability-plan.v7" || document.MaxCalls == 0 || len(document.Capabilities) == 0 {
		return nil, nil, errors.New("invalid private capability plan document")
	}
	canonical, err := json.Marshal(document)
	if err != nil || digestBytes(canonical) != expectedSHA {
		return nil, nil, errors.New("capability plan document identity mismatch")
	}
	projections := make([]semantic.CapabilityProjection, 0, len(document.Capabilities))
	effects := make(map[string]string, len(document.Capabilities))
	for _, spec := range document.Capabilities {
		if spec.Python == nil || spec.Name == "" || spec.EffectClass == "" {
			continue
		}
		projections = append(projections, semantic.CapabilityProjection{
			Name: spec.Name, EffectClass: spec.EffectClass, Playback: spec.Playback,
			Module: spec.Python.Module, Method: spec.Python.Method, GlobalAlias: spec.Python.GlobalAlias,
			Arguments: append([]string{}, spec.Python.Arguments...),
		})
		effects[spec.Name] = spec.EffectClass
	}
	sort.Slice(projections, func(i, j int) bool { return projections[i].Name < projections[j].Name })
	return projections, effects, nil
}

func eventIdentity(parent, task, turn, sourceSHA, planSHA string) string {
	return digestBytes([]byte(strings.Join([]string{"pysolate.source-prefix-census-event.v1", parent, task, turn, sourceSHA, planSHA}, "\x00")))
}

func currentHarnessCommit() (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("census harness lacks Go build identity")
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	revision := settings["vcs.revision"]
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(revision) || settings["vcs.modified"] != "false" || revision != fixedAcceptedHarnessCommit {
		return "", errors.New("census harness must be built from the fixed clean accepted commit")
	}
	return revision, nil
}

func secureRead(path, root string) ([]byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	rootAbsolute, err := filepath.Abs(root)
	if err != nil || absolute != rootAbsolute && !strings.HasPrefix(absolute, rootAbsolute+string(os.PathSeparator)) {
		return nil, errors.New("private corpus path escapes fixed root")
	}
	info, err := os.Lstat(absolute)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("private corpus input is not a regular file")
	}
	return os.ReadFile(absolute)
}

func writePrivate(path string, value []byte) error {
	if path == "" {
		return errors.New("private output is required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".source-prefix-census-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(value); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func verifyReceiptJoin(cell cellProjection, request guestRequest, analysis semantic.Analysis, sourceSHA string) error {
	if cell.Receipt.Source == nil || !receipt.ValidIdentity(cell.Receipt) ||
		cell.Receipt.CapabilityPlanSHA256 != cell.CapabilityPlanSHA256 || cell.Receipt.RunID != request.RunID ||
		cell.Receipt.Source.SourceSHA256 != sourceSHA || cell.Receipt.Source.DocumentID != semantic.SourceDocumentIdentity(analysis.SourceSHA256) ||
		cell.Receipt.Source.Capability != cell.Receipt.Capability {
		return errors.New("private receipt identity join mismatch")
	}
	matches := 0
	for _, call := range analysis.CallSites {
		if call.ID == cell.Receipt.Source.OccurrenceID && call.Capability == cell.Receipt.Capability &&
			call.DynamicOccurrence == cell.Receipt.Source.DynamicOccurrence && call.Span.StartLine == cell.Receipt.Source.StartLine &&
			call.Span.StartColumn == cell.Receipt.Source.StartColumn && call.Span.EndLine == cell.Receipt.Source.EndLine && call.Span.EndColumn == cell.Receipt.Source.EndColumn {
			matches++
		}
	}
	if matches != 1 {
		return errors.New("private receipt does not bind one exact Guest call site")
	}
	return nil
}

func run(ctx context.Context, artifactPath, parentReportPath, preregistrationPath, corpusRoot, outputPath string) error {
	harnessCommit, err := currentHarnessCommit()
	if err != nil {
		return err
	}
	parent, err := os.ReadFile(parentReportPath)
	if err != nil || digestBytes(parent) != fixedParentReportSHA256 {
		return errors.New("parent remediation report does not match fixed anchor")
	}
	preregistration, err := os.ReadFile(preregistrationPath)
	if err != nil || digestBytes(preregistration) != fixedPreregistrationSHA256 {
		return errors.New("census preregistration does not match fixed anchor")
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil || digestBytes(artifact) != fixedArtifactSHA256 {
		return errors.New("Guest artifact does not match fixed anchor")
	}
	manifestRaw, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil || digestBytes(manifestRaw) != fixedArtifactManifestSHA256 {
		return errors.New("Guest artifact manifest does not match fixed anchor")
	}
	var manifest artifactManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.Build.RepositoryCommit != fixedArtifactSourceCommit || "sha256:"+manifest.Artifact.SHA256 != fixedArtifactSHA256 {
		return errors.New("Guest artifact manifest identity mismatch")
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		return err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: fixedArtifactSHA256, ManifestSHA256: fixedArtifactManifestSHA256,
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		return err
	}
	cellsRoot := filepath.Join(corpusRoot, "cells")
	entries, err := os.ReadDir(cellsRoot)
	if err != nil {
		return err
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && eventFilePattern.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != 36 {
		return fmt.Errorf("frozen corpus denominator mismatch: got %d events", len(names))
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	runner, err := (wazeroengine.Factory{}).New(ctx, artifact, config)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	cases := make([]workflowbench.SourcePrefixCensusCase, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		matches := eventFilePattern.FindStringSubmatch(name)
		task, turn := matches[1], matches[2]
		requestRaw, err := secureRead(filepath.Join(cellsRoot, name), corpusRoot)
		if err != nil {
			return err
		}
		var request guestRequest
		if err := strictDecode(requestRaw, &request); err != nil || request.Code == "" || request.RunID == "" || string(request.Inputs) != "{}" {
			return errors.New("invalid private Guest request")
		}
		cellName := strings.TrimSuffix(name, "-guest-request.json") + ".json"
		cellRaw, err := secureRead(filepath.Join(cellsRoot, cellName), corpusRoot)
		if err != nil {
			return err
		}
		var cell cellProjection
		if err := strictDecode(cellRaw, &cell); err != nil || cell.TaskID != task || cell.Receipt.Source == nil || cell.Receipt.Capability == "" || cell.Receipt.Source.Capability != cell.Receipt.Capability || !receipt.ValidIdentity(cell.Receipt) {
			return errors.New("invalid private remediation cell")
		}
		sourceSHA := digestBytes([]byte(request.Code))
		if sourceSHA != cell.Receipt.Source.SourceSHA256 {
			return errors.New("private Guest source does not match receipt source identity")
		}
		projections, effects, err := planProjections(cell.PlanDocument, cell.CapabilityPlanSHA256)
		if err != nil || effects[cell.Receipt.Capability] != capability.EffectExternalRead {
			return errors.New("private READ capability plan mismatch")
		}
		analysisRequest := semantic.Request{
			Source: request.Code,
			Bindings: semantic.Bindings{
				ArtifactSHA256:         fixedArtifactSHA256,
				ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
				ImportClosureSHA256:    agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}),
				CapabilityPlanSHA256:   cell.CapabilityPlanSHA256,
			},
			Capabilities: projections,
		}
		analysis, err := semantic.Analyze(ctx, runner, analysisRequest)
		if err != nil {
			return fmt.Errorf("exact Guest semantic analysis failed: %w", err)
		}
		if err := verifyReceiptJoin(cell, request, analysis, sourceSHA); err != nil {
			return err
		}
		itemID := eventIdentity(fixedParentReportSHA256, task, turn, sourceSHA, cell.CapabilityPlanSHA256)
		if _, duplicate := seen[itemID]; duplicate {
			return errors.New("duplicate frozen census event identity")
		}
		seen[itemID] = struct{}{}
		row, err := workflowbench.ClassifySourcePrefixOpportunity(workflowbench.SourcePrefixCensusInput{
			ItemID: itemID, SourceBytes: len([]byte(request.Code)), Analysis: analysis, EffectClasses: effects,
		})
		if err != nil {
			return err
		}
		cases = append(cases, row)
	}
	evidence, err := workflowbench.BuildSourcePrefixCensusEvidence(workflowbench.SourcePrefixCensusBuild{
		ParentRemediationIdentity: fixedParentReportSHA256, PreregistrationSHA256: fixedPreregistrationSHA256,
		ArtifactSourceCommit: fixedArtifactSourceCommit,
		ArtifactSHA256:       fixedArtifactSHA256, HarnessSourceCommit: harnessCommit, Cases: cases,
	})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writePrivate(outputPath, encoded)
}

func main() {
	artifact := flag.String("artifact", "", "fixed exact Guest artifact")
	parent := flag.String("parent-report", "", "fixed public remediation report")
	preregistration := flag.String("preregistration", "", "fixed public census preregistration")
	corpus := flag.String("private-corpus", "", "private remediation-v2 root")
	output := flag.String("output", "", "private census evidence output")
	flag.Parse()
	if *artifact == "" || *parent == "" || *preregistration == "" || *corpus == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "artifact, parent report, preregistration, private corpus and output are required")
		os.Exit(2)
	}
	if err := run(context.Background(), *artifact, *parent, *preregistration, *corpus, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
