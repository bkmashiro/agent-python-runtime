package native

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const ociSpecVersion = "1.2.0"

func writeOCIBundle(bundleDir, rootfs, channelDir, workspaceSource, containerID string, environment []string, memoryLimit, pidsLimit int64) error {
	if bundleDir == "" || rootfs == "" || channelDir == "" || containerID == "" || memoryLimit < 64<<20 || pidsLimit < 8 {
		return ErrInvalidConfig
	}
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return err
	}
	type mount struct {
		Destination string   `json:"destination"`
		Type        string   `json:"type"`
		Source      string   `json:"source"`
		Options     []string `json:"options,omitempty"`
	}
	document := struct {
		OCIVersion string `json:"ociVersion"`
		Process    struct {
			Terminal bool `json:"terminal"`
			User     struct {
				UID uint32 `json:"uid"`
				GID uint32 `json:"gid"`
			} `json:"user"`
			Args            []string `json:"args"`
			Env             []string `json:"env"`
			Cwd             string   `json:"cwd"`
			NoNewPrivileges bool     `json:"noNewPrivileges"`
		} `json:"process"`
		Root struct {
			Path     string `json:"path"`
			Readonly bool   `json:"readonly"`
		} `json:"root"`
		Hostname string  `json:"hostname"`
		Mounts   []mount `json:"mounts"`
		Linux    struct {
			Namespaces []struct {
				Type string `json:"type"`
			} `json:"namespaces"`
			CgroupsPath string `json:"cgroupsPath"`
			Resources   struct {
				Memory struct {
					Limit int64 `json:"limit"`
					Swap  int64 `json:"swap"`
				} `json:"memory"`
				Pids struct {
					Limit int64 `json:"limit"`
				} `json:"pids"`
			} `json:"resources"`
			MaskedPaths   []string `json:"maskedPaths"`
			ReadonlyPaths []string `json:"readonlyPaths"`
		} `json:"linux"`
	}{OCIVersion: ociSpecVersion, Hostname: "pysolate-native"}
	document.Process.User.UID, document.Process.User.GID = 0, 0
	document.Process.Args = []string{"/usr/local/bin/python3", "/opt/pysolate/runner.py"}
	document.Process.Env = append([]string(nil), environment...)
	document.Process.Cwd = "/tmp"
	document.Process.NoNewPrivileges = true
	document.Root.Path, document.Root.Readonly = rootfs, true
	document.Mounts = []mount{
		{Destination: "/proc", Type: "proc", Source: "proc", Options: []string{"nosuid", "noexec", "nodev"}},
		{Destination: "/dev", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "strictatime", "mode=755", "size=65536k"}},
		{Destination: "/tmp", Type: "tmpfs", Source: "tmpfs", Options: []string{"nosuid", "nodev", "mode=1777", "size=67108864"}},
		{Destination: "/run/pysolate", Type: "bind", Source: channelDir, Options: []string{"rbind", "rw", "nosuid", "nodev", "noexec"}},
	}
	if workspaceSource != "" {
		document.Mounts = append(document.Mounts, mount{Destination: "/workspace", Type: "bind", Source: workspaceSource, Options: []string{"rbind", "rw", "nosuid", "nodev", "noexec"}})
	}
	for _, namespace := range []string{"pid", "network", "ipc", "uts", "mount"} {
		document.Linux.Namespaces = append(document.Linux.Namespaces, struct {
			Type string `json:"type"`
		}{namespace})
	}
	document.Linux.CgroupsPath = "/" + containerID
	document.Linux.Resources.Memory.Limit = memoryLimit
	document.Linux.Resources.Memory.Swap = memoryLimit
	document.Linux.Resources.Pids.Limit = pidsLimit
	document.Linux.MaskedPaths = []string{"/proc/acpi", "/proc/kcore", "/proc/keys", "/proc/latency_stats", "/proc/timer_list", "/proc/timer_stats", "/proc/sched_debug", "/sys/firmware"}
	document.Linux.ReadonlyPaths = []string{"/proc/asound", "/proc/bus", "/proc/fs", "/proc/irq", "/proc/sys", "/proc/sysrq-trigger"}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	path := filepath.Join(bundleDir, "config.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		return err
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		return errors.New("OCI config permissions are invalid")
	}
	return nil
}
