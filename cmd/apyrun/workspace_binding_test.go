package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

const testRequestSHA256 = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

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

func TestMountedWorkspaceBindingStagesPublishesAndRestoresCompleteState(t *testing.T) {
	source := t.TempDir()
	if err := os.Mkdir(filepath.Join(source, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "input.bin"), []byte{0, 1, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	capsule := filepath.Join(t.TempDir(), "state.pwc")
	binding, err := prepareMountedWorkspace(&mountedWorkspaceConfig{
		SourceDirectory: source, OutputCapsule: capsule, Disposition: runtimeconfig.WorkspaceExportOnSuccess,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readBindingFile(t, binding, "input.bin"); !bytes.Equal(got, []byte{0, 1, 0xff}) {
		t.Fatalf("projected input=%v", got)
	}
	receipt, staged, err := binding.prepareDisposition(runtimeconfig.RunResponseOK, testRequestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.discard()
	if receipt.Disposition != runtimeconfig.WorkspaceExported || receipt.CapsuleSHA256 == nil || staged == nil {
		t.Fatalf("receipt=%+v staged=%+v", receipt, staged)
	}
	if _, err := os.Stat(capsule); !os.IsNotExist(err) {
		t.Fatalf("staging published capsule early: %v", err)
	}
	if err := staged.publish(); err != nil {
		t.Fatal(err)
	}
	if err := binding.close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capsule)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if got, want := *receipt.CapsuleSHA256, "sha256:"+hex.EncodeToString(digest[:]); got != want {
		t.Fatalf("capsule digest=%s want=%s", got, want)
	}
	stat, err := os.Stat(capsule)
	if err != nil || stat.Mode().Perm() != 0o600 {
		t.Fatalf("capsule stat=%v err=%v", stat, err)
	}

	restored, err := prepareMountedWorkspace(&mountedWorkspaceConfig{InputCapsule: capsule, Disposition: runtimeconfig.WorkspaceDiscardPolicy})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.close()
	if got := readBindingFile(t, restored, "input.bin"); !bytes.Equal(got, []byte{0, 1, 0xff}) {
		t.Fatalf("restored input=%v", got)
	}
}

func TestMountedWorkspaceBindingAppliesExplicitDispositionPolicy(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name       string
		policy     runtimeconfig.WorkspaceDispositionPolicy
		status     runtimeconfig.RunResponseStatus
		wantExport bool
	}{
		{name: "success selected", policy: runtimeconfig.WorkspaceExportOnSuccess, status: runtimeconfig.RunResponseOK, wantExport: true},
		{name: "error discarded", policy: runtimeconfig.WorkspaceExportOnSuccess, status: runtimeconfig.RunResponseError},
		{name: "error retained", policy: runtimeconfig.WorkspaceExportOnResponse, status: runtimeconfig.RunResponseError, wantExport: true},
		{name: "host discard", policy: runtimeconfig.WorkspaceDiscardPolicy, status: runtimeconfig.RunResponseOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := &mountedWorkspaceConfig{Disposition: test.policy}
			if test.policy != runtimeconfig.WorkspaceDiscardPolicy {
				config.OutputCapsule = filepath.Join(root, test.name+".pwc")
			}
			binding, err := prepareMountedWorkspace(config)
			if err != nil {
				t.Fatal(err)
			}
			defer binding.close()
			receipt, staged, err := binding.prepareDisposition(test.status, testRequestSHA256)
			if err != nil {
				t.Fatal(err)
			}
			if staged != nil {
				defer staged.discard()
			}
			if got := receipt.Disposition == runtimeconfig.WorkspaceExported; got != test.wantExport {
				t.Fatalf("receipt=%+v", receipt)
			}
			if (staged != nil) != test.wantExport {
				t.Fatalf("staged=%+v wantExport=%v", staged, test.wantExport)
			}
		})
	}
}

func TestMountedWorkspaceBindingFailsClosedWithoutPublishingPartialCapsule(t *testing.T) {
	root := t.TempDir()
	invalid := filepath.Join(root, "invalid.pwc")
	output := filepath.Join(root, "output.pwc")
	if err := os.WriteFile(invalid, []byte("not-a-capsule"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareMountedWorkspace(&mountedWorkspaceConfig{
		InputCapsule: invalid, OutputCapsule: output, Disposition: runtimeconfig.WorkspaceExportOnResponse,
	}); err == nil {
		t.Fatal("invalid capsule was accepted")
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed preparation published output: %v", err)
	}
}

func TestMountedWorkspaceBindingClosePreservesActiveLeaseForRetry(t *testing.T) {
	binding, err := prepareMountedWorkspace(&mountedWorkspaceConfig{Disposition: runtimeconfig.WorkspaceDiscardPolicy})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := binding.manager.Acquire(binding.ref, "active-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.close(); !errors.Is(err, workspace.ErrWorkspaceBusy) {
		t.Fatalf("close with active lease err=%v", err)
	}
	if binding.closed {
		t.Fatal("failed close made binding non-retryable")
	}
	if _, err := os.Stat(binding.base); err != nil {
		t.Fatalf("failed close removed manager root: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	base := binding.base
	if err := binding.close(); err != nil {
		t.Fatal(err)
	}
	if !binding.closed {
		t.Fatal("successful close did not close binding")
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("successful close retained manager root: %v", err)
	}
}
