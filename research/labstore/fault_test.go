package labstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const crashHelperEnv = "PYSOLATE_LABSTORE_CRASH_POINT"

func TestLabStoreCrashBoundariesAndReopen(t *testing.T) {
	for _, point := range []string{"privacy_only", "stage_fsynced", "linked_before_cleanup"} {
		t.Run(point, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "store")
			store, err := Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(os.Args[0], "-test.run=^TestLabStoreCrashHelper$")
			command.Env = append(os.Environ(), crashHelperEnv+"="+point, "PYSOLATE_LABSTORE_CRASH_ROOT="+root)
			err = command.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != crashExitCode(point) {
				t.Fatalf("helper err=%v", err)
			}

			reopened, err := Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			body := []byte("crash-boundary-body")
			ref, err := contentRef(KindPrompt, nil, body)
			if err != nil {
				t.Fatal(err)
			}
			_, getErr := reopened.Get(ref)
			if point == "linked_before_cleanup" {
				if getErr != nil {
					t.Fatalf("published object unavailable: %v", getErr)
				}
			} else if !errors.Is(getErr, ErrNotFound) {
				t.Fatalf("pre-publication object visible: %v", getErr)
			}
			portable := PutOptions{Privacy: PrivacyPortable, Credentials: CredentialsAbsent}
			got, _, err := reopened.Put(KindPrompt, body, portable)
			if err != nil || got != ref {
				t.Fatalf("recovery put ref=%v err=%v", got, err)
			}
			if _, err := reopened.GetPortable(ref); !errors.Is(err, ErrPrivate) {
				t.Fatalf("crash classification exported: %v", err)
			}
			stages, err := crashStageFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			wantStages := 0
			if point != "privacy_only" {
				wantStages = 1
			}
			if len(stages) != wantStages {
				t.Fatalf("orphan stages=%v want=%d", stages, wantStages)
			}
			stats, err := reopened.Stats()
			if point == "privacy_only" {
				if err != nil || stats.ObjectCount != 1 || stats.PrivateObjects != 1 {
					t.Fatalf("stats=%+v err=%v", stats, err)
				}
			} else if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("orphan stage was not surfaced fail-closed: stats=%+v err=%v", stats, err)
			}
		})
	}
}

func TestLabStoreCrashHelper(t *testing.T) {
	point := os.Getenv(crashHelperEnv)
	if point == "" {
		t.Skip("crash helper")
	}
	root := os.Getenv("PYSOLATE_LABSTORE_CRASH_ROOT")
	store, err := Open(root, Options{})
	if err != nil {
		panic(err)
	}
	body := []byte("crash-boundary-body")
	ref, err := contentRef(KindPrompt, nil, body)
	if err != nil {
		panic(err)
	}
	encoded, err := encodeObject(ref, nil, body)
	if err != nil {
		panic(err)
	}
	store.mu.Lock()
	if err := store.ensureObjectParentLocked(ref, false); err != nil {
		panic(err)
	}
	if err := store.writePrivacyLocked(ref, PrivacyPrivate); err != nil {
		panic(err)
	}
	if point == "privacy_only" {
		os.Exit(crashExitCode(point))
	}
	destination := objectPath(ref)
	directory := filepath.ToSlash(filepath.Dir(destination))
	stage, file, err := store.createStageLocked(directory)
	if err != nil {
		panic(err)
	}
	if _, err := file.Write(encoded); err != nil {
		panic(err)
	}
	if err := file.Sync(); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	if point == "stage_fsynced" {
		os.Exit(crashExitCode(point))
	}
	if point == "stage_held" {
		ready, release := os.Getenv("PYSOLATE_LABSTORE_READY"), os.Getenv("PYSOLATE_LABSTORE_RELEASE")
		if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
			panic(err)
		}
		for attempt := 0; attempt < 500; attempt++ {
			if _, err := os.Stat(release); err == nil {
				os.Exit(crashExitCode(point))
			}
			time.Sleep(10 * time.Millisecond)
		}
		panic("stage hold timed out")
	}
	if err := store.root.Link(stage, destination); err != nil {
		panic(err)
	}
	if err := store.syncDirectoryLocked(directory); err != nil {
		panic(err)
	}
	os.Exit(crashExitCode(point))
}

