package main

import (
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/placement"
)

func TestRejectedScriptedCellIsACompletePassingTrial(t *testing.T) {
	corpus, err := placement.Load(filepath.Join("..", "..", "eval", "agentic", "placement", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task placement.Task
	for _, candidate := range corpus.Tasks {
		if candidate.ID == "pl-ba-native-process" {
			task = candidate
			break
		}
	}
	trial := rejectedTrial(task, "pysolate", "sha256:"+strings.Repeat("a", 64), strings.Repeat("b", 40), "sha256:"+strings.Repeat("c", 64))
	if err := placement.ValidateTrialResult(trial); err != nil {
		t.Fatal(err)
	}
	score, err := placement.Score(task, trial)
	if err != nil || !score.Pass || !score.ExpectedRejection {
		t.Fatalf("score=%+v err=%v", score, err)
	}
}

func TestScriptedCellFilenameIsContained(t *testing.T) {
	if got := trialFilename("pl-hp-dedupe-contacts", "computer"); got != "pl-hp-dedupe-contacts--computer--scripted--r1.json" {
		t.Fatalf("filename=%q", got)
	}
	for _, id := range []string{"../escape", "/absolute", "bad space"} {
		if trialFilename(id, "direct") != "" {
			t.Fatalf("unsafe id %q admitted", id)
		}
	}
}

func TestBuildSourceRevisionRejectsDirtyOrMissingIdentity(t *testing.T) {
	clean := &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "false"}}}
	if got, err := sourceRevision(clean); err != nil || got != strings.Repeat("a", 40) {
		t.Fatalf("revision=%q err=%v", got, err)
	}
	for _, info := range []*debug.BuildInfo{
		{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: strings.Repeat("a", 40)}, {Key: "vcs.modified", Value: "true"}}},
		{Settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}}},
	} {
		if _, err := sourceRevision(info); err == nil {
			t.Fatal("invalid build identity admitted")
		}
	}
}
