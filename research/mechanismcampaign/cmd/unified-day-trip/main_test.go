package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVerifyCleanCommitRejectsDirtyOrMismatchedCheckout(t *testing.T) {
	repository := t.TempDir()
	runGit(t, repository, "init")
	runGit(t, repository, "config", "user.email", "campaign@example.invalid")
	runGit(t, repository, "config", "user.name", "Campaign Test")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "add", "tracked.txt")
	runGit(t, repository, "commit", "-m", "fixture")
	commit := runGit(t, repository, "rev-parse", "HEAD")
	if verified, err := verifyCleanCommit(repository, commit); err != nil || verified != commit {
		t.Fatalf("verified=%q err=%v", verified, err)
	}
	if _, err := verifyCleanCommit(repository, "0000000000000000000000000000000000000000"); err == nil {
		t.Fatal("mismatched commit accepted")
	}
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyCleanCommit(repository, commit); err == nil {
		t.Fatal("dirty checkout accepted")
	}
}

func runGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", arguments, err)
	}
	for len(output) > 0 && (output[len(output)-1] == '\n' || output[len(output)-1] == '\r') {
		output = output[:len(output)-1]
	}
	return string(output)
}
