package labstore_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

func TestWorkspaceTreeIsCanonicalAndDeduplicatesFileBodies(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{MaxTreeEntries: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	shared, _, err := store.Put(labstore.KindFile, []byte("shared"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := store.Put(labstore.KindFile, []byte("other"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	entries := []labstore.WorkspaceEntry{
		{Path: "z/report.txt", Executable: false, Content: other},
		{Path: "a/copy.txt", Executable: true, Content: shared},
		{Path: "a/original.txt", Executable: false, Content: shared},
	}
	treeRef, created, err := store.PutWorkspaceTree(entries, privatePolicy())
	if err != nil || !created || treeRef.Kind != labstore.KindWorkspaceTree {
		t.Fatalf("tree ref=%v created=%v err=%v", treeRef, created, err)
	}
	tree, err := store.GetWorkspaceTree(treeRef)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"a/copy.txt", "a/original.txt", "z/report.txt"}
	gotPaths := make([]string, len(tree.Entries))
	for index, entry := range tree.Entries {
		gotPaths[index] = entry.Path
		if entry.Size == 0 {
			t.Fatalf("entry size was not derived: %#v", entry)
		}
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("paths=%v want=%v", gotPaths, wantPaths)
	}
	object, err := store.Get(treeRef)
	if err != nil || len(object.Links) != 2 {
		t.Fatalf("tree links=%v err=%v", object.Links, err)
	}

	reversed := []labstore.WorkspaceEntry{entries[2], entries[1], entries[0]}
	repeated, created, err := store.PutWorkspaceTree(reversed, privatePolicy())
	if err != nil || created || repeated != treeRef {
		t.Fatalf("canonical repeat=%v created=%v err=%v", repeated, created, err)
	}

	invalid := [][]labstore.WorkspaceEntry{
		{{Path: "../escape", Content: shared}},
		{{Path: "/absolute", Content: shared}},
		{{Path: "a/./b", Content: shared}},
		{{Path: "same", Content: shared}, {Path: "same", Content: other}},
		{{Path: "not-file", Content: treeRef}},
		{{Path: "wrong-size", Content: shared, Size: 99}},
	}
	for _, value := range invalid {
		if _, _, err := store.PutWorkspaceTree(value, privatePolicy()); err == nil {
			t.Fatalf("invalid tree accepted: %#v", value)
		}
	}
	tooMany := append(entries, labstore.WorkspaceEntry{Path: "four", Content: shared}, labstore.WorkspaceEntry{Path: "five", Content: shared})
	if _, _, err := store.PutWorkspaceTree(tooMany, privatePolicy()); err == nil {
		t.Fatal("oversized tree accepted")
	}
}

func TestBranchRelationsRetainReachableParentsAndSweepOnlyGarbage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	prompt, _, err := store.Put(labstore.KindPrompt, []byte("shared prompt"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	workspaceFile, _, err := store.Put(labstore.KindFile, []byte("initial state"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	workspaceTree, _, err := store.PutWorkspaceTree([]labstore.WorkspaceEntry{{Path: "state.txt", Content: workspaceFile}}, privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, err := store.PutJSON(labstore.KindToolPayload, []byte(`{"operations":[0,1]}`), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := store.PutJSON(labstore.KindSemanticDocument, []byte(`{"mode":"override"}`), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	parentRun := putStructured(t, store, labstore.KindRun, `{"run":"parent"}`, prompt, workspaceTree)
	childExecution := putStructured(t, store, labstore.KindExecution, `{"execution":"child-a"}`, prompt)
	branchRef, created, err := store.PutBranch(labstore.Branch{
		ParentRun:        parentRun,
		ChildExecution:   childExecution,
		ForkOperation:    2,
		Prefix:           prefix,
		InitialWorkspace: workspaceTree,
		Manifest:         manifest,
	}, privatePolicy())
	if err != nil || !created {
		t.Fatalf("branch=%v created=%v err=%v", branchRef, created, err)
	}
	branch, err := store.GetBranch(branchRef)
	if err != nil || branch.ParentRun != parentRun || branch.ChildExecution != childExecution || branch.ForkOperation != 2 {
		t.Fatalf("decoded branch=%#v err=%v", branch, err)
	}
	unrelated, _, err := store.Put(labstore.KindProviderBody, []byte("garbage"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Pin("study.branch-a", branchRef); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.Resolve("study.branch-a")
	if err != nil || resolved != branchRef {
		t.Fatalf("resolved=%v err=%v", resolved, err)
	}
	plan, err := store.PlanRetention()
	if err != nil {
		t.Fatal(err)
	}
	if !containsRef(plan.Reachable, parentRun) || !containsRef(plan.Reachable, prompt) || !containsRef(plan.Reachable, workspaceFile) || !containsRef(plan.Unreachable, unrelated) {
		t.Fatalf("unexpected retention plan: %#v", plan)
	}
	if countFor(plan.ReferenceCounts, branchRef) != 1 || countFor(plan.ReferenceCounts, prompt) != 2 {
		t.Fatalf("reference counts=%#v", plan.ReferenceCounts)
	}

	report, err := store.Sweep()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Deleted, []labstore.Ref{unrelated}) {
		t.Fatalf("deleted=%v want=%v", report.Deleted, unrelated)
	}
	for _, retained := range []labstore.Ref{branchRef, parentRun, childExecution, prompt, workspaceTree, workspaceFile, prefix, manifest} {
		if _, err := store.Get(retained); err != nil {
			t.Fatalf("reachable object %v deleted: %v", retained, err)
		}
	}
	if _, err := store.Get(unrelated); !errors.Is(err, labstore.ErrNotFound) {
		t.Fatalf("garbage survived sweep: %v", err)
	}

	if err := store.Unpin("study.branch-a"); err != nil {
		t.Fatal(err)
	}
	second, err := store.Sweep()
	if err != nil || len(second.Deleted) != 8 {
		t.Fatalf("second sweep=%#v err=%v", second, err)
	}
	if _, err := store.Get(parentRun); !errors.Is(err, labstore.ErrNotFound) {
		t.Fatalf("unrooted parent survived: %v", err)
	}
}

func TestPinsAreBoundedStrictAndReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(root, labstore.Options{MaxRoots: 1})
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := store.Put(labstore.KindPrompt, []byte("root"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", "UPPER", "", ".hidden", "a/b"} {
		if err := store.Pin(name, ref); err == nil {
			t.Fatalf("invalid pin name accepted: %q", name)
		}
	}
	if err := store.Pin("one", ref); err != nil {
		t.Fatal(err)
	}
	if err := store.Pin("two", ref); err == nil {
		t.Fatal("root bound was not enforced")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := labstore.Open(root, labstore.Options{ReadOnly: true, MaxRoots: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if err := readOnly.Pin("one", ref); !errors.Is(err, labstore.ErrReadOnly) {
		t.Fatalf("read-only pin err=%v", err)
	}
	if err := readOnly.Unpin("one"); !errors.Is(err, labstore.ErrReadOnly) {
		t.Fatalf("read-only unpin err=%v", err)
	}
	if _, err := readOnly.Sweep(); !errors.Is(err, labstore.ErrReadOnly) {
		t.Fatalf("read-only sweep err=%v", err)
	}

	pinPath := filepath.Join(root, "roots", "one.json")
	if err := os.Remove(pinPath); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(filepath.Dir(root), "external-pin")
	if err := os.WriteFile(external, []byte(`{"schema_version":"pysolate.labstore-root.v1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, pinPath); err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Resolve("one"); !errors.Is(err, labstore.ErrCorrupt) {
		t.Fatalf("pin symlink err=%v", err)
	}
}

func TestPortableGraphRejectsPrivateDescendants(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	private, _, err := store.Put(labstore.KindFile, []byte("private"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	portablePolicy := privatePolicy()
	portablePolicy.Privacy = labstore.PrivacyPortable
	portablePolicy.Links = []labstore.Ref{private}
	if _, _, err := store.PutJSON(labstore.KindSemanticDocument, []byte(`{"label":"claims portable"}`), portablePolicy); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("portable parent linked private child err=%v", err)
	}

	child, _, err := store.Put(labstore.KindFile, []byte("initially portable"), portablePolicyWithoutLinks())
	if err != nil {
		t.Fatal(err)
	}
	portablePolicy.Links = []labstore.Ref{child}
	parent, _, err := store.PutJSON(labstore.KindSemanticDocument, []byte(`{"label":"portable"}`), portablePolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPortable(parent); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(labstore.KindFile, []byte("initially portable"), privatePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPortable(parent); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("portable ancestor ignored tightened child err=%v", err)
	}
}

func putStructured(t *testing.T, store *labstore.Store, kind labstore.Kind, body string, links ...labstore.Ref) labstore.Ref {
	t.Helper()
	policy := privatePolicy()
	policy.Links = links
	ref, _, err := store.PutJSON(kind, []byte(body), policy)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

func portablePolicyWithoutLinks() labstore.PutOptions {
	return labstore.PutOptions{Privacy: labstore.PrivacyPortable, Credentials: labstore.CredentialsAbsent}
}

func containsRef(values []labstore.Ref, target labstore.Ref) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func countFor(values []labstore.ReferenceCount, target labstore.Ref) uint64 {
	for _, value := range values {
		if value.Ref == target {
			return value.Count
		}
	}
	return 0
}
