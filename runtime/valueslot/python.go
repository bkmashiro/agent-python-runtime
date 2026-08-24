package valueslot

import "fmt"

const PythonValueName = "prepared_value"

// PythonPrelude binds one Host-prepared value before the fresh Guest executes.
// The slot table remains Host-owned; Python receives only a scalar or private byte copy.
func PythonPrelude(slotID string) (string, error) {
	if !identityPattern.MatchString(slotID) {
		return "", ErrInvalidSpec
	}
	return fmt.Sprintf(`import json as _pysolate_value_slot_json
import _agent_runtime_host as _pysolate_value_slot_host
_pysolate_value_slot_response = _pysolate_value_slot_host.materialize_slot(%q)
if not isinstance(_pysolate_value_slot_response, bytes) or len(_pysolate_value_slot_response) < 2:
    raise RuntimeError("value-slot response is invalid")
_pysolate_value_slot_tag = _pysolate_value_slot_response[0]
if _pysolate_value_slot_tag == 1:
    %s = _pysolate_value_slot_json.loads(_pysolate_value_slot_response[1:].decode("utf-8"))
elif _pysolate_value_slot_tag == 2:
    %s = bytearray(_pysolate_value_slot_response[1:])
else:
    raise RuntimeError("value-slot strategy is invalid")
del _pysolate_value_slot_response
del _pysolate_value_slot_tag
del _pysolate_value_slot_host
del _pysolate_value_slot_json
`, slotID, PythonValueName, PythonValueName), nil
}
