package durable_test

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func providerDB(t *testing.T, directory string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(directory, "provider.db")}
	db, err := sql.Open("sqlite3", u.String()+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(wal)&_pragma=synchronous(full)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS calls(kind TEXT); CREATE TABLE IF NOT EXISTS effects(id TEXT PRIMARY KEY, args BLOB)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func crashTools(t *testing.T, directory string, hold bool) []durable.Tool {
	db := providerDB(t, directory)
	return []durable.Tool{
		{Name: "read", Version: "v1", Recovery: durable.RetrySafe, Call: func(ctx context.Context, _ json.RawMessage) (any, error) {
			_, err := db.ExecContext(ctx, "INSERT INTO calls VALUES ('read')")
			if hold {
				return 7, err
			}
			return 999, err
		}},
		{Name: "write", Version: "v1", Recovery: durable.Idempotent, Call: func(ctx context.Context, args json.RawMessage) (any, error) {
			key, ok := durable.OperationKey(ctx)
			if !ok {
				return nil, fmt.Errorf("missing operation key")
			}
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return nil, err
			}
			defer tx.Rollback()
			if _, err = tx.Exec("INSERT INTO calls VALUES ('write')"); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("INSERT OR IGNORE INTO effects VALUES (?,?)", key, []byte(args)); err != nil {
				return nil, err
			}
			if err = tx.Commit(); err != nil {
				return nil, err
			}
			if hold {
				fmt.Println("provider-committed")
				select {}
			}
			var saved []byte
			err = db.QueryRowContext(ctx, "SELECT args FROM effects WHERE id=?", key).Scan(&saved)
			return json.RawMessage(saved), err
		}},
	}
}

func TestCrashHelper(t *testing.T) {
	directory := os.Getenv("PYSOLATE_CRASH_CHILD")
	if directory == "" {
		return
	}
	if os.Getenv("PYSOLATE_CRASH_RESUME") == "1" {
		recoverAfterCrash(t, directory)
		fmt.Println("recovered: read=1 requests=2 effects=1")
		return
	}
	ctx := context.Background()
	store, err := durable.Open(filepath.Join(directory, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := durable.NewRunner(ctx, store, realGuest(t), "crash-v1", crashTools(t, directory, true), crashPreparation()...)
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Create(ctx, durable.Definition{ID: "crash", Code: "x = read()\nresult = write(value=x)", Inputs: json.RawMessage(`{}`), Seed: "seed", ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "crash-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runner.Resume(ctx, "crash"); err != nil {
		t.Fatal(err)
	}
	t.Fatal("fixture returned past hold point")
}

func TestRealProcessKillAfterExternalCommit(t *testing.T) {
	directory := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	command.Env = append(os.Environ(), "PYSOLATE_CRASH_CHILD="+directory)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer command.Process.Kill()
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		if scanner.Scan() {
			ready <- scanner.Text()
		} else {
			ready <- "EOF"
		}
	}()
	select {
	case line := <-ready:
		if line != "provider-committed" {
			_ = command.Process.Kill()
			_ = command.Wait()
			t.Fatalf("line=%q stderr=%s", line, &stderr)
		}
	case <-time.After(time.Minute):
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("hold point timeout: %s", &stderr)
	}
	if err = command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err = command.Wait(); err == nil {
		t.Fatal("expected killed process")
	}
	recoverAfterCrash(t, directory)
}

func recoverAfterCrash(t *testing.T, directory string) {
	t.Helper()
	ctx := context.Background()
	store, err := durable.Open(filepath.Join(directory, "runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner, err := durable.NewRunner(ctx, store, realGuest(t), "crash-v1", crashTools(t, directory, false), crashPreparation()...)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	out, err := runner.Resume(ctx, "crash")
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]int
	if err = json.Unmarshal(out.Value, &result); err != nil || result["value"] != 7 {
		t.Fatalf("out=%s error=%v", out.Value, err)
	}
	db := providerDB(t, directory)
	var reads, writes, effects int
	if err = db.QueryRow("SELECT (SELECT count(*) FROM calls WHERE kind='read'),(SELECT count(*) FROM calls WHERE kind='write'),(SELECT count(*) FROM effects)").Scan(&reads, &writes, &effects); err != nil {
		t.Fatal(err)
	}
	if reads != 1 || writes != 2 || effects != 1 {
		t.Fatalf("read=%d write=%d effects=%d", reads, writes, effects)
	}
}

func crashPreparation() []durable.Preparation {
	switch os.Getenv("PYSOLATE_CRASH_PREPARED") {
	case "copy":
		return []durable.Preparation{{Seed: "seed"}}
	case "cow":
		return []durable.Preparation{{Seed: "seed", COW: true}}
	default:
		return nil
	}
}

func TestRealPreparedProcessKillAfterExternalCommit(t *testing.T) {
	t.Setenv("PYSOLATE_CRASH_PREPARED", "copy")
	TestRealProcessKillAfterExternalCommit(t)
}
