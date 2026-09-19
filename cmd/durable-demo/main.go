package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/durable"
)

const (
	demoVersion = "demo-v1"
	defaultRun  = "demo-run"
)

type options struct {
	db, guest, runID, providerDB, readValue, holdPoint, decision, waitID string
}

func main() {
	if len(os.Args) < 2 {
		fatalf("usage: durable-demo create|resume|decide|show|provider-status [flags]")
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fatalf("%s", err)
	}
}

func run(command string, args []string) error {
	opts, positional, err := parseOptions(command, args)
	if err != nil {
		return err
	}
	switch command {
	case "create":
		return createRun(opts)
	case "resume":
		return resumeRun(opts)
	case "decide":
		if len(positional) > 1 {
			return fmt.Errorf("decide accepts at most one positional decision")
		}
		if opts.decision == "" && len(positional) == 1 {
			opts.decision = positional[0]
		}
		return decideRun(opts)
	case "show":
		if len(positional) != 0 {
			return fmt.Errorf("show does not accept positional arguments")
		}
		return showRun(opts)
	case "provider-status":
		if len(positional) != 0 {
			return fmt.Errorf("provider-status does not accept positional arguments")
		}
		return showProvider(opts)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func parseOptions(command string, args []string) (options, []string, error) {
	opts := options{db: "durable-demo.db", runID: defaultRun, providerDB: "durable-demo-provider.db"}
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&opts.db, "db", opts.db, "durable run SQLite database")
	fs.StringVar(&opts.guest, "guest", "", "verified Guest wasm path")
	fs.StringVar(&opts.runID, "run-id", opts.runID, "durable run id")
	fs.StringVar(&opts.providerDB, "provider-db", opts.providerDB, "independent provider fixture SQLite database")
	fs.StringVar(&opts.readValue, "read-value", "", "provider read value; on resume, empty preserves it")
	fs.StringVar(&opts.holdPoint, "hold-point", "", "waiting or provider-committed")
	fs.StringVar(&opts.decision, "decision", "", "approval JSON object or bool")
	fs.StringVar(&opts.waitID, "wait-id", "", "wait id; defaults to run-id/wait/1")
	if err := fs.Parse(args); err != nil {
		return options{}, nil, err
	}
	if opts.holdPoint != "" && opts.holdPoint != "waiting" && opts.holdPoint != "provider-committed" {
		return options{}, nil, fmt.Errorf("invalid hold-point %q", opts.holdPoint)
	}
	if opts.runID == "" {
		return options{}, nil, fmt.Errorf("run-id is required")
	}
	if command == "create" || command == "resume" {
		if opts.guest == "" {
			return options{}, nil, fmt.Errorf("-guest is required for %s", command)
		}
	}
	return opts, fs.Args(), nil
}

func createRun(opts options) error {
	store, err := durable.Open(opts.db)
	if err != nil {
		return err
	}
	defer store.Close()
	p, runner, artifactSHA, err := makeRunner(store, opts)
	if err != nil {
		return err
	}
	defer p.close()
	defer runner.Close(context.Background())
	readValue := opts.readValue
	if readValue == "" {
		readValue = "7"
	}
	if err := p.setReadValue(readValue); err != nil {
		return err
	}
	definition := durable.Definition{
		ID: opts.runID, Code: demoCode, Seed: demoVersion,
		ArtifactSHA256: artifactSHA, EnvironmentVersion: demoVersion,
		Inputs: json.RawMessage(`{"seed":417,"read_key":"demo"}`),
	}
	run, err := runner.Create(context.Background(), definition)
	if err != nil {
		return err
	}
	return printJSON(run)
}

func resumeRun(opts options) error {
	store, err := durable.Open(opts.db)
	if err != nil {
		return err
	}
	defer store.Close()
	p, runner, _, err := makeRunner(store, opts)
	if err != nil {
		return err
	}
	defer p.close()
	defer runner.Close(context.Background())
	if err := p.setReadValue(opts.readValue); err != nil {
		return err
	}
	payload, runErr := runner.Resume(context.Background(), opts.runID)
	if runErr != nil {
		var parked *durable.ParkError
		if errors.As(runErr, &parked) {
			event := map[string]any{"event": "waiting", "run_id": opts.runID, "wait_id": parked.WaitID}
			if err := printJSON(event); err != nil {
				return err
			}
			if opts.holdPoint == "waiting" {
				time.Sleep(365 * 24 * time.Hour)
			}
			return nil
		}
		return runErr
	}
	return printJSON(json.RawMessage(payload))
}

func decideRun(opts options) error {
	if opts.decision == "" {
		return fmt.Errorf("-decision or one positional JSON/bool value is required")
	}
	decision, err := parseDecision(opts.decision)
	if err != nil {
		return err
	}
	store, err := durable.Open(opts.db)
	if err != nil {
		return err
	}
	defer store.Close()
	waitID := opts.waitID
	if waitID == "" {
		waitID = opts.runID + "/wait/1"
	}
	if err := store.ResolveWait(context.Background(), waitID, durable.Decision{Result: decision}); err != nil {
		return err
	}
	return printJSON(map[string]any{"status": "decided", "run_id": opts.runID, "wait_id": waitID, "decision": json.RawMessage(decision)})
}

func showRun(opts options) error {
	store, err := durable.Open(opts.db)
	if err != nil {
		return err
	}
	defer store.Close()
	run, err := store.Get(context.Background(), opts.runID)
	if err != nil {
		return err
	}
	out := map[string]any{"run": run}
	if wait, err := store.GetWait(context.Background(), opts.runID+"/wait/1"); err == nil {
		out["wait"] = wait
	}
	return printJSON(out)
}

func showProvider(opts options) error {
	p, err := openProvider(opts.providerDB, "")
	if err != nil {
		return err
	}
	defer p.close()
	status, err := p.status(context.Background())
	if err != nil {
		return err
	}
	return printJSON(status)
}

func makeRunner(store *durable.Store, opts options) (*provider, *durable.Runner, string, error) {
	if filepath.Clean(opts.db) == filepath.Clean(opts.providerDB) {
		return nil, nil, "", fmt.Errorf("db and provider-db must be independent")
	}
	artifact, identity, err := verifiedGuest(opts.guest)
	if err != nil {
		return nil, nil, "", err
	}
	p, err := openProvider(opts.providerDB, opts.holdPoint)
	if err != nil {
		return nil, nil, "", err
	}
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"durable-demo"}`))
	if err != nil {
		p.close()
		return nil, nil, "", err
	}
	tools := []durable.Tool{
		{Spec: readSpec(), Grant: grant, Recovery: durable.RetrySafe,
			Handler: capability.HandlerFunc(func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
				key, ok := durable.OperationKey(ctx)
				if !ok {
					return nil, fmt.Errorf("read handler missing operation key")
				}
				return p.read(ctx, key, args)
			})},
		{Spec: approvalSpec(), Grant: grant, Recovery: durable.WaitMode,
			Wait: func(_ context.Context, call capability.LoggedCall) (durable.WaitSpec, error) {
				return durable.WaitSpec{Kind: "approval", Request: append(json.RawMessage(nil), call.Arguments...)}, nil
			}},
		{Spec: writeSpec(), Grant: grant, Recovery: durable.Idempotent,
			Handler: capability.HandlerFunc(func(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
				key, ok := durable.OperationKey(ctx)
				if !ok {
					return nil, fmt.Errorf("write handler missing operation key")
				}
				return p.write(ctx, key, args)
			})},
	}
	runner, err := durable.NewRunner(store, artifact, identity, demoVersion, tools)
	if err != nil {
		p.close()
		return nil, nil, "", err
	}
	return p, runner, identity.ArtifactSHA256, nil
}

func verifiedGuest(path string) ([]byte, runtimeconfig.VerifiedArtifactIdentity, error) {
	artifact, err := os.ReadFile(path)
	if err != nil {
		return nil, runtimeconfig.VerifiedArtifactIdentity{}, err
	}
	root := filepath.Dir(path)
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, name)) }
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, runtimeconfig.VerifiedArtifactIdentity{}, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return nil, runtimeconfig.VerifiedArtifactIdentity{}, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return nil, runtimeconfig.VerifiedArtifactIdentity{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(path), artifact, manifest, inventory, qualification)
	if err != nil {
		return nil, runtimeconfig.VerifiedArtifactIdentity{}, fmt.Errorf("verify Guest distribution: %w", err)
	}
	return artifact, identity, nil
}

func readSpec() capability.Spec {
	return capability.Spec{Name: "demo.read", Version: demoVersion + ".read", Description: "Retry-safe provider read", EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: demoVersion + ".read-handler", InputSchema: json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`), Python: &capability.PythonProjection{Module: "demo", Method: "read", Arguments: []string{"key"}}}
}

