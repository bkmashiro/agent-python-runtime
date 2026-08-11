package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

func readBindingFile(t *testing.T, binding *mountedWorkspaceBinding, name string) []byte {
	t.Helper()
	lease, err := binding.manager.Acquire(binding.ref, "binding-test-read")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	file, errno := lease.FS().OpenFile(name, experimentalsys.O_RDONLY, 0)
	if errno != 0 {
		t.Fatalf("open %s: %v", name, errno)
	}
	defer file.Close()
	var output bytes.Buffer
	buffer := make([]byte, 16)
	for {
		count, errno := file.Read(buffer)
		if errno != 0 {
			t.Fatalf("read %s: %v", name, errno)
		}
		if count == 0 {
			break
		}
		output.Write(buffer[:count])
	}
	return output.Bytes()
}

func TestMountedWorkspaceBindingProjectsAndMigratesCompleteState(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "input.bin"), []byte{0, 1, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	capsule := filepath.Join(t.TempDir(), "state.pwc")
	binding, err := prepareMountedWorkspace(&mountedWorkspaceConfig{SourceDirectory: source, OutputCapsule: capsule})
	if err != nil {
		t.Fatal(err)
	}
	if got := readBindingFile(t, binding, "input.bin"); !bytes.Equal(got, []byte{0, 1, 0xff}) {
		t.Fatalf("projected input=%v", got)
	}
	info, err := binding.export()
	if err != nil {
		t.Fatal(err)
	}
	if info.EntryCount != 2 || info.TotalBytes != 3 {
		t.Fatalf("capsule info=%+v", info)
	}
	if err := binding.close(); err != nil {
		t.Fatal(err)
	}
	stat, err := os.Stat(capsule)
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("capsule stat=%v err=%v", stat, err)
	}

	restored, err := prepareMountedWorkspace(&mountedWorkspaceConfig{InputCapsule: capsule})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	if got := readBindingFile(t, restored, "input.bin"); !bytes.Equal(got, []byte{0, 1, 0xff}) {
		t.Fatalf("restored input=%v", got)
	}
}

func TestMountedWorkspaceBindingFailsClosedWithoutPublishingPartialCapsule(t *testing.T) {
	root := t.TempDir()
	invalid := filepath.Join(root, "invalid.pwc")
	output := filepath.Join(root, "output.pwc")
	if err := os.WriteFile(invalid, []byte("not-a-capsule"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMountedWorkspace(&mountedWorkspaceConfig{InputCapsule: invalid, OutputCapsule: output}); err == nil {
		t.Fatal("invalid capsule was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed preparation published output: %v", err)
	}
}
