package main

import (
	"os"
	"path/filepath"
	"testing"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func TestParseStrategyAcceptsFreshInstanceStrategies(t *testing.T) {
	for _, want := range []enginecontract.ExecutionStrategy{
		enginecontract.StrategyFreshInstance,
		enginecontract.StrategySingleUsePrepared,
		enginecontract.StrategyCOWReadySingleUse,
	} {
		got, err := parseStrategy(string(want))
		if err != nil || got != want {
			t.Fatalf("parse %q: got=%q err=%v", want, got, err)
		}
	}
	if _, err := parseStrategy(string(enginecontract.StrategyCOWFullRemapRestore)); err == nil {
		t.Fatal("restore strategy was accepted")
	}
}

func TestWriteReportUsesPrivateMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode=%o", info.Mode().Perm())
	}
}

func TestWriteReportRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias.json")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if err := writeReport(alias, []byte("new")); err == nil {
		t.Fatal("symlink output was accepted")
	}
	content, err := os.ReadFile(target)
	if err != nil || string(content) != "old" {
		t.Fatalf("target changed: %q err=%v", content, err)
	}
}
