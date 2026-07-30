package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseOptionsDerivesBoundedCanaryPolicy(t *testing.T) {
	root := t.TempDir()
	options, err := parseOptions([]string{
		"-artifact", filepath.Join(root, "guest.wasm"),
		"-manifest", filepath.Join(root, "manifest.json"),
		"-socket", filepath.Join(root, "runtime.sock"),
		"-trace-db", filepath.Join(root, "trace.sqlite"),
		"-max-memory-mib", "512",
		"-max-cpu-ms", "15000",
	})
	if err != nil {
		t.Fatal(err)
	}
	config, factory, err := options.runtimePolicy()
	if err != nil {
		t.Fatal(err)
	}
	if config.MemoryLimitPages != 8192 || config.Timeout != 15*time.Second {
		t.Fatalf("unexpected config: %#v", config)
	}
	if factory.PreparedCapacity != 1 || factory.PreparedMaxCapacity != 1 || !factory.AdaptivePreparedRefill {
		t.Fatalf("unexpected derived factory policy: %#v", factory)
	}
}

func TestParseOptionsRejectsUnsafeOrUnboundedConfiguration(t *testing.T) {
	root := t.TempDir()
	base := []string{
		"-artifact", filepath.Join(root, "guest.wasm"),
		"-manifest", filepath.Join(root, "manifest.json"),
		"-socket", filepath.Join(root, "runtime.sock"),
		"-trace-db", filepath.Join(root, "trace.sqlite"),
	}
	cases := map[string][]string{
		"relative artifact": append(append([]string{}, base...), "-artifact", "guest.wasm"),
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
