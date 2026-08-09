package wazero

import (
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

func TestWorkspaceGateDeniesPreparedAccessUntilSingleActivation(t *testing.T) {
	probe := &workspaceGateProbeFS{}
	config, gate, err := newModuleConfig(nil, probe)
	if err != nil {
		t.Fatal(err)
	}
	if config == nil || gate == nil {
		t.Fatal("workspace mount did not create a gate")
	}
	preopen, errno := gate.OpenFile(".", experimentalsys.O_RDONLY|experimentalsys.O_DIRECTORY, 0)
	if errno != 0 || preopen == nil {
		t.Fatalf("virtual preopen unavailable: file=%v errno=%v", preopen, errno)
	}
	if stat, errno := preopen.Stat(); errno != 0 || !stat.Mode.IsDir() {
		t.Fatalf("virtual preopen stat=%+v errno=%v", stat, errno)
	}
	if _, errno := preopen.Readdir(1); errno != experimentalsys.EACCES {
		t.Fatalf("prepared preopen listed workspace: errno=%v", errno)
	}
	if _, errno := gate.Stat("."); errno != experimentalsys.EACCES || probe.statCalls != 0 {
		t.Fatalf("prepared access reached workspace: errno=%v calls=%d", errno, probe.statCalls)
	}
	if errno := gate.Mkdir("state", 0o755); errno != experimentalsys.EACCES {
		t.Fatalf("prepared mutation errno=%v", errno)
	}
	if err := gate.activate(); err != nil {
		t.Fatal(err)
	}
	stat, errno := gate.Stat(".")
	if errno != 0 || !stat.Mode.IsDir() || probe.statCalls != 1 {
		t.Fatalf("active gate did not forward: stat=%+v errno=%v calls=%d", stat, errno, probe.statCalls)
	}
	if err := gate.activate(); err == nil {
		t.Fatal("workspace gate activated twice")
	}
}

func TestModuleConfigWithoutWorkspaceHasNoGate(t *testing.T) {
	config, gate, err := newModuleConfig(nil, nil)
	if err != nil || config == nil || gate != nil {
		t.Fatalf("config=%v gate=%v err=%v", config, gate, err)
	}
}
