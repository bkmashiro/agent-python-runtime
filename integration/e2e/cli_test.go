package e2e_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
)

func TestCLIWithRealGuestArtifact(t *testing.T) {
	artifact := guestArtifact(t)
	binary := apyrunBinary(t)

	run := func(config, request string) (int, []byte, []byte) {
		args := []string{"-artifact", artifact}
		if config != "" {
			path := filepath.Join(t.TempDir(), "operator.json")
			if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "-config", path)
		}
		command := exec.Command(binary, args...)
		command.Stdin = strings.NewReader(request)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		if err == nil {
			return 0, stdout.Bytes(), stderr.Bytes()
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), stdout.Bytes(), stderr.Bytes()
		}
		t.Fatal(err)
		return -1, nil, nil
	}

	exit, stdout, stderr := run("", `{"run_id":"cli","code":"result = inputs['value'] + 1","inputs":{"value":41}}`)
	if exit != 0 || len(stderr) != 0 {
		t.Fatalf("exit=%d stderr=%q", exit, stderr)
	}
	var response guestResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "ok" || response.Result != float64(42) {
		t.Fatalf("unexpected response: %#v", response)
	}

	timeoutCapsule := filepath.Join(t.TempDir(), "timeout-state.pwc")
	timeoutConfig, err := json.Marshal(map[string]any{
		"timeout_ms": 50,
		"workspace":  map[string]any{"output_capsule": timeoutCapsule},
	})
	if err != nil {
		t.Fatal(err)
	}
	timeoutRequest, err := json.Marshal(map[string]any{
		"run_id": "timeout",
		"code":   "with open('/workspace/partial.txt', 'w') as f:\n    f.write('partial')\nwhile True:\n    pass",
		"inputs": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = run(string(timeoutConfig), string(timeoutRequest))
	if exit == 0 || len(stdout) != 0 || !strings.Contains(string(stderr), "execute guest") {
		t.Fatalf("timeout did not fail closed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if _, err := os.Stat(timeoutCapsule); !os.IsNotExist(err) {
		t.Fatalf("timeout published partial capsule: %v", err)
	}

	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "input.txt"), []byte("seed"), 0o644); err != nil {
		t.Fatal(err)
	}
	capsuleOne := filepath.Join(t.TempDir(), "state-one.pwc")
	firstConfig, err := json.Marshal(map[string]any{"workspace": map[string]any{
		"source_directory": source, "output_capsule": capsuleOne,
	}})
	if err != nil {
		t.Fatal(err)
	}
	firstRequest, err := json.Marshal(map[string]any{
		"run_id": "workspace-first",
		"code":   "with open('/workspace/input.txt', 'r') as f:\n    value = f.read()\nwith open('/workspace/state.txt', 'w') as f:\n    f.write(value + '-one')\nresult = value",
		"inputs": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = run(string(firstConfig), string(firstRequest))
	if exit != 0 || len(stderr) != 0 {
		t.Fatalf("first workspace run exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if err := json.Unmarshal(stdout, &response); err != nil || response.Status != "ok" || response.Result != "seed" {
		t.Fatalf("first workspace response=%#v err=%v", response, err)
	}
	if info, err := os.Stat(capsuleOne); err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		t.Fatalf("first capsule info=%v err=%v", info, err)
	}

	capsuleTwo := filepath.Join(t.TempDir(), "state-two.pwc")
	secondConfig, err := json.Marshal(map[string]any{"workspace": map[string]any{
		"input_capsule": capsuleOne, "output_capsule": capsuleTwo,
	}})
	if err != nil {
		t.Fatal(err)
	}
	secondRequest, err := json.Marshal(map[string]any{
		"run_id": "workspace-second",
		"code":   "with open('/workspace/state.txt', 'r') as f:\n    value = f.read()\nwith open('/workspace/state.txt', 'a') as f:\n    f.write('-two')\nresult = value",
		"inputs": map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit, stdout, stderr = run(string(secondConfig), string(secondRequest))
	if exit != 0 || len(stderr) != 0 {
		t.Fatalf("restored workspace run exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
	if err := json.Unmarshal(stdout, &response); err != nil || response.Status != "ok" || response.Result != "seed-one" {
		t.Fatalf("restored workspace response=%#v err=%v", response, err)
	}
	if info, err := os.Stat(capsuleTwo); err != nil || info.Mode().Perm() != 0o600 || info.Size() == 0 {
		t.Fatalf("second capsule info=%v err=%v", info, err)
	}
}

func apyrunBinary(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("APYRUN_BIN"); binary != "" {
		if info, err := os.Stat(binary); err != nil || info.Mode()&0o111 == 0 {
			t.Fatalf("APYRUN_BIN is not executable: %q", binary)
		}
		return binary
	}
	root := repositoryRoot(t)
	binary := filepath.Join(t.TempDir(), "apyrun")
	build := exec.Command("go", "build", "-o", binary, "./cmd/apyrun")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build apyrun: %v\n%s", err, output)
	}
	return binary
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
