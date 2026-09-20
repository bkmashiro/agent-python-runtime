// Command agent-core-usecases qualifies common repository, data, and Host-tool workflows.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const program = `import ast
import csv
import io
import json
import os
import tomllib
from pathlib import Path

import numpy as np
import yaml
from helper import tax_rate

project = tomllib.loads(Path("pyproject.toml").read_text())
helper_tree = ast.parse(Path("helper.py").read_text())
functions = [node.name for node in helper_tree.body if isinstance(node, ast.FunctionDef)]
workflow_path = Path("workflow.yml")
workflow = yaml.safe_load(workflow_path.read_text())
workflow["jobs"]["test"]["retries"] = 3
workflow_path.write_text(yaml.safe_dump(workflow, sort_keys=True))

rows = list(csv.DictReader(Path("data.csv").read_text().splitlines()))
events = [json.loads(line) for line in Path("events.jsonl").read_text().splitlines()]
values = np.array([float(row["value"]) for row in rows], dtype=np.float64)
quotes = market.get_prices(symbols=["AAPL", "MSFT"])
summary = {
    "project": project["project"]["name"],
    "function": functions[0],
    "cwd": os.getcwd(),
    "mean": float(values.mean()),
    "event_total": sum(event["count"] for event in events),
    "gross_price": round(quotes["AAPL"] * (1 + tax_rate), 2),
}
Path("summary.json").write_text(json.dumps(summary, sort_keys=True) + "\n")
output = io.StringIO()
writer = csv.writer(output, lineterminator="\n")
writer.writerow(["name", "normalized"])
for row in rows:
    writer.writerow([row["name"], round(float(row["value"]) / float(values.max()), 3)])
