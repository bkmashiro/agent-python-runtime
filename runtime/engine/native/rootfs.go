package native

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var ErrInvalidRootFS = errors.New("invalid native rootfs")

const (
	maxRootFSEntries = 200000
	maxRootFSBytes   = int64(2 << 30)
)

func VerifyOCIImageConfig(path, expectedDigest string) error {
	if !filepath.IsAbs(path) || !strings.HasPrefix(expectedDigest, "sha256:") {
		return ErrInvalidRootFS
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return fmt.Errorf("%w: image config metadata", ErrInvalidRootFS)
	}
	content, err := os.ReadFile(path)
	if err != nil || !json.Valid(content) {
		return fmt.Errorf("%w: image config JSON", ErrInvalidRootFS)
	}
	digest := sha256.Sum256(content)
	if "sha256:"+hex.EncodeToString(digest[:]) != expectedDigest {
		return fmt.Errorf("%w: image config digest", ErrInvalidRootFS)
	}
	return nil
}

func RootFSIdentity(root string) (string, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("%w: absolute path", ErrInvalidRootFS)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("%w: root directory", ErrInvalidRootFS)
	}
	hash := sha256.New()
	_, _ = io.WriteString(hash, "pysolate.native-rootfs.v1\x00")
	entries := 0
	var total int64
	err = filepath.WalkDir(absolute, func(path string, _ fs.DirEntry, walkErr error) error {
		relative, _ := filepath.Rel(absolute, path)
		if walkErr != nil {
			return fmt.Errorf("%w: walk %s", ErrInvalidRootFS, filepath.ToSlash(relative))
		}
		if path == absolute {
			return nil
		}
		entries++
		if entries > maxRootFSEntries {
			return fmt.Errorf("%w: entry bound", ErrInvalidRootFS)
		}
		if relative == "." || relative == "" {
			return fmt.Errorf("%w: relative path", ErrInvalidRootFS)
		}
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("%w: lstat %s", ErrInvalidRootFS, filepath.ToSlash(relative))
		}
		typeName, linkTarget := "", ""
		switch {
		case info.Mode().IsDir():
			typeName = "directory"
		case info.Mode().IsRegular():
			typeName = "file"
		case info.Mode()&os.ModeSymlink != 0:
			typeName = "symlink"
			linkTarget, err = os.Readlink(path)
			if err != nil {
				return fmt.Errorf("%w: readlink %s", ErrInvalidRootFS, filepath.ToSlash(relative))
			}
		default:
			return fmt.Errorf("%w: special node %s", ErrInvalidRootFS, filepath.ToSlash(relative))
		}
		header, err := json.Marshal(struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Mode uint32 `json:"mode"`
			Size int64  `json:"size"`
			Link string `json:"link,omitempty"`
		}{filepath.ToSlash(relative), typeName, uint32(info.Mode().Perm()), info.Size(), linkTarget})
		if err != nil {
			return fmt.Errorf("%w: header %s", ErrInvalidRootFS, filepath.ToSlash(relative))
		}
		hash.Write(header)
		hash.Write([]byte{0})
		if typeName == "file" {
			total += info.Size()
			if total > maxRootFSBytes {
				return fmt.Errorf("%w: byte bound", ErrInvalidRootFS)
			}
			file, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("%w: open %s", ErrInvalidRootFS, filepath.ToSlash(relative))
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil || closeErr != nil {
				return fmt.Errorf("%w: read %s", ErrInvalidRootFS, filepath.ToSlash(relative))
			}
			hash.Write([]byte{0})
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if entries == 0 {
		return "", fmt.Errorf("%w: empty", ErrInvalidRootFS)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
