package valueslot_test

import (
	"strings"
	"testing"

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
