package lifecycle

import (
	"errors"
	"regexp"
)

const SchemaVersion = "pysolate.lifecycle-evidence.v1"

var (
	ErrInvalidEvidence = errors.New("invalid lifecycle evidence")
	identifier         = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	digest             = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Phase struct {
	Name            string `json:"name"`
	WallNanoseconds int64  `json:"wall_nanoseconds"`
}

type Resources struct {
	UserCPUNanoseconds    int64  `json:"user_cpu_nanoseconds"`
	SystemCPUNanoseconds  int64  `json:"system_cpu_nanoseconds"`
	MaxRSSBytes           int64  `json:"max_rss_bytes"`
	CgroupMemoryPeakBytes uint64 `json:"cgroup_memory_peak_bytes"`
	PSSPeakBytes          uint64 `json:"pss_peak_bytes"`
	PrivateDirtyPeakBytes uint64 `json:"private_dirty_peak_bytes"`
	ReadBytes             uint64 `json:"read_bytes"`
	WriteBytes            uint64 `json:"write_bytes"`
	PidsPeak              uint64 `json:"pids_peak"`
	Samples               uint64 `json:"samples"`
}

type Cleanup struct {
	Process        bool `json:"process"`
	Socket         bool `json:"socket"`
	Mount          bool `json:"mount"`
	Cgroup         bool `json:"cgroup"`
	WorkspaceLease bool `json:"workspace_lease"`
}

type Evidence struct {
	SchemaVersion      string    `json:"schema_version"`
	ExecutionID        string    `json:"execution_id"`
	Backend            string    `json:"backend"`
	ArtifactIdentity   string    `json:"artifact_identity"`
	LogicalExecutions  uint32    `json:"logical_executions"`
	PhysicalExecutions uint32    `json:"physical_executions"`
	Phases             []Phase   `json:"phases"`
	Resources          Resources `json:"resources"`
	Cleanup            Cleanup   `json:"cleanup"`
	TerminalStatus     string    `json:"terminal_status"`
}

func (e Evidence) Validate() error {
	if e.SchemaVersion != SchemaVersion || !identifier.MatchString(e.ExecutionID) || (e.Backend != "wazero" && e.Backend != "native_sandbox") || !digest.MatchString(e.ArtifactIdentity) || e.LogicalExecutions == 0 || e.PhysicalExecutions == 0 || (e.TerminalStatus != "ok" && e.TerminalStatus != "error") {
		return ErrInvalidEvidence
	}
	previous := ""
	for _, phase := range e.Phases {
		if !identifier.MatchString(phase.Name) || phase.WallNanoseconds < 0 || phase.Name <= previous {
			return ErrInvalidEvidence
		}
		previous = phase.Name
	}
	if e.Resources.UserCPUNanoseconds < 0 || e.Resources.SystemCPUNanoseconds < 0 || e.Resources.MaxRSSBytes < 0 {
		return ErrInvalidEvidence
	}
	return nil
}
