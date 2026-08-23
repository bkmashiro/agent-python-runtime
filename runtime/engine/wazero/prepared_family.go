package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sort"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

// PreparedFamilyMode selects the Host-side physical preparation strategy.
type PreparedFamilyMode string

// PreparedPhysicalDisposition records the strategy actually used for a member.
type PreparedPhysicalDisposition string

const (
	PreparedFamilyAuto        PreparedFamilyMode = "auto"
	PreparedFamilyPrivateCopy PreparedFamilyMode = "private_copy"
	PreparedFamilyPrivateCOW  PreparedFamilyMode = "private_cow"

	PreparedDispositionPrivateCopy   PreparedPhysicalDisposition = "private_copy"
	PreparedDispositionPrivateCOW    PreparedPhysicalDisposition = "private_cow"
	PreparedDispositionOrdinaryFresh PreparedPhysicalDisposition = "ordinary_fresh"
)

var (
	ErrPreparedFamilyConfig        = errors.New("invalid prepared family configuration")
	ErrPreparedFamilyDrift         = errors.New("prepared family runner is incompatible with its image")
	ErrPreparedFamilyIdentityReuse = errors.New("prepared family member identity is already bound")
	ErrPreparedFamilyBrokerReuse   = errors.New("prepared family Broker was reused across members")
)

// PreparedFamilyConfig binds an authority-free image config and finite member bounds.
type PreparedFamilyConfig struct {
	ImageConfig  runtimeconfig.RunConfig
	MaxConsumers uint32
	MaxActive    uint32
	Mode         PreparedFamilyMode
}

// PreparedMemberRecord is the body-free terminal join between a family and one Run.
type PreparedMemberRecord struct {
	SchemaVersion        string                      `json:"schema_version"`
	FamilySHA256         string                      `json:"family_sha256"`
	InputSHA256          string                      `json:"input_sha256"`
	MemberID             uint64                      `json:"member_id"`
	RunID                string                      `json:"run_id,omitempty"`
	InvocationID         string                      `json:"invocation_id"`
	ExecutionID          string                      `json:"execution_id"`
	PlanSHA256           string                      `json:"plan_sha256,omitempty"`
	GrantsSHA256         string                      `json:"grants_sha256"`
	PhysicalDisposition  PreparedPhysicalDisposition `json:"physical_disposition"`
	Outcome              PreparedMemberDisposition   `json:"outcome"`
	FinalWorkspaceSHA256 string                      `json:"final_workspace_sha256,omitempty"`
}

// Validate rejects incomplete or body-bearing-by-extension terminal identities.
func (record PreparedMemberRecord) Validate() error {
	if record.SchemaVersion != "pysolate.prepared-family-member.v1" ||
		!validPreparedDigest(record.FamilySHA256) || !validPreparedDigest(record.InputSHA256) ||
		record.MemberID == 0 || record.InvocationID == "" || record.ExecutionID == "" || !validPreparedDigest(record.GrantsSHA256) ||
		(record.PhysicalDisposition != PreparedDispositionPrivateCopy && record.PhysicalDisposition != PreparedDispositionPrivateCOW && record.PhysicalDisposition != PreparedDispositionOrdinaryFresh) {
		return ErrPreparedFamilyConfig
	}
	switch record.Outcome {
	case PreparedMemberOK, PreparedMemberGuestError, PreparedMemberCancelled, PreparedMemberTimeout:
		if record.RunID == "" {
			return ErrPreparedFamilyConfig
		}
	case PreparedMemberClosedUnrun:
		if record.RunID != "" {
			return ErrPreparedFamilyConfig
		}
	default:
		return ErrPreparedFamilyConfig
	}
	if record.FinalWorkspaceSHA256 != "" && !validPreparedDigest(record.FinalWorkspaceSHA256) {
		return ErrPreparedFamilyConfig
	}
	if record.PlanSHA256 != "" && !validPreparedDigest(record.PlanSHA256) {
		return ErrPreparedFamilyConfig
	}
	return nil
}

// PreparedFamily owns one immutable input image and creates bounded single-use runners.
// It does not schedule, retry, select, or publish member work.
type PreparedFamily struct {
	closeMu       sync.Mutex
	closeComplete bool
	mu            sync.Mutex
	wasm          []byte
	imageConfig   runtimeconfig.RunConfig
	input         PreparedNumpyInput
	identity      string
	disposition   PreparedPhysicalDisposition
	lifecycle     *preparedFamilyLifecycle
	parent        *Engine
	runners       map[uint64]*preparedFamilyRunner
	invocations   map[uint64]runtimeconfig.InvocationRef
	records       map[uint64]PreparedMemberRecord
	invocationIDs map[string]struct{}
	executionIDs  map[string]struct{}
	workspaceRefs map[string]struct{}
	brokers       map[*capability.Broker]struct{}
	closed        bool
}

