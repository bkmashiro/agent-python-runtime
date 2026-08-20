package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	ss "github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	rc "github.com/bkmashiro/agent-python-runtime/runtime"
)

type record struct {
	SchemaVersion            string             `json:"schema_version"`
	Platform                 string             `json:"platform"`
	ArtifactSHA256           string             `json:"artifact_sha256"`
	CaseID                   string             `json:"case_id"`
	Treatment                string             `json:"treatment"`
	Stages                   map[string]float64 `json:"stage_milliseconds"`
	CriticalWallMilliseconds float64            `json:"critical_wall_milliseconds"`
	BodyFree                 bool               `json:"body_free"`
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func main() {
	path := os.Getenv("AGENT_RUNTIME_GUEST")
	artifact, err := os.ReadFile(path)
	must(err)
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(path), "manifest.json"))
	must(err)
	profile, err := rc.NewExecutionProfile("base", []string{"json"})
	must(err)
	profile, err = profile.BindVerifiedArtifact(rc.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: digest(artifact), ManifestSHA256: digest(manifest),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	must(err)
	config := rc.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Timeout = 100 * time.Second
	var candidate ss.Phase5Case
	found := false
	for _, item := range ss.Phase5Cases() {
		if item.ID == "scalar_add_64_gap250" {
			candidate, found = item, true
		}
	}
	if !found {
		panic("cost-profile case missing")
	}
	for _, treatment := range []string{"original", "prepared_region_derived"} {
		ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
		run(ctx, artifact, config, candidate, treatment)
		cancel()
	}
}

func run(ctx context.Context, artifact []byte, config rc.RunConfig, candidate ss.Phase5Case, treatment string) {
	operations, err := ss.NewPhase5ExactGuestOperations(artifact, config)
	must(err)
	defer operations.Teardown(context.Background())
	stages := map[string]float64{}
	timed := func(name string, operation func() error) {
		start := time.Now()
		must(operation())
		stages[name] = float64(time.Since(start).Microseconds()) / 1000
	}
	criticalStart := time.Now()
	gapStart := time.Now()
	gap, err := operations.BeginFinalizationGap(ctx, time.Duration(candidate.FinalizationGapMillis)*time.Millisecond)
	must(err)
	input := ss.Phase5ExecutionInput{Source: candidate.Source, FocusRegionIndex: candidate.FocusRegionIndex, OutputName: candidate.OutputName}
	if treatment == "original" {
		timed("final_guest_provision", func() error { return operations.Provision(ctx, ss.Phase5FinalCapacity) })
	} else {
		timed("analyzer_provision", func() error { return operations.Provision(ctx, ss.Phase5AnalyzerCapacity) })
		timed("scratch_guest_provision", func() error { return operations.Provision(ctx, ss.Phase5ScratchCapacity) })
		timed("final_guest_provision", func() error { return operations.Provision(ctx, ss.Phase5FinalCapacity) })
		timed("analysis", func() error { return operations.Analyze(ctx, input) })
		timed("patch_emission", func() error { return operations.EmitPatch(ctx, input) })
		timed("scratch_execution", func() error { return operations.ExecuteScratch(ctx, input) })
		timed("capsule_seal_transport", func() error { return operations.SealCapsule(ctx) })
	}
	must(gap.Wait(ctx))
	stages["finalization_gap"] = float64(time.Since(gapStart).Microseconds()) / 1000
	if treatment == "original" {
		timed("final_execution", func() error { return operations.ExecuteOriginal(ctx, input) })
	} else {
		timed("final_selection_validation", func() error { return operations.ValidateSelection(ctx, input) })
		timed("final_patch_compile_load", func() error { return operations.CompileDerived(ctx, input) })
		timed("final_execution", func() error { return operations.ExecuteDerived(ctx, input) })
	}
	timed("teardown", func() error { return operations.Teardown(ctx) })
	output := record{
		SchemaVersion: "pysolate.semantic-speculation-phase5r-cost-profile.v1", Platform: runtime.GOOS + "_" + runtime.GOARCH,
		ArtifactSHA256: digest(artifact), CaseID: candidate.ID, Treatment: treatment, Stages: stages,
		CriticalWallMilliseconds: float64(time.Since(criticalStart).Microseconds()) / 1000, BodyFree: true,
	}
	raw, err := json.Marshal(output)
	must(err)
	fmt.Println(string(raw))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
