package labstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOfflineAuditAndRepairCrashLeftovers(t *testing.T) {
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
			audit, err := AuditOffline(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			wantStages, wantPrivacy := uint64(0), uint64(1)
			if point != "privacy_only" {
				wantStages = 1
			}
			if point == "linked_before_cleanup" {
				wantPrivacy = 0
			}
			if audit.OrphanStages != wantStages || audit.OrphanPrivacyRecords != wantPrivacy || audit.RemovedStages != 0 || audit.ReclaimedBytes != 0 {
				t.Fatalf("audit=%+v", audit)
			}
			repair, err := RepairOffline(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if repair.RemovedStages != wantStages || repair.ReclaimedBytes != repair.OrphanStageBytes {
				t.Fatalf("repair=%+v", repair)
			}
			clean, err := AuditOffline(root, Options{})
			if err != nil || clean.OrphanStages != 0 || clean.OrphanPrivacyRecords != wantPrivacy {
				t.Fatalf("clean=%+v err=%v", clean, err)
			}
			reopened, err := Open(root, Options{})
			if err != nil {
				t.Fatal(err)
			}
			ref, _, err := reopened.Put(KindPrompt, []byte("crash-boundary-body"), PutOptions{Privacy: PrivacyPortable, Credentials: CredentialsAbsent})
			if err != nil {
				t.Fatal(err)
			}
			object, err := reopened.Get(ref)
			if err != nil || object.Privacy != PrivacyPrivate {
				t.Fatalf("privacy=%q err=%v", object.Privacy, err)
			}
			if _, err := reopened.Stats(); err != nil {
				t.Fatal(err)
			}
			_ = reopened.Close()
		})
	}
}

func TestOfflineRecoveryRejectsActiveStoreHandles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	writer, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := AuditOffline(root, Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("audit err=%v", err)
	}
	if _, err := RepairOffline(root, Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("repair err=%v", err)
	}
	reader, err := Open(root, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := RepairOffline(root, Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("reader repair err=%v", err)
	}
}

func TestOfflineRepairValidatesAllCandidatesBeforeMutation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	store, err := Open(root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLabStoreCrashHelper$")
	command.Env = append(os.Environ(), crashHelperEnv+"=stage_fsynced", "PYSOLATE_LABSTORE_CRASH_ROOT="+root)
	err = command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != crashExitCode("stage_fsynced") {
		t.Fatalf("helper err=%v", err)
	}
	stages, err := crashStageFiles(root)
	if err != nil || len(stages) != 1 {
		t.Fatalf("stages=%v err=%v", stages, err)
	}
	malformed := filepath.Join(filepath.Dir(stages[0]), ".stage-malformed")
	if err := os.WriteFile(malformed, []byte("not an object"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairOffline(root, Options{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("repair err=%v", err)
	}
	if _, err := os.Stat(stages[0]); err != nil {
		t.Fatalf("valid candidate was removed before full validation: %v", err)
	}
	if _, err := os.Stat(malformed); err != nil {
		t.Fatalf("malformed candidate unexpectedly removed: %v", err)
	}
}
