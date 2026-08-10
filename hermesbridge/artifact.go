package hermesbridge

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const (
	maxArtifactBytes = 256 << 20
	maxManifestBytes = 1 << 20
)

type ArtifactProvenance struct {
	ArtifactSHA256   string                          `json:"artifact_sha256"`
	ManifestSHA256   string                          `json:"manifest_sha256"`
	RepositoryCommit string                          `json:"repository_commit"`
	ABIVersion       string                          `json:"abi_version"`
	Target           string                          `json:"target"`
	ArtifactProfile  string                          `json:"artifact_profile"`
	Packages         []runtimeconfig.ArtifactPackage `json:"packages"`
	ImportRoots      []string                        `json:"import_roots,omitempty"`
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
	var inventoryBytes []byte
	inventoryFilename, required, err := runtimeconfig.DistributionImportInventoryFilename(manifestBytes)
	if err != nil {
		return nil, ArtifactProvenance{}, errors.New("inspect distribution artifact manifest")
	}
	if required {
		inventoryBytes, err = readPinnedRegularFile(filepath.Join(filepath.Dir(manifestPath), inventoryFilename), maxManifestBytes)
		if err != nil {
			return nil, ArtifactProvenance{}, err
		}
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), artifact, manifestBytes, inventoryBytes)
	if err != nil {
		return nil, ArtifactProvenance{}, errors.New("verify distribution artifact identity")
	}
	return artifact, ArtifactProvenance{
		ArtifactSHA256:   identity.ArtifactSHA256,
		ManifestSHA256:   identity.ManifestSHA256,
		RepositoryCommit: identity.RepositoryCommit,
		ABIVersion:       identity.ABIVersion,
		Target:           identity.Target,
		ArtifactProfile:  identity.ProfileID,
		Packages:         append([]runtimeconfig.ArtifactPackage(nil), identity.Packages...),
		ImportRoots:      append([]string(nil), identity.ImportRoots...),
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