// PrepareNumpyFamily seals one bounded ndarray input for later fresh consumers.
func PrepareNumpyFamily(ctx context.Context, wasm []byte, config PreparedFamilyConfig, input PreparedNumpyInput) (*PreparedFamily, error) {
	if ctx == nil || len(wasm) < 8 || config.ImageConfig.Validate() != nil ||
		config.ImageConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect || len(config.ImageConfig.CapabilityGrants) != 0 || config.ImageConfig.ColdIO != nil ||
		(config.Mode != PreparedFamilyAuto && config.Mode != PreparedFamilyPrivateCopy && config.Mode != PreparedFamilyPrivateCOW) {
		return nil, ErrPreparedFamilyConfig
	}
	lifecycle, err := newPreparedFamilyLifecycle(config.MaxConsumers, config.MaxActive)
	if err != nil || input.validateForConfig(config.ImageConfig) != nil {
		return nil, ErrPreparedFamilyConfig
	}
	imageConfig := cloneFamilyRunConfig(config.ImageConfig)
	imageConfig.Mechanisms = runtimeconfig.MechanismSet{}
	disposition := PreparedDispositionPrivateCopy
	if config.Mode == PreparedFamilyPrivateCOW || (config.Mode == PreparedFamilyAuto && runtime.GOOS == "linux") {
		disposition = PreparedDispositionPrivateCOW
		imageConfig.Mechanisms.PreparedRuntime = true
		imageConfig.Mechanisms.MemoryCOW = true
	}
	identity, err := preparedImageIdentity(imageConfig, input, PreparedNumpyABIV1)
	if err != nil {
		return nil, err
	}
	family := &PreparedFamily{
		wasm: append([]byte(nil), wasm...), imageConfig: imageConfig, input: input, identity: identity,
		disposition: disposition, lifecycle: lifecycle, runners: make(map[uint64]*preparedFamilyRunner),
		invocations: make(map[uint64]runtimeconfig.InvocationRef), records: make(map[uint64]PreparedMemberRecord),
		invocationIDs: make(map[string]struct{}), executionIDs: make(map[string]struct{}), workspaceRefs: make(map[string]struct{}), brokers: make(map[*capability.Broker]struct{}),
	}
	if disposition == PreparedDispositionPrivateCOW {
		parent, err := New(ctx, family.wasm, imageConfig)
		if err != nil {
			return nil, err
		}
		if err := parent.PrepareNumpyCOWInput(ctx, input); err != nil {
			closeErr := parent.Close(context.Background())
			if config.Mode == PreparedFamilyAuto && errors.Is(err, ErrCOWIneligible) && closeErr == nil {
				family.disposition = PreparedDispositionPrivateCopy
				family.imageConfig.Mechanisms.PreparedRuntime = false
				family.imageConfig.Mechanisms.MemoryCOW = false
				return family, nil
			}
			return nil, errors.Join(err, closeErr)
		}
		family.parent = parent
		family.input.body = nil
	}
	return family, nil
}

// State returns a detached body-free lifecycle snapshot.
func (family *PreparedFamily) State() PreparedFamilyState {
	if family == nil || family.lifecycle == nil {
		return PreparedFamilyState{}
	}
	state := family.lifecycle.state()
	family.mu.Lock()
	state.FamilySHA256 = family.identity
	state.InputSHA256 = family.input.identity
	state.Disposition = family.disposition
	state.Closed = family.closeComplete
	family.mu.Unlock()
	return state
}

// Records returns detached terminal records ordered by member ID.
func (family *PreparedFamily) Records() []PreparedMemberRecord {
	if family == nil {
		return nil
	}
	family.mu.Lock()
	defer family.mu.Unlock()
	ids := make([]uint64, 0, len(family.records))
	for id := range family.records {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	records := make([]PreparedMemberRecord, 0, len(ids))
	for _, id := range ids {
		records = append(records, family.records[id])
	}
	return records
}

func validPreparedDigest(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	for _, character := range value[7:] {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

func decodeWorkspaceRoot(response []byte) string {
	var envelope struct {
		WorkspaceReceipt *struct {
			FinalWorkspaceSHA256 string `json:"final_workspace_sha256"`
		} `json:"workspace_receipt"`
	}
	if json.Unmarshal(response, &envelope) == nil && envelope.WorkspaceReceipt != nil {
		return envelope.WorkspaceReceipt.FinalWorkspaceSHA256
	}
	return ""
}
