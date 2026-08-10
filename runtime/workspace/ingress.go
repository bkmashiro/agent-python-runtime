package workspace

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	wazerosys "github.com/tetratelabs/wazero/sys"
)

// CreateFromDirectory validates and copies one trusted Host directory into a
// new workspace. The source path is never retained or exposed to the guest,
// and the copy occurs once at provisioning time rather than once per Run.
func (manager *Manager) CreateFromDirectory(source string, limits Limits) (Ref, error) {
	if manager == nil {
		return "", ErrWorkspaceClosed
	}
	if err := limits.validate(); err != nil {
		return "", err
	}
	if source == "" || !filepath.IsAbs(source) || filepath.Clean(source) != source {
		return "", ingressError("source root must be a clean absolute directory")
	}
	rootInfo, err := os.Lstat(source)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", ingressError("source root is unavailable or unsupported")
	}
	sourceRoot, err := os.OpenRoot(source)
	if err != nil {
		return "", ingressError("source root cannot be opened")
	}
	defer sourceRoot.Close()
	openedRoot, err := sourceRoot.Open(".")
	if err != nil {
		return "", ingressError("source root cannot be opened")
	}
	openedRootInfo, err := openedRoot.Stat()
	_ = openedRoot.Close()
	if err != nil || !sameSourceObject(rootInfo, openedRootInfo) {
		return "", ingressError("source root changed during provisioning")
	}
	rootDevice := wazerosys.NewStat_t(rootInfo).Dev

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", ErrWorkspaceClosed
	}
	ref, destination, err := manager.allocateRootLocked()
	if err != nil {
		return "", err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(destination)
		}
	}()
	usage := &ingressUsage{}
	if err := copySourceDirectory(sourceRoot, ".", destination, rootDevice, limits, usage); err != nil {
		return "", err
	}
	if _, err := scanOrdinaryTree(destination, limits); err != nil {
		return "", err
	}
	manager.entries[ref] = &entry{root: destination, limits: limits}
	failed = false
	return ref, nil
}

type ingressUsage struct {
	entries uint32
	bytes   uint64
}

func copySourceDirectory(source *os.Root, sourceName, destination string, rootDevice uint64, limits Limits, usage *ingressUsage) error {
	before, err := source.Lstat(sourceName)
	if err != nil || !before.IsDir() || wazerosys.NewStat_t(before).Dev != rootDevice {
		return ingressError("source directory changed or crosses a filesystem boundary")
	}
	directory, err := source.Open(sourceName)
	if err != nil {
		return ingressError("source directory cannot be opened")
	}
	defer directory.Close()
	openedInfo, err := directory.Stat()
	if err != nil || !openedInfo.IsDir() || !sameSourceObject(before, openedInfo) || wazerosys.NewStat_t(openedInfo).Dev != rootDevice {
		return ingressError("source directory changed or crosses a filesystem boundary")
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return ingressError("source directory cannot be listed")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, directoryEntry := range entries {
		name := directoryEntry.Name()
		relative := name
		if sourceName != "." {
			relative = path.Join(sourceName, name)
		}
		cleaned, err := cleanGuestPath(relative, limits.MaxDepth, false)
		if err != nil || cleaned != relative {
			return ingressError("source contains an invalid path")
		}
		info, err := source.Lstat(cleaned)
		if err != nil {
			return ingressError("source entry changed during provisioning")
		}
		stat := wazerosys.NewStat_t(info)
		mode := info.Mode()
		if stat.Dev != rootDevice {
			return ingressError("source crosses a filesystem boundary")
		}
		if mode&os.ModeSymlink != 0 || (!mode.IsDir() && !mode.IsRegular()) {
			return ingressError("source contains a non-ordinary entry")
		}
		if mode.IsRegular() && stat.Nlink != 1 {
			return ingressError("source contains a hard-linked file")
		}
		usage.entries++
		if usage.entries > limits.MaxFiles {
			return ingressError("source exceeds the entry limit")
		}
		destinationPath := filepath.Join(destination, filepath.FromSlash(cleaned))
		if mode.IsDir() {
			if err := os.Mkdir(destinationPath, 0o700); err != nil {
				return ingressError("workspace directory cannot be materialized")
			}
			if err := copySourceDirectory(source, cleaned, destination, rootDevice, limits, usage); err != nil {
				return err
			}
			if err := os.Chmod(destinationPath, 0o755); err != nil {
				return ingressError("workspace directory mode cannot be canonicalized")
			}
			continue
		}
		if info.Size() < 0 || uint64(info.Size()) > limits.MaxFileBytes || usage.bytes > limits.MaxBytes-uint64(info.Size()) {
			return ingressError("source exceeds the byte limit")
		}
		if err := copySourceFile(source, cleaned, destinationPath, info); err != nil {
			return err
		}
		usage.bytes += uint64(info.Size())
	}
	after, err := directory.Stat()
	if err != nil || !sameSourceObject(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return ingressError("source directory changed during provisioning")
	}
	return nil
}

func copySourceFile(source *os.Root, sourceName, destination string, before fs.FileInfo) error {
	input, err := source.Open(sourceName)
	if err != nil {
		return ingressError("source file cannot be opened")
	}
	defer input.Close()
	openedInfo, err := input.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !sameSourceObject(before, openedInfo) {
		return ingressError("source file changed during provisioning")
	}
	mode := fs.FileMode(0o600)
	canonicalMode := fs.FileMode(0o644)
	if before.Mode().Perm()&0o111 != 0 {
		mode = 0o700
		canonicalMode = 0o755
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return ingressError("workspace file cannot be materialized")
	}
	copied, copyErr := io.Copy(output, io.LimitReader(input, before.Size()+1))
	closeErr := output.Close()
	if copyErr != nil || closeErr != nil || copied != before.Size() {
		return ingressError("source file changed or could not be copied")
	}
	after, err := input.Stat()
	if err != nil || !sameSourceObject(before, after) || !before.ModTime().Equal(after.ModTime()) {
		return ingressError("source file changed during provisioning")
	}
	if err := os.Chmod(destination, canonicalMode); err != nil {
		return ingressError("workspace file mode cannot be canonicalized")
	}
	return nil
}

func sameSourceObject(left, right fs.FileInfo) bool {
	if left == nil || right == nil || left.Mode().Type() != right.Mode().Type() || left.Size() != right.Size() {
		return false
	}
	leftStat := wazerosys.NewStat_t(left)
	rightStat := wazerosys.NewStat_t(right)
	return leftStat.Dev == rightStat.Dev && leftStat.Ino == rightStat.Ino && leftStat.Nlink == rightStat.Nlink
}

func ingressError(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidWorkspace, reason)
}
