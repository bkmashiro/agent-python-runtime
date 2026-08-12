package labstore_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

func TestStorePutGetDeduplicatesTypedImmutableBodies(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := labstore.Open(root, labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	policy := labstore.PutOptions{
		Privacy:     labstore.PrivacyPrivate,
		Credentials: labstore.CredentialsAbsent,
	}
	prompt, created, err := store.Put(labstore.KindPrompt, []byte("same bytes"), policy)
	if err != nil || !created {
		t.Fatalf("first put ref=%v created=%v err=%v", prompt, created, err)
	}
	repeated, created, err := store.Put(labstore.KindPrompt, []byte("same bytes"), policy)
	if err != nil || created || repeated != prompt {
		t.Fatalf("dedup ref=%v created=%v err=%v", repeated, created, err)
	}
	code, created, err := store.Put(labstore.KindCode, []byte("same bytes"), policy)
	if err != nil || !created || code == prompt || code.SHA256 == prompt.SHA256 {
		t.Fatalf("domain separation ref=%v created=%v err=%v", code, created, err)
	}

	object, err := store.Get(prompt)
	if err != nil {
		t.Fatal(err)
	}
	if object.Ref != prompt || object.Kind != labstore.KindPrompt || object.Privacy != labstore.PrivacyPrivate || len(object.Links) != 0 || !bytes.Equal(object.Body, []byte("same bytes")) {
		t.Fatalf("unexpected object: %#v", object)
	}
	object.Body[0] = 'X'
	again, err := store.Get(prompt)
	if err != nil || !bytes.Equal(again.Body, []byte("same bytes")) {
		t.Fatalf("stored body was mutable: body=%q err=%v", again.Body, err)
	}

	objectFiles := 0
	err = filepath.WalkDir(filepath.Join(root, "objects"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		objectFiles++
		info, infoErr := os.Lstat(path)
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("object path=%q mode=%v", path, info.Mode())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if objectFiles != 2 {
		t.Fatalf("object files=%d, want 2", objectFiles)
	}
}

func TestStoreRejectsMissingCredentialAttestation(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if _, _, err := store.Put(labstore.KindPrompt, []byte("body"), labstore.PutOptions{Privacy: labstore.PrivacyPrivate}); err == nil {
		t.Fatal("put without explicit credential-free attestation succeeded")
	}
	if _, _, err := store.Put(labstore.KindPrompt, []byte("secret"), labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsPresent}); err == nil {
		t.Fatal("credential-bearing body was accepted")
	}
}
