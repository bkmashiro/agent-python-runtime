package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"runtime/debug"
	"strings"
)

func binarySourceIdentityFromSettings(settings []debug.BuildSetting) (hostSourceIdentity, error) {
	values := make(map[string]string, 2)
	for _, setting := range settings {
		if setting.Key != "vcs.revision" && setting.Key != "vcs.modified" {
			continue
		}
		if _, exists := values[setting.Key]; exists {
			return hostSourceIdentity{}, errors.New("duplicate binary VCS build setting")
		}
		values[setting.Key] = setting.Value
	}
	revision, revisionOK := values["vcs.revision"]
	modified, modifiedOK := values["vcs.modified"]
	decoded, decodeErr := hex.DecodeString(revision)
	if !revisionOK || !modifiedOK || len(revision) != 40 || len(decoded) != 20 || decodeErr != nil || strings.ToLower(revision) != revision {
		return hostSourceIdentity{}, errors.New("binary VCS identity is unavailable or invalid")
	}
	if modified != "false" {
		return hostSourceIdentity{}, errors.New("binary VCS identity is modified")
	}
	return hostSourceIdentity{Revision: revision, Modified: false}, nil
}

func currentBinarySourceIdentity() (hostSourceIdentity, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return hostSourceIdentity{}, errors.New("Go build information is unavailable")
	}
	return binarySourceIdentityFromSettings(info.Settings)
}

func runBinarySourceIdentityMain(options benchmarkOptions) error {
	if options.ArtifactPath != "" || options.ManifestPath != "" || options.InputPath != "" || options.SchemaPath != "" || options.OutputPath != "" || options.LifecycleDensityChild {
		return errors.New("binary-source-identity accepts no input or output paths")
	}
	identity, err := currentBinarySourceIdentity()
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(identity)
}