func crashExitCode(point string) int {
	switch point {
	case "privacy_only":
		return 91
	case "stage_fsynced":
		return 92
	case "linked_before_cleanup":
		return 93
	case "stage_held":
		return 94
	default:
		return 99
	}
}

func crashStageFiles(root string) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), ".stage-") {
			result = append(result, path)
		}
		return nil
	})
	return result, err
}

func TestCrossProcessActiveStageMakesAggregateReadsFailClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	control := t.TempDir()
	ready, release := filepath.Join(control, "ready"), filepath.Join(control, "release")
	command := exec.Command(os.Args[0], "-test.run=^TestLabStoreCrashHelper$")
	command.Env = append(os.Environ(), crashHelperEnv+"=stage_held", "PYSOLATE_LABSTORE_CRASH_ROOT="+root, "PYSOLATE_LABSTORE_READY="+ready, "PYSOLATE_LABSTORE_RELEASE="+release)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 500; attempt++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if attempt == 499 {
			_ = command.Process.Kill()
			t.Fatal("writer did not expose held stage")
		}
		time.Sleep(10 * time.Millisecond)
	}
	reader, err := Open(root, Options{ReadOnly: true})
	if err != nil {
		_ = command.Process.Kill()
		t.Fatal(err)
	}
	if _, err := reader.Stats(); !errors.Is(err, ErrCorrupt) {
		_ = reader.Close()
		_ = command.Process.Kill()
		t.Fatalf("active stage was not visible fail-closed: %v", err)
	}
	_ = reader.Close()
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		_ = command.Process.Kill()
		t.Fatal(err)
	}
	err = command.Wait()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != crashExitCode("stage_held") {
		t.Fatalf("helper err=%v", err)
	}
}

func TestCrossProcessWritersConvergeToOnePrivateObject(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	const writers = 8
	commands := make([]*exec.Cmd, writers)
	outputs := make([]bytes.Buffer, writers)
	for i := 0; i < writers; i++ {
		privacy := "portable"
		if i%2 == 0 {
			privacy = "private"
		}
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestLabStoreWriterHelper$")
		commands[i].Env = append(os.Environ(), "PYSOLATE_LABSTORE_WRITER_ROOT="+root, "PYSOLATE_LABSTORE_WRITER_PRIVACY="+privacy)
		commands[i].Stdout, commands[i].Stderr = &outputs[i], &outputs[i]
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for i, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatalf("writer %d: %v output=%s", i, err, outputs[i].String())
		}
	}
	reopened, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	body := []byte("cross-process-body")
	ref, err := contentRef(KindPrompt, nil, body)
	if err != nil {
		t.Fatal(err)
	}
	object, err := reopened.Get(ref)
	if err != nil || object.Privacy != PrivacyPrivate {
		t.Fatalf("object=%+v err=%v", object, err)
	}
	if _, err := reopened.GetPortable(ref); !errors.Is(err, ErrPrivate) {
		t.Fatalf("private object exported: %v", err)
	}
	stats, err := reopened.Stats()
	if err != nil || stats.ObjectCount != 1 || stats.PrivateObjects != 1 {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	stages, err := crashStageFiles(root)
	if err != nil || len(stages) != 0 {
		t.Fatalf("stages=%v err=%v", stages, err)
	}
}

func TestLabStoreWriterHelper(t *testing.T) {
	root := os.Getenv("PYSOLATE_LABSTORE_WRITER_ROOT")
	if root == "" {
		t.Skip("writer helper")
	}
	privacy := Privacy(os.Getenv("PYSOLATE_LABSTORE_WRITER_PRIVACY"))
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ref, created, err := store.Put(KindPrompt, []byte("cross-process-body"), PutOptions{Privacy: privacy, Credentials: CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s created=%t\n", ref, created)
}
