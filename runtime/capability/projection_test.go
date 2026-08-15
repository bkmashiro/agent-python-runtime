package capability_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestSealedPlanGeneratesNamespacedProjectionAndToolSchemas(t *testing.T) {
	registry := capability.NewRegistry()
	spec := testSpec()
	spec.Python = &capability.PythonProjection{
		Module: "workspace", Method: "read_text", GlobalAlias: "read_text",
		Arguments: []string{"path"}, ResultField: "content",
	}
	if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	prelude := plan.PythonPrelude()
	for _, fragment := range []string{
		"class _CapabilityModule:",
		"workspace = _CapabilityModule()",
		`_capability_call("workspace.read_text", {"path": path})["content"]`,
		"workspace.read_text = _capability_proxy_0",
		"read_text = workspace.read_text",
	} {
		if !strings.Contains(prelude, fragment) {
			t.Fatalf("prelude missing %q:\n%s", fragment, prelude)
		}
	}
	tools := plan.ToolSchemas()
	sealedSpec := plan.Specs()[0]
	if len(tools) != 1 || tools[0].Name != spec.Name || tools[0].Description != spec.Description || tools[0].EffectClass != spec.EffectClass || tools[0].Playback != spec.Playback ||
		string(tools[0].InputSchema) != string(sealedSpec.InputSchema) || string(tools[0].OutputSchema) != string(sealedSpec.OutputSchema) {
		t.Fatalf("tool schemas=%#v", tools)
	}
	tools[0].InputSchema[0] = 'x'
	if !json.Valid(plan.ToolSchemas()[0].InputSchema) {
		t.Fatal("Plan.ToolSchemas leaked mutable schema bytes")
	}
}

func TestNamespacedProjectionIsOrderIndependent(t *testing.T) {
	makePlan := func(reverse bool) *capability.Plan {
		t.Helper()
		registry := capability.NewRegistry()
		specs := []capability.Spec{testSpec(), testSpec()}
		specs[0].Name, specs[0].Version = "sources.alpha", "pysolate.sources.alpha.v1"
		specs[0].Python = &capability.PythonProjection{Module: "sources", Method: "alpha", Arguments: []string{}}
		specs[1].Name, specs[1].Version = "sources.beta", "pysolate.sources.beta.v1"
		specs[1].Python = &capability.PythonProjection{Module: "sources", Method: "beta", Arguments: []string{}}
		if reverse {
			specs[0], specs[1] = specs[1], specs[0]
		}
		for _, spec := range specs {
			if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first, second := makePlan(false), makePlan(true)
	if first.Identity() != second.Identity() || first.PythonPrelude() != second.PythonPrelude() {
		t.Fatalf("projection order drift identity=%v prelude=%v", first.Identity() == second.Identity(), first.PythonPrelude() == second.PythonPrelude())
	}
}

func TestNamespacedProjectionRejectsCollisionsAndInjection(t *testing.T) {
	for name, mutate := range map[string]func(*capability.PythonProjection){
		"module injection": func(projection *capability.PythonProjection) { projection.Module = "sources;import_os" },
		"method injection": func(projection *capability.PythonProjection) { projection.Method = "read-text" },
		"alias builtin":    func(projection *capability.PythonProjection) { projection.GlobalAlias = "len" },
		"reserved module":  func(projection *capability.PythonProjection) { projection.Module = "inputs" },
	} {
		t.Run(name, func(t *testing.T) {
			registry := capability.NewRegistry()
			spec := testSpec()
			spec.Python = &capability.PythonProjection{Module: "workspace", Method: "read_text", GlobalAlias: "read_text", Arguments: []string{"path"}}
			mutate(spec.Python)
			if err := registry.Register(spec, basicGrant(t), noopHandler); err != capability.ErrInvalidTool {
				t.Fatalf("error=%v", err)
			}
		})
	}

	registry := capability.NewRegistry()
	first := testSpec()
	first.Python = &capability.PythonProjection{Module: "sources", Method: "catalog", Arguments: []string{}}
	if err := registry.Register(first, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	second := testSpec()
	second.Name, second.Version = "sources.other", "pysolate.sources.other.v1"
	second.Python = &capability.PythonProjection{Module: "sources", Method: "catalog", Arguments: []string{}}
	if err := registry.Register(second, basicGrant(t), noopHandler); err != capability.ErrToolExists {
		t.Fatalf("duplicate module method error=%v", err)
	}
}

func TestPlanPresentsDirectProgrammaticAndBothWithoutChangingRegistry(t *testing.T) {
	registry := capability.NewRegistry()
	spec := testSpec()
	spec.Python = &capability.PythonProjection{Module: "workspace", Method: "read_text", Arguments: []string{"path"}, ResultField: "content"}
	if err := registry.Register(spec, basicGrant(t), noopHandler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}

	direct, err := plan.Present(capability.ProgramSurfaceDirect, "")
	if err != nil || len(direct.Tools) != 1 || direct.PythonPrelude != "" || direct.ParentCallID != "" {
		t.Fatalf("direct=%+v err=%v", direct, err)
	}
	programmatic, err := plan.Present(capability.ProgramSurfaceProgrammatic, "parent-call")
	if err != nil || len(programmatic.Tools) != 0 || programmatic.PythonPrelude == "" || programmatic.ParentCallID != "parent-call" {
		t.Fatalf("programmatic=%+v err=%v", programmatic, err)
	}
	for _, fragment := range []string{`_program_parent_call_id = "parent-call"`, `+ ":program:" + str(_capability_call_sequence)`} {
		if !strings.Contains(programmatic.PythonPrelude, fragment) {
			t.Fatalf("programmatic prelude missing %q:\n%s", fragment, programmatic.PythonPrelude)
		}
	}
	both, err := plan.Present(capability.ProgramSurfaceBoth, "parent-call")
	if err != nil || len(both.Tools) != 1 || both.PythonPrelude == "" {
		t.Fatalf("both=%+v err=%v", both, err)
	}
	if _, err := plan.Present(capability.ProgramSurfaceProgrammatic, "bad parent"); err == nil {
		t.Fatal("invalid parent identity accepted")
	}
}
