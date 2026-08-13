package workspace

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

func TestPortableRootMovesThroughCapsuleWithoutLocalIdentity(t *testing.T) {
	manager := newTestManager(t)
	base, err := manager.Create(nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, _ := manager.Inspect(base)
	branch, _ := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	writeBranchFile(t, manager, branch.Ref(), "moved.txt", "portable")
	root, err := branch.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var capsule bytes.Buffer
	if _, err := manager.ExportCapsule(root.Ref(), &capsule); err != nil {
		t.Fatal(err)
	}
	other := newTestManager(t)
	imported, _, err := other.ImportCapsule(&capsule, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	bound, err := other.BindImportedRoot(imported, root)
	if err != nil {
		t.Fatal(err)
	}
	if bound.Ref() == root.Ref() || bound.IdentitySHA256 != root.IdentitySHA256 {
		t.Fatalf("bound root=%+v", bound)
	}
	if _, err := other.Acquire(bound.Ref(), "immutable"); !errors.Is(err, ErrWorkspaceImmutable) {
		t.Fatalf("imported root mutable: %v", err)
	}
}

func TestSelectPortableRootRequiresExpectedParent(t *testing.T) {
	manager := newTestManager(t)
	base, err := manager.Create(nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, _ := manager.Inspect(base)
	first, _ := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	writeBranchFile(t, manager, first.Ref(), "first.txt", "one")
	firstRoot, err := first.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	writeBranchFile(t, manager, second.Ref(), "second.txt", "two")
	secondRoot, err := second.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := manager.SelectRoot(baseInfo.WorkspaceSHA256, []Root{firstRoot, secondRoot}, secondRoot.IdentitySHA256)
	if err != nil || selected.IdentitySHA256 != secondRoot.IdentitySHA256 {
		t.Fatalf("SelectRoot() root=%+v err=%v", selected, err)
	}
	if _, err := manager.SelectRoot(digestForTest('0'), []Root{firstRoot}, firstRoot.IdentitySHA256); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("SelectRoot(conflict) error=%v", err)
	}
}

func TestPortableBranchSealsImmutableRootAndRecurses(t *testing.T) {
	manager := newTestManager(t)
	base, err := manager.Create([]InitialFile{{Path: "base.txt", Data: []byte("base")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, err := manager.Inspect(base)
	if err != nil {
		t.Fatal(err)
	}
	branch, err := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	writeBranchFile(t, manager, branch.Ref(), "child.txt", "child")
	root, err := branch.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if root.SchemaVersion != RootSchemaVersion || root.IdentitySHA256 == "" || root.WorkspaceSHA256 == baseInfo.WorkspaceSHA256 || root.ParentIdentitySHA256 == "" || root.Depth != 1 || root.ChangedEntries != 1 || root.ChangedBytes != 5 {
		t.Fatalf("root = %+v", root)
	}
	if _, err := manager.Acquire(root.Ref(), "mutate-sealed"); !errors.Is(err, ErrWorkspaceImmutable) {
		t.Fatalf("Acquire(sealed) error = %v", err)
	}
	grandchild, err := manager.ForkBranch(root.Ref(), root.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	writeBranchFile(t, manager, grandchild.Ref(), "grandchild.txt", "next")
	grandRoot, err := grandchild.Seal(root.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if grandRoot.ParentIdentitySHA256 != root.IdentitySHA256 || grandRoot.Depth != 2 {
		t.Fatalf("grandchild root = %+v", grandRoot)
	}
}

func TestPortableBranchExpectedBaseConflictLeavesChildUnpublished(t *testing.T) {
	manager := newTestManager(t)
	base, err := manager.Create([]InitialFile{{Path: "base.txt", Data: []byte("base")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, _ := manager.Inspect(base)
	branch, err := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	writeBranchFile(t, manager, branch.Ref(), "child.txt", "child")
	writeBranchFile(t, manager, base, "changed.txt", "changed")
	if _, err := branch.Seal(baseInfo.WorkspaceSHA256); !errors.Is(err, ErrWorkspaceConflict) {
		t.Fatalf("Seal() error = %v", err)
	}
	lease, err := manager.Acquire(branch.Ref(), "still-private")
	if err != nil {
		t.Fatalf("conflicted child became inaccessible: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestPortableRootRecordHasNoHostPathOrLocalRefInIdentityDocument(t *testing.T) {
	manager := newTestManager(t)
	base, err := manager.Create(nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, _ := manager.Inspect(base)
	branch, err := manager.ForkBranch(base, baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	root, err := branch.Seal(baseInfo.WorkspaceSHA256)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := root.IdentityDocument()
	if err != nil {
		t.Fatal(err)
	}
	if containsAny(string(identity), manager.base, string(root.Ref()), "/tmp", "host_path") {
		t.Fatalf("identity leaked local state: %s", identity)
	}
}

func writeBranchFile(t *testing.T, manager *Manager, ref Ref, name, content string) {
	t.Helper()
	lease, err := manager.Acquire(ref, "writer-"+name)
	if err != nil {
		t.Fatal(err)
	}
	file, errno := lease.FS().OpenFile(name, experimentalsys.O_WRONLY|experimentalsys.O_CREAT|experimentalsys.O_EXCL, 0o600)
	if errno != 0 {
		t.Fatal(errno)
	}
	if _, errno := file.Write([]byte(content)); errno != 0 {
		t.Fatal(errno)
	}
	_ = file.Close()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func mustLease(t *testing.T, manager *Manager, ref Ref, owner string) *Lease {
	t.Helper()
	lease, err := manager.Acquire(ref, owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return lease
}

func digestForTest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }

func containsAny(value string, values ...string) bool {
	for _, candidate := range values {
		if candidate != "" && strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
