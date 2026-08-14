package native

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOCIBundleBindsReadonlyRootAndResources(t *testing.T) {
	bundle, rootfs, channel := filepath.Join(t.TempDir(), "bundle"), filepath.Join(t.TempDir(), "rootfs"), filepath.Join(t.TempDir(), "channel")
	workspace := filepath.Join(t.TempDir(), "workspace")
	for _, directory := range []string{bundle, rootfs, channel, workspace} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeOCIBundle(bundle, rootfs, channel, workspace, "container-1", []string{"PATH=/usr/local/bin:/usr/bin:/bin", "TOKEN=transport-only"}, 256<<20, 64); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bundle, "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	var document map[string]any
	encoded, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	root := document["root"].(map[string]any)
	if root["path"] != rootfs || root["readonly"] != true {
		t.Fatalf("root=%v", root)
	}
	linux := document["linux"].(map[string]any)
	resources := linux["resources"].(map[string]any)
	memory := resources["memory"].(map[string]any)
	if memory["limit"] != float64(256<<20) || memory["swap"] != float64(256<<20) || resources["pids"].(map[string]any)["limit"] != float64(64) {
		t.Fatalf("resources=%v", resources)
	}
	mounts := document["mounts"].([]any)
	foundBroker, foundWorkspace := false, false
	for _, raw := range mounts {
		mount := raw.(map[string]any)
		if mount["destination"] == "/run/pysolate" && mount["source"] == channel {
			foundBroker = true
		}
		if mount["destination"] == "/workspace" && mount["source"] == workspace {
			foundWorkspace = true
		}
	}
	if !foundBroker || !foundWorkspace {
		t.Fatalf("required mounts missing: %v", mounts)
	}
}

func TestWriteOCIBundleRejectsUnboundedResources(t *testing.T) {
	if err := writeOCIBundle(t.TempDir(), t.TempDir(), t.TempDir(), "", "container", nil, 0, 0); err == nil {
		t.Fatal("unbounded OCI resources accepted")
	}
}
