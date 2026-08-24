package valueslot_test

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
)

func TestPythonPreludeMaterializesOnePreparedValue(t *testing.T) {
	prelude, err := valueslot.PythonPrelude("slot-numpy-sum-v1")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`materialize_slot("slot-numpy-sum-v1")`,
		valueslot.PythonValueName,
		"json.loads",
		"bytearray",
		"del _pysolate_value_slot_host",
	} {
		if !strings.Contains(prelude, expected) {
			t.Fatalf("prelude does not contain %q: %s", expected, prelude)
		}
	}
}

func TestPythonPreludeRejectsInvalidSlot(t *testing.T) {
	if _, err := valueslot.PythonPrelude("slot with spaces"); err == nil {
		t.Fatal("invalid slot was accepted")
	}
}

func TestPreparedValueIsAnAnalyzerFreeRunBindingPass(t *testing.T) {
	pass, err := valueslot.NewPreparedValuePass()
	if err != nil {
		t.Fatal(err)
	}
	registration := pass.Registration()
	if registration.Stage() != passregistration.StageRunBinding || registration.AnalyzerSHA256() != "" {
		t.Fatalf("registration=%+v", registration)
	}
	bound, err := pass.Bind("slot-numpy-sum-v1")
	if err != nil {
		t.Fatal(err)
	}
	direct, err := valueslot.PythonPrelude("slot-numpy-sum-v1")
	if err != nil || bound != direct {
		t.Fatalf("binding changed direct prelude: err=%v", err)
	}
}
