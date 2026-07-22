package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func validateJSON(schemaBytes, instanceBytes []byte) error {
	var schemaDocument any
	if err := json.Unmarshal(schemaBytes, &schemaDocument); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaDocument); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	schema, err := compiler.Compile("schema.json")
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	var instance any
	if err := json.Unmarshal(instanceBytes, &instance); err != nil {
		return fmt.Errorf("decode instance: %w", err)
	}
	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("validate instance: %w", err)
	}
	return nil
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: validate-json-schema <schema.json> <instance.json>")
	}
	schemaBytes, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read schema: %w", err)
	}
	instanceBytes, err := os.ReadFile(args[1])
	if err != nil {
		return fmt.Errorf("read instance: %w", err)
	}
	return validateJSON(schemaBytes, instanceBytes)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