Path("clean.csv").write_text(output.getvalue())
Path("REPORT.md").write_text(f"# {summary['project']}\n\nMean: {summary['mean']}\nAAPL gross: {summary['gross_price']}\n")
result = summary
`

type acceptanceResult struct {
	Value        json.RawMessage            `json:"value"`
	Changes      workspacepkg.ChangeSummary `json:"changes"`
	HostRequests int32                      `json:"host_requests"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "agent-core Guest artifact")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	result, err := executeAcceptance(ctx, *guest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func executeAcceptance(ctx context.Context, guestPath string) (acceptanceResult, error) {
	var result acceptanceResult
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return result, err
	}
	root, err := os.MkdirTemp("", "pysolate-agent-core-usecases-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(root)
	base := filepath.Join(root, "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		return result, err
	}
	manager, err := workspacepkg.NewManager(base)
	if err != nil {
		return result, err
	}
	defer manager.Close()
	ref, err := manager.Create(fixtureFiles(), workspacepkg.DefaultLimits())
	if err != nil {
		return result, err
	}
	lease, err := manager.Acquire(ref, "agent-core-usecases")
	if err != nil {
		return result, err
	}
	defer lease.Release()
	var hostRequests atomic.Int32
	marketAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		hostRequests.Add(1)
		var arguments struct {
			Symbols []string `json:"symbols"`
		}
		if err := json.NewDecoder(request.Body).Decode(&arguments); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		prices := map[string]float64{"AAPL": 125, "MSFT": 410}
		response := make(map[string]float64, len(arguments.Symbols))
		for _, symbol := range arguments.Symbols {
			price, ok := prices[symbol]
			if !ok {
				http.Error(w, "unknown symbol", http.StatusNotFound)
				return
			}
			response[symbol] = price
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer marketAPI.Close()
	manifest := pysolate.Manifest{
		"market/prices": {
			PythonPath:  "market.get_prices",
			Description: "Return prices for an allowlisted set of symbols",
			InputSchema: json.RawMessage(`{"type":"object","required":["symbols"],"properties":{"symbols":{"type":"array","items":{"type":"string"}}}}`),
			Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, marketAPI.URL, bytes.NewReader(raw))
				if err != nil {
					return nil, err
				}
				request.Header.Set("Content-Type", "application/json")
				response, err := marketAPI.Client().Do(request)
				if err != nil {
					return nil, err
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusOK {
					return nil, fmt.Errorf("market API returned %s", response.Status)
				}
				var prices map[string]float64
				if err := json.NewDecoder(response.Body).Decode(&prices); err != nil {
					return nil, err
				}
				return prices, nil
			},
		},
	}
	var runner *pysolate.Runner
	if runtime.GOOS == "linux" {
		runner, err = pysolate.NewPreparedWorkspaceCOW(ctx, wasm, manifest)
	} else {
		runner, err = pysolate.NewPreparedWorkspace(ctx, wasm, manifest)
	}
	if err != nil {
		return result, err
	}
	defer runner.Close(context.Background())
	before, err := lease.Snapshot()
	if err != nil {
		return result, err
	}
	output, err := runner.RunWorkspace(ctx, program, nil, lease)
	if err != nil {
		return result, err
	}
	after, err := lease.Snapshot()
	if err != nil {
		return result, err
	}
	result.Value = append(json.RawMessage(nil), output.Value...)
	result.Changes = workspacepkg.Diff(before, after)
	result.HostRequests = hostRequests.Load()
	if err := verifyAcceptance(lease, result); err != nil {
		return result, err
	}
	return result, nil
}

func fixtureFiles() []workspacepkg.InitialFile {
	return []workspacepkg.InitialFile{
		{Path: "helper.py", Data: []byte("tax_rate = 0.2\n\ndef label():\n    return 'agent-core'\n")},
		{Path: "pyproject.toml", Data: []byte("[project]\nname = \"agent-core\"\n")},
		{Path: "workflow.yml", Data: []byte("jobs:\n  test:\n    runs-on: linux\n")},
		{Path: "data.csv", Data: []byte("name,value\nalpha,10\nbeta,20\n")},
		{Path: "events.jsonl", Data: []byte("{\"count\":2}\n{\"count\":3}\n")},
	}
}

func verifyAcceptance(lease *workspacepkg.Lease, result acceptanceResult) error {
	var value struct {
		Project    string  `json:"project"`
		Function   string  `json:"function"`
		CWD        string  `json:"cwd"`
		Mean       float64 `json:"mean"`
		EventTotal int     `json:"event_total"`
		GrossPrice float64 `json:"gross_price"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		return err
	}
	if value.Project != "agent-core" || value.Function != "label" || value.CWD != "/workspace" || value.Mean != 15 || value.EventTotal != 5 || value.GrossPrice != 150 || result.HostRequests != 1 {
		return fmt.Errorf("unexpected result: %#v host_requests=%d", value, result.HostRequests)
	}
	if result.Changes.Added != 3 || result.Changes.Modified != 1 || result.Changes.Deleted != 0 {
		return fmt.Errorf("unexpected changes: %#v", result.Changes)
	}
	expected := map[string]string{
		"summary.json": "{\"cwd\": \"/workspace\", \"event_total\": 5, \"function\": \"label\", \"gross_price\": 150.0, \"mean\": 15.0, \"project\": \"agent-core\"}\n",
		"clean.csv":    "name,normalized\nalpha,0.5\nbeta,1.0\n",
		"REPORT.md":    "# agent-core\n\nMean: 15.0\nAAPL gross: 150.0\n",
	}
	for name, want := range expected {
		file, err := lease.ReadFile(name, 4096)
		if err != nil {
			return err
		}
		if string(file.Data) != want {
			return fmt.Errorf("unexpected %s: %q", name, file.Data)
		}
	}
	workflow, err := lease.ReadFile("workflow.yml", 4096)
	if err != nil {
		return err
	}
	if string(workflow.Data) != "jobs:\n  test:\n    retries: 3\n    runs-on: linux\n" {
		return errors.New("YAML workflow was not safely updated")
	}
	return nil
}
