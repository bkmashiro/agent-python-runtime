package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvidenceBundleBindsEveryOutput(t *testing.T) {
	corpus := []byte("corpus\n")
	effectReport := []byte("effect\n")
	regionReport := []byte("region\n")
	bundle, err := encodeEvidenceBundle(corpus, effectReport, regionReport, digest([]byte("identity")), digest([]byte("artifact")), "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyEvidenceBundle(bundle, corpus, effectReport, regionReport); err != nil {
		t.Fatal(err)
	}
	mutated := append([]byte{}, regionReport...)
	mutated[0] = 'R'
	if err := verifyEvidenceBundle(bundle, corpus, effectReport, mutated); err == nil {
		t.Fatal("mixed evidence generation was accepted")
	}
}

func TestEvidenceBundleMarkerIsWrittenLastAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	bundlePath := filepath.Join(root, "bundle.json")
	oldMarker := []byte("old-generation\n")
	if err := os.WriteFile(bundlePath, oldMarker, 0o644); err != nil {
		t.Fatal(err)
	}
	blockingFile := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := writeEvidenceBundle(
		bundlePath,
		filepath.Join(root, "corpus.json"),
		filepath.Join(root, "effect.json"),
		filepath.Join(blockingFile, "region.json"),
		[]byte("corpus"), []byte("effect"), []byte("region"),
		digest([]byte("identity")), digest([]byte("artifact")), "0123456789abcdef0123456789abcdef01234567",
	)
	if err == nil {
		t.Fatal("expected staged output failure")
	}
	marker, readErr := os.ReadFile(bundlePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(marker) != string(oldMarker) {
		t.Fatalf("generation marker changed before all outputs landed: %q", marker)
	}
}
