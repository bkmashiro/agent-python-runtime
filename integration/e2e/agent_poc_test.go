package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAgentSourceGetsProfileAndWorkspaceToolsFromHost(t *testing.T) {
	artifact := guestArtifact(t)
	manifest := filepath.Join(filepath.Dir(artifact), "manifest.json")
	if _, err := os.Stat(manifest); err != nil {
		t.Skip("distribution manifest is not available beside the Guest artifact")
	}
	binary := apyrunBinary(t)
	config := filepath.Join(t.TempDir(), "host.json")
	configData := []byte(`{"execution_profile":{"id":"base","allowed_imports":["csv"]},"workspace_files":{"metrics.csv":"name,value\na,2\nb,3\n"},"max_tool_calls":4}`)
	if err := os.WriteFile(config, configData, 0o600); err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"run_id":"agent-poc","code":"import csv\nrows=list(csv.reader(read_text('metrics.csv').splitlines()))\ntotal=sum(int(row[1]) for row in rows[1:])\nwrite_text('summary.txt', str(total))\nresult={'total':total,'files':list_files(),'summary':read_text('summary.txt')}","inputs":{}}`)
	command := exec.Command(binary, "-artifact", artifact, "-manifest", manifest, "-config", config)
	command.Stdin = bytes.NewReader(request)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run apyrun: %v\n%s", err, output)
	}
	var response guestResponse
	if err := json.Unmarshal(output, &response); err != nil {
		t.Fatal(err)
	}
	result, ok := response.Result.(map[string]any)
	if response.Status != "ok" || !ok || result["total"] != float64(5) || result["summary"] != "5" || len(response.Receipts) != 4 {
		t.Fatalf("response=%+v", response)
	}
}
