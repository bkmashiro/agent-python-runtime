package main

import "testing"

func TestValidateJSONSchema(t *testing.T) {
	schema := []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["ok"],"properties":{"ok":{"const":true}},"additionalProperties":false}`)
	if err := validateJSON(schema, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := validateJSON(schema, []byte(`{"ok":false}`)); err == nil {
		t.Fatal("invalid instance passed schema validation")
	}
}
