package workspace

import (
	"bytes"
	"errors"
	"testing"
)

func TestCaptureFileReadsOnlyBoundedImmutableRootFiles(t *testing.T) {
	manager := newTestManager(t)
	body := []byte("captured private output")
	base, err := manager.Create([]InitialFile{{Path: "output.txt", Data: body}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CaptureFile(base, "output.txt", 1024); !errors.Is(err, ErrWorkspaceImmutable) {
		t.Fatalf("mutable capture err=%v", err)
	}
	info, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := manager.ForkBranch(base, info.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	root, err := branch.Seal(info.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	captured, err := manager.CaptureFile(root.Ref(), "output.txt", 1024)
	if err != nil || !bytes.Equal(captured, body) {
		t.Fatalf("captured=%q err=%v", captured, err)
	}
	captured[0] = 'X'
	again, err := manager.CaptureFile(root.Ref(), "output.txt", 1024)
	if err != nil || !bytes.Equal(again, body) {
		t.Fatalf("fresh capture=%q err=%v", again, err)
	}
	for _, test := range []struct {
		name string
		max  uint64
	}{
		{name: "../output.txt", max: 1024},
		{name: "output.txt", max: uint64(len(body) - 1)},
	} {
		if _, err := manager.CaptureFile(root.Ref(), test.name, test.max); err == nil {
			t.Fatalf("unsafe capture accepted: %+v", test)
		}
	}
}
