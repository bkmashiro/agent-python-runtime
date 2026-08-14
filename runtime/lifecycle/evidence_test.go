package lifecycle

import "testing"

func TestEvidenceValidation(t *testing.T) {
	e := Evidence{SchemaVersion: SchemaVersion, ExecutionID: "exec-1", Backend: "native_sandbox", ArtifactIdentity: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LogicalExecutions: 1, PhysicalExecutions: 1, Phases: []Phase{{Name: "artifact.verify", WallNanoseconds: 1}, {Name: "backend.execute", WallNanoseconds: 2}}, Cleanup: Cleanup{Process: true, Socket: true, Mount: true, Cgroup: true, WorkspaceLease: true}, TerminalStatus: "ok"}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.Phases[1].Name = "artifact.before"
	if err := e.Validate(); err == nil {
		t.Fatal("out-of-order phases accepted")
	}
}
