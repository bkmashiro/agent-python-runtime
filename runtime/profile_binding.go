package runtime

import (
	"encoding/json"

	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

// ExecutionProfileBindingSHA256 identifies the complete Host execution
// profile relation used by playback and observation. It remains meaningful for
// the legacy no-profile path (ID "none") and includes the optional bounded
// deterministic-verification profile when selected.
func ExecutionProfileBindingSHA256(config RunConfig) (string, error) {
	descriptor := struct {
		SchemaVersion              string   `json:"schema_version"`
		ID                         string   `json:"id"`
		ArtifactSHA256             string   `json:"artifact_sha256"`
		ManifestSHA256             string   `json:"manifest_sha256"`
		AllowedImports             []string `json:"allowed_imports"`
		DeterministicProfileSHA256 string   `json:"deterministic_profile_sha256,omitempty"`
	}{AllowedImports: []string{}, ID: "none", SchemaVersion: "pysolate.execution-profile-binding.v1"}
	if config.ExecutionProfile != nil {
		profile := *config.ExecutionProfile
		descriptor.ID = profile.ID()
		descriptor.ArtifactSHA256 = profile.ArtifactSHA256()
		descriptor.ManifestSHA256 = profile.ManifestSHA256()
		descriptor.AllowedImports = profile.AllowedImports()
	}
	if config.DeterministicVerification != nil {
		if config.DeterministicVerification.Validate() != nil {
			return "", ErrDeterministicVerificationAdmission
		}
		descriptor.DeterministicProfileSHA256 = config.DeterministicVerification.Identity()
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	return playback.CanonicalSHA256(encoded)
}