func approvalSpec() capability.Spec {
	return capability.Spec{Name: "demo.approve", Version: demoVersion + ".approval", Description: "Approval wait", EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: demoVersion + ".approval-handler", InputSchema: json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"}},"required":["prompt"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"approved":{"type":"boolean"}},"required":["approved"],"additionalProperties":false}`), Python: &capability.PythonProjection{Module: "demo", Method: "approve", Arguments: []string{"prompt"}}}
}

func writeSpec() capability.Spec {
	return capability.Spec{Name: "demo.write", Version: demoVersion + ".write", Description: "Idempotent provider write", EffectClass: capability.EffectExternalWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: demoVersion + ".write-handler", InputSchema: json.RawMessage(`{"type":"object","properties":{"read_value":{"type":"integer"},"random_value":{"type":"integer"},"computed":{"type":"integer"}},"required":["read_value","random_value","computed"],"additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","properties":{"written":{"type":"boolean"},"computed":{"type":"integer"},"operation_key":{"type":"string"}},"required":["written","computed","operation_key"],"additionalProperties":false}`), Python: &capability.PythonProjection{Module: "demo", Method: "write", Arguments: []string{"read_value", "random_value", "computed"}, ResultField: "written"}}
}

const demoCode = `import random
read = demo.read("demo")
random.seed(inputs["seed"])
random_value = random.randint(0, 999999)
read_value = int(read["value"])
computed = read_value + random_value
approval = demo.approve("write the computed demo value")
if approval["approved"]:
    written = demo.write(read_value, random_value, computed)
else:
    written = None
result = {"read_value": read_value, "random_value": random_value, "computed": computed, "approval": approval, "written": written}`

func parseDecision(raw string) (json.RawMessage, error) {
	raw = strings.TrimSpace(raw)
	if raw == "true" || raw == "false" {
		return json.RawMessage(`{"approved":` + raw + `}`), nil
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return nil, fmt.Errorf("decision must be a JSON object or bool")
	}
	if _, ok := value["approved"]; !ok {
		return nil, fmt.Errorf("decision object requires approved")
	}
	return json.RawMessage(raw), nil
}

func printJSON(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
