package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func TestCLIFailureCategoriesExposeOnlySafeKinds(t *testing.T) {
	cases := []struct {
		name string
		args []string
		err  error
		want durable.ReplayErrorCategory
	}{
		{"usage", nil, usageError("private flag value"), durable.ReplayErrorCategoryUsage},
		{"artifact", []string{"replay"}, &durable.ReplayMismatchError{Location: durable.ReplayMismatchLocationArtifact, Reason: durable.ReplayMismatchReasonArtifactIdentity}, durable.ReplayErrorCategoryArtifact},
		{"call", []string{"replay"}, &durable.ReplayMismatchError{Location: durable.ReplayMismatchLocationCall, Reason: durable.ReplayMismatchReasonCallArguments, Sequence: 4, HasSequence: true}, durable.ReplayErrorCategoryCall},
		{"result", []string{"replay"}, &durable.ReplayMismatchError{Location: durable.ReplayMismatchLocationResult, Reason: durable.ReplayMismatchReasonTerminalStdout}, durable.ReplayErrorCategoryResult},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := cliFailureCategory(test.args, test.err); got != test.want {
				t.Fatalf("category=%q want %q", got, test.want)
			}
		})
	}
}

func TestLocalExportAndOfflineReplayCLI(t *testing.T) {
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	wasm, err := os.ReadFile(guest)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "runs.db")
	bundlePath := filepath.Join(dir, "private.json")
	store, err := durable.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	calls := 0
	runner, err := durable.NewRunner(ctx, store, wasm, "cli-test-v1", []durable.Tool{{Name: "lookup", Version: "v1", Recovery: durable.RetrySafe, Call: func(context.Context, json.RawMessage) (any, error) {
		calls++
		return map[string]any{"sequence": calls, "private": "private-tool-value<&>"}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Create(ctx, durable.Definition{ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "cli-test-v1", ID: "cli-run", Code: "import random\na=lookup(key='same')\nb=lookup(key='same')\nprint('private-stdout')\nresult=[a,b,random.getrandbits(32)]", Seed: "cli-seed", Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Resume(ctx, "cli-run"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("dispatches=%d", calls)
	}
	if err = runner.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(db, 0400); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	export := []string{"export", "-db", db, "-run", "cli-run", "-out", bundlePath}
	if err = run(export, &output, &output); err != nil {
		t.Fatal(err)
	}
	if err = run(export, &output, &output); err == nil {
		t.Fatal("overwrote existing bundle")
	}
	info, _ := os.Stat(bundlePath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("bundle permissions %v", info.Mode())
	}
	dbInfo, _ := os.Stat(db)
	if dbInfo.Mode().Perm() != 0400 {
		t.Fatal("export changed database permissions")
	}
	after, _ := os.ReadFile(db)
	if !bytes.Equal(before, after) {
		t.Fatal("export modified database")
	}
	if err = os.Remove(db); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"replay", "-bundle", bundlePath, "-guest", guest}, &output, &output); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("offline replay dispatched a real tool")
	}
	for _, secret := range []string{"private-tool-value", "private-stdout"} {
		if bytes.Contains(output.Bytes(), []byte(secret)) {
			t.Fatal("CLI leaked payload")
		}
	}
	bad := filepath.Join(dir, "bad.wasm")
	if err = os.WriteFile(bad, []byte("wrong artifact"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"replay", "-bundle", bundlePath, "-guest", bad}, &output, &output); err == nil {
		t.Fatal("accepted wrong artifact")
	}
}
