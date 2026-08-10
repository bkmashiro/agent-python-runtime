package wazero

import (
	"errors"
	"io/fs"
	"testing"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

type workspaceGateProbeFS struct {
	experimentalsys.UnimplementedFS
	statCalls int
}

func (probe *workspaceGateProbeFS) Stat(string) (wazerosys.Stat_t, experimentalsys.Errno) {
	probe.statCalls++
	return wazerosys.Stat_t{Mode: fs.ModeDir | 0o755, Nlink: 1}, 0
}

type workspaceGateProbeTemporary struct {
	filesystem experimentalsys.FS
	closeErr   error
	closeCalls int
	closed     bool
}

func (temporary *workspaceGateProbeTemporary) FS() experimentalsys.FS { return temporary.filesystem }
func (temporary *workspaceGateProbeTemporary) Close() error {
	temporary.closeCalls++
	if temporary.closeErr != nil {
		return temporary.closeErr
	}
	temporary.closed = true
	return nil
}

func assertVirtualPreparedPreopen(t *testing.T, gate *workspaceGate) {
	t.Helper()
	preopen, errno := gate.OpenFile(".", experimentalsys.O_RDONLY|experimentalsys.O_DIRECTORY, 0)
	if errno != 0 || preopen == nil {
		t.Fatalf("virtual preopen unavailable: file=%v errno=%v", preopen, errno)
	}
	if stat, errno := preopen.Stat(); errno != 0 || !stat.Mode.IsDir() {
		t.Fatalf("virtual preopen stat=%+v errno=%v", stat, errno)
	}
	if _, errno := preopen.Readdir(1); errno != experimentalsys.EACCES {
		t.Fatalf("prepared preopen listed mounted content: errno=%v", errno)
	}
	if errno := preopen.Close(); errno != 0 {
		t.Fatal(errno)
	}
}

func TestModuleMountsDenyPreparedAccessAndCreateTemporaryAtActivation(t *testing.T) {
	workspaceProbe := &workspaceGateProbeFS{}
	temporaryProbe := &workspaceGateProbeFS{}
	temporary := &workspaceGateProbeTemporary{filesystem: temporaryProbe}
	factoryCalls := 0
	config, mounts, err := newModuleConfig(nil, workspaceProbe, func() (temporaryMount, error) {
		factoryCalls++
		return temporary, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if config == nil || mounts == nil || mounts.workspace == nil || mounts.temporary == nil {
		t.Fatal("workspace and temporary mounts were not configured")
	}
	assertVirtualPreparedPreopen(t, mounts.workspace)
	assertVirtualPreparedPreopen(t, mounts.temporary)
	if _, errno := mounts.workspace.Stat("."); errno != experimentalsys.EACCES || workspaceProbe.statCalls != 0 {
		t.Fatalf("prepared access reached workspace: errno=%v calls=%d", errno, workspaceProbe.statCalls)
	}
	if _, errno := mounts.temporary.Stat("."); errno != experimentalsys.EACCES || temporaryProbe.statCalls != 0 || factoryCalls != 0 {
		t.Fatalf("prepared access created temporary: errno=%v calls=%d factory=%d", errno, temporaryProbe.statCalls, factoryCalls)
	}
	if err := mounts.activate(); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 1 {
		t.Fatalf("temporary factory calls=%d", factoryCalls)
	}
	if stat, errno := mounts.workspace.Stat("."); errno != 0 || !stat.Mode.IsDir() || workspaceProbe.statCalls != 1 {
		t.Fatalf("active workspace did not forward: stat=%+v errno=%v calls=%d", stat, errno, workspaceProbe.statCalls)
	}
	if stat, errno := mounts.temporary.Stat("."); errno != 0 || !stat.Mode.IsDir() || temporaryProbe.statCalls != 1 {
		t.Fatalf("active temporary did not forward: stat=%+v errno=%v calls=%d", stat, errno, temporaryProbe.statCalls)
	}
	if err := mounts.activate(); err == nil {
		t.Fatal("module mounts activated twice")
	}
	if err := mounts.close(); err != nil || !temporary.closed {
		t.Fatalf("temporary close: closed=%v err=%v", temporary.closed, err)
	}
	if err := mounts.close(); err != nil {
		t.Fatalf("module mounts close was not idempotent: %v", err)
	}
}

func TestModuleMountsPropagateAndRetryTemporaryCleanupFailure(t *testing.T) {
	sentinel := errors.New("remove temporary")
	temporary := &workspaceGateProbeTemporary{filesystem: &workspaceGateProbeFS{}, closeErr: sentinel}
	mounts, err := newModuleMounts(&workspaceGateProbeFS{}, func() (temporaryMount, error) {
		return temporary, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mounts.activate(); err != nil {
		t.Fatal(err)
	}
	if err := mounts.close(); !errors.Is(err, sentinel) || temporary.closed || temporary.closeCalls != 1 {
		t.Fatalf("cleanup failure was hidden: err=%v closed=%v calls=%d", err, temporary.closed, temporary.closeCalls)
	}
	temporary.closeErr = nil
	if err := mounts.close(); err != nil || !temporary.closed || temporary.closeCalls != 2 {
		t.Fatalf("cleanup retry failed: err=%v closed=%v calls=%d", err, temporary.closed, temporary.closeCalls)
	}
}

func TestModuleMountsRetainTemporaryWhenActivationAndCleanupFail(t *testing.T) {
	sentinel := errors.New("remove temporary")
	temporary := &workspaceGateProbeTemporary{filesystem: &workspaceGateProbeFS{}, closeErr: sentinel}
	mounts, err := newModuleMounts(&workspaceGateProbeFS{}, func() (temporaryMount, error) {
		return temporary, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mounts.temporary.target.Store(&workspaceTarget{filesystem: &workspaceGateProbeFS{}})
	if err := mounts.activate(); err == nil || !errors.Is(err, sentinel) || temporary.closeCalls != 1 {
		t.Fatalf("activation cleanup failure was hidden: err=%v calls=%d", err, temporary.closeCalls)
	}
	temporary.closeErr = nil
	if err := mounts.close(); err != nil || !temporary.closed || temporary.closeCalls != 2 {
		t.Fatalf("retained temporary was not retried: err=%v closed=%v calls=%d", err, temporary.closed, temporary.closeCalls)
	}
}

func TestModuleConfigRequiresCompleteMountBinding(t *testing.T) {
	config, mounts, err := newModuleConfig(nil, nil, nil)
	if err != nil || config == nil || mounts != nil {
		t.Fatalf("config=%v mounts=%v err=%v", config, mounts, err)
	}
	probe := &workspaceGateProbeFS{}
	if _, _, err := newModuleConfig(nil, probe, nil); err == nil {
		t.Fatal("workspace without temporary factory was accepted")
	}
	if _, _, err := newModuleConfig(nil, nil, func() (temporaryMount, error) { return nil, nil }); err == nil {
		t.Fatal("temporary factory without workspace was accepted")
	}
}
