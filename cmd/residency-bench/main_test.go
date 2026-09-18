package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestWorkloadSourceComputesBeforeWaitAndReadsAfter(t *testing.T) {
	source := workloadSource(64)
	compute := strings.Index(source, "computed =")
	wait := strings.Index(source, "wait_ok = testio.wait()")
	read := strings.Index(source, "actual_sum =")
	if compute < 0 || wait < 0 || read < 0 || !(compute < wait && wait < read) {
		t.Fatalf("source order does not keep NumPy compute -> wait -> full read: %q", source)
	}
	if !strings.Contains(source, "np.arange(n, dtype=np.int64)") || !strings.Contains(source, "np.sum(data)") {
		t.Fatalf("source is not a real NumPy working-set computation")
	}
}

func TestScheduleHasEveryArmPerRandomizedBlock(t *testing.T) {
	trials := makeSchedule(5, 17)
	if len(trials) != 15 {
		t.Fatalf("trial count=%d", len(trials))
	}
	for block := 0; block < 5; block++ {
		seen := map[arm]bool{}
		for _, trial := range trials {
			if trial.Block == block {
				seen[trial.Arm] = true
			}
		}
		for _, selected := range arms {
			if !seen[selected] {
				t.Fatalf("block %d missing %s", block, selected)
			}
		}
	}
	first := makeSchedule(1, 17)
	second := makeSchedule(1, 18)
	if first[0].Arm == second[0].Arm && first[1].Arm == second[1].Arm && first[2].Arm == second[2].Arm {
		t.Fatal("block order did not respond to seed")
	}
}

func TestRunConfigUsesExplicitThreeArmStrategies(t *testing.T) {
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"numpy"})
	if err != nil {
		t.Fatal(err)
	}
	opts := options{workloadMiB: 64, waitMs: 1000, pressureThreshold: 0.8, cold: 10 * time.Millisecond, pageout: 20 * time.Millisecond}
	for _, selected := range arms {
		config, err := runConfig(profile, selected, opts)
		if err != nil {
			t.Fatalf("%s config: %v", selected, err)
		}
		if config.ColdIO == nil || config.ColdIO.Strategy != strategyForArm(selected) {
			t.Fatalf("%s policy=%+v", selected, config.ColdIO)
		}
	}
}

func TestParseGuestResponseChecksExactResult(t *testing.T) {
	correct, err := parseGuestResponse([]byte(`{"status":"ok","result":{"wait_ok":true,"sum":35184367894528,"computed":105553103683584,"offset":0}}`), 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	if correct.Exact == nil || !*correct.Exact {
		t.Fatalf("correctness=%+v", correct)
	}
}

func TestBenchmarkValidationRejectsShortWaitAndFullPressureBudget(t *testing.T) {
	opts := options{blocks: 1, workloadMiB: 64, waitMs: 999, pressureMiB: 0, cold: time.Millisecond, pressureThreshold: 0.8}
	if err := validateOptions(opts); err == nil {
		t.Fatal("wait shorter than one second was accepted")
	}
	opts.waitMs = 1000
	opts.pressureMiB = maxPressureMiB
	if err := validateOptions(opts); err == nil {
		t.Fatal("pressure budget at 256 MiB was accepted")
	}
}

func TestRowSchemaUsesWaitSamplesAndNullFailureMeasurements(t *testing.T) {
	row := trialRow{RecordType: "trial", SchemaVersion: rowSchemaVersion, TrialID: "b000-natural-00", Block: 0, Arm: "natural", Outcome: "worker_error", WaitSamples: []resourceSample{{AtMonoNS: 1}}}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"wait_samples":[{"at_mono_ns":1`) {
		t.Fatalf("wait sample series was not serialized: %s", text)
	}
	if !strings.Contains(text, `"after_run_faults":null`) || !strings.Contains(text, `"correctness":null`) {
		t.Fatalf("failure measurements were not retained as null: %s", text)
	}
	row.WaitSamples = nil
	encoded, err = json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	text = string(encoded)
	if !strings.Contains(text, `"wait_samples":null`) {
		t.Fatalf("missing wait series was not retained as null: %s", text)
	}
	if strings.Contains(text, "resource_last_live") || strings.Contains(text, "resource_before") {
		t.Fatalf("row contains misleading resource field: %s", text)
	}
}

func TestMetadataOmitsPerArmHashesAndConcurrencyClaims(t *testing.T) {
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"numpy"})
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := makeMetadata(options{blocks: 1, seed: 1, workloadMiB: 64, waitMs: 1000, cold: 10 * time.Millisecond, pageout: 20 * time.Millisecond, pressureThreshold: 0.8}, artifactBundle{profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	for _, field := range []string{"profile_binding_sha256_by_arm", "source_template_sha256", "concurrency"} {
		if strings.Contains(text, field) {
			t.Fatalf("metadata retained removed field %q: %s", field, text)
		}
	}
}

func TestWaitSamplesRemainIndependentDuringRecording(t *testing.T) {
	done := make(chan struct{})
	var samples waitObservations
	go func() {
		defer close(done)
		for i := uint64(0); i < 100; i++ {
			samples.add(resourceSample{AtMonoNS: i})
		}
	}()
	for i := 0; i < 100; i++ {
		copy := samples.snapshot()
		if len(copy) > 0 {
			copy[0].AtMonoNS = 999
		}
	}
	<-done
	if got := samples.snapshot(); len(got) != 100 || got[0].AtMonoNS != 0 {
		t.Fatalf("corrupted samples: %v", got)
	}
}
