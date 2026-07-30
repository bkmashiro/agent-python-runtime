package hermesbridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	maxArtifactBytes = 256 << 20
	maxManifestBytes = 1 << 20
)

var hexDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type ArtifactProvenance struct {
	ArtifactSHA256   string `json:"artifact_sha256"`
	ManifestSHA256   string `json:"manifest_sha256"`
	RepositoryCommit string `json:"repository_commit"`
	ABIVersion       string `json:"abi_version"`
	Target           string `json:"target"`
}

type distributionManifest struct {
	SchemaVersion int    `json:"schema_version"`
	ABIVersion    string `json:"abi_version"`
	Target        string `json:"target"`
	Artifact      struct {
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
		Size     int64  `json:"size"`
	} `json:"artifact"`
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
	} `json:"build"`
}

func LoadPinnedArtifact(artifactPath, manifestPath string) ([]byte, ArtifactProvenance, error) {
	artifact, err := readPinnedRegularFile(artifactPath, maxArtifactBytes)
	if err != nil {
		return nil, ArtifactProvenance{}, err
	}
	manifestBytes, err := readPinnedRegularFile(manifestPath, maxManifestBytes)
	if err != nil {
		return nil, ArtifactProvenance{}, err
	}
	if rejectDuplicateJSON(manifestBytes) != nil {
		return nil, ArtifactProvenance{}, errors.New("distribution manifest contains duplicate JSON keys")
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	var manifest distributionManifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, ArtifactProvenance{}, errors.New("decode distribution manifest")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, ArtifactProvenance{}, errors.New("distribution manifest contains trailing JSON")
	}
	if manifest.SchemaVersion != 2 || manifest.ABIVersion != "v1" || manifest.Target != "wasm32-wasip1" ||
		manifest.Artifact.Filename != filepath.Base(artifactPath) || filepath.Base(manifest.Artifact.Filename) != manifest.Artifact.Filename ||
		manifest.Artifact.Size != int64(len(artifact)) || !hexDigestPattern.MatchString(manifest.Artifact.SHA256) ||
		!commitPattern.MatchString(manifest.Build.RepositoryCommit) {
		return nil, ArtifactProvenance{}, errors.New("invalid distribution manifest identity")
	}
	artifactSum := sha256.Sum256(artifact)
	if hex.EncodeToString(artifactSum[:]) != manifest.Artifact.SHA256 {
		return nil, ArtifactProvenance{}, errors.New("guest artifact digest mismatch")
	}
	manifestSum := sha256.Sum256(manifestBytes)
	return artifact, ArtifactProvenance{
		ArtifactSHA256:   "sha256:" + hex.EncodeToString(artifactSum[:]),
		ManifestSHA256:   "sha256:" + hex.EncodeToString(manifestSum[:]),
		RepositoryCommit: manifest.Build.RepositoryCommit, ABIVersion: manifest.ABIVersion, Target: manifest.Target,
	}, nil
}

func readPinnedRegularFile(path string, maximum int64) ([]byte, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("pinned path must be clean and absolute")
	}
	linkInfo, err := os.Lstat(path)
	if err != nil || !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 || linkInfo.Size() > maximum {
		return nil, errors.New("pinned path is not a bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) || openedInfo.Size() > maximum {
		return nil, errors.New("pinned file changed during open")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(payload)) > maximum || int64(len(payload)) != openedInfo.Size() {
		return nil, errors.New("read bounded pinned file")
	}
	return payload, nil
}
