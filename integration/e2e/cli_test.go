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

	exit, stdout, stderr = run(`{"timeout_ms":50}`, `{"run_id":"timeout","code":"while True:\n    pass","inputs":{}}`)
	if exit == 0 || len(stdout) != 0 || !strings.Contains(string(stderr), "execute guest") {
		t.Fatalf("timeout did not fail closed: exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
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
