package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

type stagedPlaybackBundle struct {
	temporaryPath string
	outputPath    string
	identity      string
	published     bool
}

func executionProfileSHA256(config runtimeconfig.RunConfig) (string, error) {
	descriptor := struct {
		SchemaVersion  string   `json:"schema_version"`
		ID             string   `json:"id"`
		ArtifactSHA256 string   `json:"artifact_sha256"`
		ManifestSHA256 string   `json:"manifest_sha256"`
		AllowedImports []string `json:"allowed_imports"`
	}{SchemaVersion: "pysolate.execution-profile-binding.v1", ID: "none", AllowedImports: []string{}}
	if config.ExecutionProfile != nil {
		profile := *config.ExecutionProfile
		descriptor.ID = profile.ID()
		descriptor.ArtifactSHA256 = profile.ArtifactSHA256()
		descriptor.ManifestSHA256 = profile.ManifestSHA256()
		descriptor.AllowedImports = profile.AllowedImports()
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	return playback.CanonicalSHA256(encoded)
}

func stagePlaybackBundle(outputPath string, bundle playback.Bundle) (*stagedPlaybackBundle, error) {
	if outputPath == "" || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) != outputPath {
		return nil, errors.New("invalid playback bundle output path")
	}
	if _, err := os.Lstat(outputPath); err == nil || !os.IsNotExist(err) {
		return nil, errors.New("playback bundle output already exists")
	}
	encoded, err := playback.Encode(bundle)
	if err != nil {
		return nil, errors.New("encode playback bundle")
	}
	if _, err := playback.Decode(encoded); err != nil {
		return nil, errors.New("validate playback bundle")
	}
	directory := filepath.Dir(outputPath)
	file, err := os.CreateTemp(directory, "."+filepath.Base(outputPath)+".tmp-*")
	if err != nil {
		return nil, errors.New("stage playback bundle")
	}
	staged := &stagedPlaybackBundle{temporaryPath: file.Name(), outputPath: outputPath, identity: bundle.Identity}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
			_ = os.Remove(staged.temporaryPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.New("protect playback bundle")
	}
	if _, err := file.Write(encoded); err != nil {
		return nil, errors.New("write playback bundle")
	}
	if err := file.Sync(); err != nil {
		return nil, errors.New("sync playback bundle")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("close playback bundle")
	}
	failed = false
	return staged, nil
}

func (staged *stagedPlaybackBundle) publish() error {
	if staged == nil || staged.temporaryPath == "" || staged.outputPath == "" || staged.published {
		return errors.New("playback bundle stage is unavailable")
	}
	// A same-directory hard link publishes atomically without overwriting an
	// existing protected artifact. The staged name is removed only after the
	// directory entry is durable.
	if err := os.Link(staged.temporaryPath, staged.outputPath); err != nil {
		return errors.New("publish playback bundle")
	}
	if err := syncDirectory(filepath.Dir(staged.outputPath)); err != nil {
		_ = os.Remove(staged.outputPath)
		return errors.New("sync published playback bundle")
	}
	if err := os.Remove(staged.temporaryPath); err != nil {
		return errors.New("remove playback bundle stage")
	}
	if err := syncDirectory(filepath.Dir(staged.outputPath)); err != nil {
		return errors.New("sync playback bundle directory")
	}
	staged.published = true
	return nil
}

func (staged *stagedPlaybackBundle) discard() error {
	if staged == nil || staged.temporaryPath == "" || staged.published {
		return nil
	}
	err := os.Remove(staged.temporaryPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
