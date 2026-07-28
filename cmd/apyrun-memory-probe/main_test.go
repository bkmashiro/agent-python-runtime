package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteHeapProfileIsPrivateAndNonempty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "heap.pprof")
	if err := writeHeapProfile(path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("heap profile size=%d mode=%o", info.Size(), info.Mode().Perm())
	}
}

func TestParseSlots(t *testing.T) {
	slots, err := parseSlots("0,1,4,64")
	if err != nil {
		t.Fatal(err)
	}
	want := []uint32{0, 1, 4, 64}
	for index := range want {
		if slots[index] != want[index] {
			t.Fatalf("slots=%v", slots)
		}
	}
	for _, invalid := range []string{"", "1,1", "4097", "-1", "1, 4"} {
		if _, err := parseSlots(invalid); err == nil {
			t.Fatalf("accepted invalid slots %q", invalid)
		}
	}
}

func TestSelectedObserverPhase(t *testing.T) {
	for _, phase := range []string{"instantiate_host", "compile", "cow_image_instantiate_guest", "cow_image__initialize", "cow_image_runtime_init", "cow_image_seal"} {
		if !selectedObserverPhase(phase) {
			t.Fatalf("phase %q is not selected", phase)
		}
	}
	for _, phase := range []string{"pool_prepare_instantiate_guest", "execute", "prepare"} {
		if selectedObserverPhase(phase) {
			t.Fatalf("phase %q unexpectedly selected", phase)
		}
	}
}
