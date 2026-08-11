package placement

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

var ErrGuestBundle = errors.New("invalid placement guest bundle")

const maxGuestArtifactBytes int64 = 1 << 30

type GuestIdentityExpectation struct {
	ArtifactSHA256 string
	ManifestSHA256 string
}

type GuestBundle struct {
	WASM     []byte
	Profile  runtimeconfig.ExecutionProfile
	Identity runtimeconfig.VerifiedArtifactIdentity
}

// LoadGuestBundle verifies the artifact, manifest, inventory and qualification
// sidecars before binding the Host-owned base profile. It follows no symlinks and
// accepts no caller-selected filenames.
func LoadGuestBundle(directory string, allowedImports []string, expected GuestIdentityExpectation) (GuestBundle, error) {
	if directory == "" || !validDigest(expected.ArtifactSHA256) || !validDigest(expected.ManifestSHA256) {
		return GuestBundle{}, ErrGuestBundle
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return GuestBundle{}, ErrGuestBundle
	}
	artifact, err := readRegularBounded(directory, "agent-python-runtime.wasm", maxGuestArtifactBytes)
	if err != nil {
		return GuestBundle{}, ErrGuestBundle
	}
	manifest, err := readRegularBounded(directory, "manifest.json", 8<<20)
	if err != nil || digestBytes(manifest) != expected.ManifestSHA256 {
		return GuestBundle{}, ErrGuestBundle
	}
	inventory, err := readRegularBounded(directory, "import-inventory.json", 8<<20)
	if err != nil {
		return GuestBundle{}, ErrGuestBundle
	}
	qualification, err := readRegularBounded(directory, "import-qualification.json", 8<<20)
	if err != nil {
		return GuestBundle{}, ErrGuestBundle
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime.wasm", artifact, manifest, inventory, qualification)
	if err != nil || identity.ArtifactSHA256 != expected.ArtifactSHA256 || identity.ManifestSHA256 != expected.ManifestSHA256 || identity.ProfileID != "base" {
		return GuestBundle{}, ErrGuestBundle
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		return GuestBundle{}, ErrGuestBundle
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	if err != nil {
		return GuestBundle{}, ErrGuestBundle
	}
	return GuestBundle{WASM: artifact, Profile: profile, Identity: identity}, nil
}

func readRegularBounded(directory, name string, limit int64) ([]byte, error) {
	path := filepath.Join(directory, name)
	if filepath.Dir(path) != filepath.Clean(directory) {
		return nil, ErrGuestBundle
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return nil, ErrGuestBundle
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) != info.Size() {
		return nil, ErrGuestBundle
	}
	return data, nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
