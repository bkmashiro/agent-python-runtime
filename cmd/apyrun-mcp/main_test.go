package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseOptionsDerivesBoundedCodexPolicy(t *testing.T) {
	root := t.TempDir()
	value, err := parseOptions([]string{
		"-artifact", filepath.Join(root, "agent-python-runtime.wasm"),
		"-manifest", filepath.Join(root, "manifest.json"),
		"-trace-db", filepath.Join(root, "trace.sqlite"),
		"-max-memory-mib", "512",
		"-max-cpu-ms", "15000",
	})
	if err != nil {
		t.Fatal(err)
	}
	config, factory, err := value.runtimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if config.MemoryLimitPages != 8192 || config.Timeout != 15*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
	if factory.PreparedCapacity != 1 || factory.PreparedMaxCapacity != 1 || !factory.AdaptivePreparedRefill {
		t.Fatalf("unexpected factory: %#v", factory)
	}
}

func TestParseOptionsRejectsUnsafeOrUnboundedConfiguration(t *testing.T) {
	root := t.TempDir()
	base := []string{
		"-artifact", filepath.Join(root, "agent-python-runtime.wasm"),
		"-manifest", filepath.Join(root, "manifest.json"),
		"-trace-db", filepath.Join(root, "trace.sqlite"),
	}
	cases := map[string][]string{
		"relative artifact": append(append([]string{}, base...), "-artifact", "guest.wasm"),
		"relative trace":    append(append([]string{}, base...), "-trace-db", "trace.sqlite"),
		"excess memory":     append(append([]string{}, base...), "-max-memory-mib", "1025"),
		"excess cpu":        append(append([]string{}, base...), "-max-cpu-ms", "300001"),
		"unexpected arg":    append(append([]string{}, base...), "extra"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseOptions(args); err == nil {
				t.Fatal("unsafe options were accepted")
			}
		})
	}
}

func TestRequirePrivateOwnedDir(t *testing.T) {
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := requirePrivateOwnedDir(path); err != nil {
		t.Fatalf("owned private directory rejected: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := requirePrivateOwnedDir(path); err == nil {
		t.Fatal("non-private directory accepted")
	}
}
