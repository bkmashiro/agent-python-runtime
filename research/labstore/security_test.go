package labstore_test

import (
	"bytes"
	"encoding/binary"
	"errors"

	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

func TestReadOnlyOpenPerformsNoWritesAndEnforcesBounds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	writable, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := writable.Put(labstore.KindFile, bytes.Repeat([]byte("x"), 32), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	before := directorySnapshot(t, root)

	readOnly, err := labstore.Open(root, labstore.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Get(ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readOnly.Put(labstore.KindFile, []byte("new"), privatePolicy()); !errors.Is(err, labstore.ErrReadOnly) {
		t.Fatalf("read-only put err=%v", err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
	after := directorySnapshot(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only open changed store\nbefore=%#v\nafter=%#v", before, after)
	}

	bounded, err := labstore.Open(root, labstore.Options{ReadOnly: true, MaxObjectBytes: 16})
	if err != nil {
		t.Fatal(err)
	}
	defer bounded.Close()
	if _, err := bounded.Get(ref); !errors.Is(err, labstore.ErrCorrupt) {
		t.Fatalf("oversized read err=%v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := labstore.Open(missing, labstore.Options{ReadOnly: true}); err == nil {
		t.Fatal("read-only open created a missing store")
	}
	if _, err := os.Lstat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing store was written: %v", err)
	}
}

func TestObjectReadRejectsTamperAndNonCanonicalHeaders(t *testing.T) {
	cases := map[string]func([]byte) []byte{
		"body digest mismatch": func(encoded []byte) []byte {
			encoded[len(encoded)-1] ^= 1
			return encoded
		},
		"trailing bytes": func(encoded []byte) []byte {
			return append(encoded, 'x')
		},
		"duplicate key": func(encoded []byte) []byte {
			return rewriteObjectHeader(t, encoded, func(header []byte) []byte {
				return bytes.Replace(header, []byte(`"kind":`), []byte(`"kind":"blob.file","kind":`), 1)
			})
		},
		"folded alias": func(encoded []byte) []byte {
			return rewriteObjectHeader(t, encoded, func(header []byte) []byte {
				return bytes.Replace(header, []byte(`"kind":`), []byte(`"Kind":"blob.file","kind":`), 1)
			})
		},
		"unknown field": func(encoded []byte) []byte {
			return rewriteObjectHeader(t, encoded, func(header []byte) []byte {
				return append(append([]byte(nil), header[:len(header)-1]...), []byte(`,"extra":true}`)...)
			})
		},
		"trailing header JSON": func(encoded []byte) []byte {
			return rewriteObjectHeader(t, encoded, func(header []byte) []byte { return append(header, []byte(`{}`)...) })
		},
		"invalid UTF-8": func(encoded []byte) []byte {
			return rewriteObjectHeader(t, encoded, func(header []byte) []byte {
				header[0] = 0xff
				return header
			})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "store")
			store, err := labstore.Open(root, labstore.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ref, _, err := store.Put(labstore.KindFile, []byte("protected body"), privatePolicy())
			if err != nil {
				t.Fatal(err)
			}
			path := onlyObjectPath(t, root)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tampered := mutate(append([]byte(nil), original...))
			if err := os.WriteFile(path, tampered, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(ref); !errors.Is(err, labstore.ErrCorrupt) {
				t.Fatalf("tamper accepted: %v", err)
			}
			if _, _, err := store.Put(labstore.KindFile, []byte("protected body"), privatePolicy()); !errors.Is(err, labstore.ErrCorrupt) {
				t.Fatalf("put replaced corrupt immutable object: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(after, tampered) {
				t.Fatalf("tampered object was overwritten err=%v", err)
			}
			_ = store.Close()
		})
	}
}

func TestStoreDeniesTraversalAndSymlinks(t *testing.T) {
	temporary := t.TempDir()
	if _, err := labstore.Open("relative", labstore.Options{}); err == nil {
		t.Fatal("relative root accepted")
	}
	nonCanonical := temporary + string(filepath.Separator) + "nested" + string(filepath.Separator) + ".." + string(filepath.Separator) + "store"
	if _, err := labstore.Open(nonCanonical, labstore.Options{}); err == nil {
		t.Fatal("non-canonical root accepted")
	}

	realRoot := filepath.Join(temporary, "real")
	if err := os.Mkdir(realRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(temporary, "root-link")
	if err := os.Symlink(realRoot, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := labstore.Open(rootLink, labstore.Options{}); err == nil {
		t.Fatal("symlink store root accepted")
	}
	realParent := filepath.Join(temporary, "real-parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	throughRealParent, err := labstore.Open(filepath.Join(realParent, "nested-store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = throughRealParent.Close()
	parentLink := filepath.Join(temporary, "parent-link")
	if err := os.Symlink(realParent, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := labstore.Open(filepath.Join(parentLink, "nested-store"), labstore.Options{ReadOnly: true}); err == nil {
		t.Fatal("store through symlink ancestor accepted")
	}

	permissiveRoot := filepath.Join(temporary, "permissive")
	permissive, err := labstore.Open(permissiveRoot, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = permissive.Close()
	if err := os.Chmod(permissiveRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := labstore.Open(permissiveRoot, labstore.Options{ReadOnly: true}); err == nil {
		t.Fatal("store root with group/world access accepted")
	}

	root := filepath.Join(temporary, "store")
	store, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ref, _, err := store.Put(labstore.KindFile, []byte("safe"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	objectPath := onlyObjectPath(t, root)
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(temporary, "external")
	if err := os.WriteFile(external, []byte("do not read"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, objectPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ref); !errors.Is(err, labstore.ErrCorrupt) {
		t.Fatalf("object symlink err=%v", err)
	}
	_ = store.Close()

	layoutRoot := filepath.Join(temporary, "layout")
	layout, err := labstore.Open(layoutRoot, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = layout.Close()
	if err := os.Rename(filepath.Join(layoutRoot, "objects"), filepath.Join(layoutRoot, "objects-real")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(layoutRoot, "objects-real"), filepath.Join(layoutRoot, "objects")); err != nil {
		t.Fatal(err)
	}
	if _, err := labstore.Open(layoutRoot, labstore.Options{ReadOnly: true}); err == nil {
		t.Fatal("symlink layout accepted")
	}
}

func TestConcurrentPublicationIsExclusive(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const workers = 24
	var created atomic.Int32
	var failures atomic.Int32
	refs := make(chan labstore.Ref, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			ref, wasCreated, err := store.Put(labstore.KindPrompt, []byte("one immutable body"), privatePolicy())
			if err != nil {
				failures.Add(1)
				return
			}
			if wasCreated {
				created.Add(1)
			}
			refs <- ref
		}()
	}
	group.Wait()
	close(refs)
	if failures.Load() != 0 || created.Load() != 1 {
		t.Fatalf("failures=%d created=%d", failures.Load(), created.Load())
	}
	var first labstore.Ref
	for ref := range refs {
		if first == (labstore.Ref{}) {
			first = ref
		} else if ref != first {
			t.Fatalf("different refs: %v and %v", first, ref)
		}
	}
}

func TestSecondWritableStoreIsRejected(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	first, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := labstore.Open(root, labstore.Options{}); !errors.Is(err, labstore.ErrBusy) || second != nil {
		t.Fatalf("second=%v err=%v", second, err)
	}
	reader, err := labstore.Open(root, labstore.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSequentialPrivacyClassificationAlwaysTightens(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	first, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	portable := portablePolicyWithoutLinks()
	ref, _, err := first.Put(labstore.KindPrompt, []byte("classification"), portable)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, _, err := second.Put(labstore.KindPrompt, []byte("classification"), privatePolicy())
	if err != nil || got != ref {
		t.Fatalf("ref=%v err=%v", got, err)
	}
	object, err := second.Get(ref)
	if err != nil || object.Privacy != labstore.PrivacyPrivate {
		t.Fatalf("privacy=%q err=%v", object.Privacy, err)
	}
}

func TestPrivacyNeverDowngradesAndPortableReadIsExplicit(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	portable := privatePolicy()
	portable.Privacy = labstore.PrivacyPortable
	ref, _, err := store.Put(labstore.KindPrompt, []byte("publishable"), portable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPortable(ref); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(labstore.KindPrompt, []byte("publishable"), privatePolicy()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPortable(ref); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("private object exported err=%v", err)
	}
	if _, _, err := store.Put(labstore.KindPrompt, []byte("publishable"), portable); err != nil {
		t.Fatal(err)
	}
	object, err := store.Get(ref)
	if err != nil || object.Privacy != labstore.PrivacyPrivate {
		t.Fatalf("privacy was downgraded: %#v err=%v", object, err)
	}
}

func TestMissingPrivateClassificationCannotBeRecreatedAsPortable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body := []byte("classification must fail closed")
	ref, _, err := store.Put(labstore.KindPrompt, body, privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.TrimPrefix(ref.SHA256, "sha256:")
	privacyPath := filepath.Join(root, "metadata", "privacy", string(ref.Kind), digest[:2], digest[2:]+".json")
	if err := os.Remove(privacyPath); err != nil {
		t.Fatal(err)
	}
	portable := privatePolicy()
	portable.Privacy = labstore.PrivacyPortable
	if _, _, err := store.Put(labstore.KindPrompt, body, portable); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("missing private classification upgraded err=%v", err)
	}
	if _, err := store.GetPortable(ref); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("missing classification exported err=%v", err)
	}
}

func TestOrphanPrivateClassificationPreventsPortablePublication(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body := []byte("orphan privacy classification")
	portable := privatePolicy()
	portable.Privacy = labstore.PrivacyPortable
	ref, _, err := store.Put(labstore.KindPrompt, body, portable)
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.TrimPrefix(ref.SHA256, "sha256:")
	objectPath := filepath.Join(root, "objects", string(ref.Kind), digest[:2], digest[2:]+".obj")
	privacyPath := filepath.Join(root, "metadata", "privacy", string(ref.Kind), digest[:2], digest[2:]+".json")
	if err := os.Remove(objectPath); err != nil {
		t.Fatal(err)
	}
	privateRecord := []byte(`{"schema_version":"pysolate.labstore-privacy.v1","privacy":"private"}`)
	if err := os.WriteFile(privacyPath, privateRecord, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(labstore.KindPrompt, body, portable); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPortable(ref); !errors.Is(err, labstore.ErrPrivate) {
		t.Fatalf("orphan private sidecar was downgraded err=%v", err)
	}
}

func TestStructuredObjectsAreCanonicalAndCredentialGuarded(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	policy := privatePolicy()
	ref, _, err := store.PutJSON(labstore.KindSemanticDocument, []byte(" {\n\"b\":2,\"a\":1\n} "), policy)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Get(ref)
	if err != nil || string(object.Body) != `{"a":1,"b":2}` {
		t.Fatalf("canonical body=%q err=%v", object.Body, err)
	}
	invalid := [][]byte{
		[]byte(`{"a":1,"a":2}`),
		append([]byte(`{"a":"`), 0xff, '"', '}'),
		[]byte(`{"a":1}{}`),
		[]byte(`{"authorization":"Bearer value"}`),
		[]byte(`{"nested":{"api_key":"value"}}`),
	}
	for _, raw := range invalid {
		if _, _, err := store.PutJSON(labstore.KindSemanticDocument, raw, policy); err == nil {
			t.Fatalf("invalid structured body accepted: %q", raw)
		}
	}
	if _, _, err := store.PutJSON(labstore.KindFile, []byte(`{}`), policy); err == nil {
		t.Fatal("PutJSON accepted an unstructured kind")
	}
}

func privatePolicy() labstore.PutOptions {
	return labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent}
}

type fileSnapshot struct {
	Mode    fs.FileMode
	Size    int64
	ModTime int64
}

func directorySnapshot(t *testing.T, root string) map[string]fileSnapshot {
	t.Helper()
	result := make(map[string]fileSnapshot)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		result[relative] = fileSnapshot{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func onlyObjectPath(t *testing.T, root string) string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(filepath.Join(root, "objects"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil || len(paths) != 1 {
		t.Fatalf("object paths=%v err=%v", paths, err)
	}
	return paths[0]
}

func rewriteObjectHeader(t *testing.T, encoded []byte, mutate func([]byte) []byte) []byte {
	t.Helper()
	magicEnd := bytes.IndexByte(encoded, '\n') + 1
	if magicEnd <= 0 || len(encoded) < magicEnd+4 {
		t.Fatal("invalid test object framing")
	}
	headerLength := int(binary.BigEndian.Uint32(encoded[magicEnd : magicEnd+4]))
	headerStart := magicEnd + 4
	headerEnd := headerStart + headerLength
	if headerEnd > len(encoded) {
		t.Fatal("invalid test object header length")
	}
	header := mutate(append([]byte(nil), encoded[headerStart:headerEnd]...))
	result := make([]byte, 0, len(encoded)-headerLength+len(header))
	result = append(result, encoded[:magicEnd]...)
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(header)))
	result = append(result, length[:]...)
	result = append(result, header...)
	result = append(result, encoded[headerEnd:]...)
	return result
}
