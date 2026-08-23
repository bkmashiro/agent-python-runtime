package wazero

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sort"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

type PreparedFamilyMode string

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
	ErrPreparedFamilyConfig = errors.New("invalid prepared family configuration")
	ErrPreparedFamilyDrift  = errors.New("prepared family runner is incompatible with its image")
)

type PreparedFamilyConfig struct {
	RunConfig    runtimeconfig.RunConfig
	MaxConsumers uint32
	MaxActive    uint32
	Mode         PreparedFamilyMode
}

type PreparedMemberRecord struct {
	SchemaVersion        string                      `json:"schema_version"`
	FamilySHA256         string                      `json:"family_sha256"`
	InputSHA256          string                      `json:"input_sha256"`
	MemberID             uint64                      `json:"member_id"`
	RunID                string                      `json:"run_id,omitempty"`
	InvocationID         string                      `json:"invocation_id"`
	ExecutionID          string                      `json:"execution_id"`
	PhysicalDisposition  PreparedPhysicalDisposition `json:"physical_disposition"`
	Outcome              PreparedMemberDisposition   `json:"outcome"`
	FinalWorkspaceSHA256 string                      `json:"final_workspace_sha256,omitempty"`
}

func (record PreparedMemberRecord) Validate() error {
	if record.SchemaVersion != "pysolate.prepared-family-member.v1" ||
		!validPreparedDigest(record.FamilySHA256) || !validPreparedDigest(record.InputSHA256) ||
		record.MemberID == 0 || record.InvocationID == "" || record.ExecutionID == "" ||
		(record.PhysicalDisposition != PreparedDispositionPrivateCopy && record.PhysicalDisposition != PreparedDispositionPrivateCOW && record.PhysicalDisposition != PreparedDispositionOrdinaryFresh) {
		return ErrPreparedFamilyConfig
	}
	switch record.Outcome {
	case PreparedMemberOK, PreparedMemberGuestError, PreparedMemberCancelled:
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
	return nil
}

type PreparedFamily struct {
	mu          sync.Mutex
	wasm        []byte
	imageConfig runtimeconfig.RunConfig
	input       PreparedNumpyInput
	identity    string
	disposition PreparedPhysicalDisposition
	lifecycle   *preparedFamilyLifecycle
	parent      *Engine
	runners     map[uint64]*preparedFamilyRunner
	invocations map[uint64]runtimeconfig.InvocationRef
	records     map[uint64]PreparedMemberRecord
	closed      bool
}

func PrepareNumpyFamily(ctx context.Context, wasm []byte, config PreparedFamilyConfig, input PreparedNumpyInput) (*PreparedFamily, error) {
	if ctx == nil || len(wasm) < 8 || config.RunConfig.Validate() != nil ||
		config.RunConfig.ProgramSurface != runtimeconfig.ProgramSurfaceDirect || len(config.RunConfig.CapabilityGrants) != 0 || config.RunConfig.ColdIO != nil ||
		(config.Mode != PreparedFamilyAuto && config.Mode != PreparedFamilyPrivateCopy && config.Mode != PreparedFamilyPrivateCOW) {
		return nil, ErrPreparedFamilyConfig
	}
	lifecycle, err := newPreparedFamilyLifecycle(config.MaxConsumers, config.MaxActive)
	if err != nil || input.validateForConfig(config.RunConfig) != nil {
		return nil, ErrPreparedFamilyConfig
	}
	imageConfig := cloneFamilyRunConfig(config.RunConfig)
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
	}
	if disposition == PreparedDispositionPrivateCOW {
		parent, err := New(ctx, family.wasm, imageConfig)
		if err != nil {
			return nil, err
		}
		if err := parent.PrepareNumpyCOWInput(ctx, input); err != nil {
			_ = parent.Close(context.Background())
			return nil, err
		}
		family.parent = parent
		family.input.body = nil
	}
	return family, nil
}

func (family *PreparedFamily) State() PreparedFamilyState {
	if family == nil || family.lifecycle == nil {
		return PreparedFamilyState{}
	}
	state := family.lifecycle.state()
	family.mu.Lock()
	state.FamilySHA256 = family.identity
	state.InputSHA256 = family.input.identity
	state.Disposition = family.disposition
	family.mu.Unlock()
	return state
}

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
