package agentfunction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	ErrInvalidGuestCompute = errors.New("invalid fresh Guest compute")
	ErrGuestResultTooLarge = errors.New("fresh Guest result exceeds bound")
	ErrGuestNotShareable   = errors.New("fresh Guest execution profile is not shareable")
	ErrGuestIdentity       = errors.New("fresh Guest request does not match invocation identity")
	ErrGuestRetention      = errors.New("fresh Guest completed-result retention is unsupported")
	ErrGuestQualification  = errors.New("fresh Guest semantic qualification is invalid")
)

type GuestRunnerFactory func(context.Context) (physicalExecutionID string, runner enginecontract.Runner, err error)
type GuestResultDecoder func([]byte) ([]byte, error)

// FreshGuestCompute adapts an agent-function leader to one single-use Runner.
// The Runner and its Guest state are never shared; only copied result bytes are.
type FreshGuestCompute struct {
	NewRunner      GuestRunnerFactory
	Request        []byte
	TrustedPrepare string
	DecodeResult   GuestResultDecoder
	MaxResultBytes uint64
}

func (compute FreshGuestCompute) Run(ctx context.Context, guard *Guard) ([]byte, error) {
	if compute.NewRunner == nil || compute.DecodeResult == nil || len(compute.Request) == 0 || compute.MaxResultBytes == 0 {
		return nil, ErrInvalidGuestCompute
	}
	physicalID, runner, err := compute.NewRunner(ctx)
	if err != nil || runner == nil {
		if runner != nil {
			_ = runner.Close(context.Background())
		}
		return nil, errors.Join(ErrInvalidGuestCompute, err)
	}
	if err := guard.BindPhysicalExecution(physicalID); err != nil {
		_ = runner.Close(context.Background())
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = runner.Close(context.Background())
		}
	}()
	runContext, effects := enginecontract.WithEffectProbe(ctx)
	payload, runErr := runner.Run(runContext, append([]byte(nil), compute.Request...), compute.TrustedPrepare)
	if runErr == nil && ctx.Err() != nil {
		runErr = ctx.Err()
	}
	closeErr := runner.Close(context.Background())
	closed = true
	if effects.HostCallAttempted() {
		return nil, errors.Join(ErrGuestNotShareable, runErr, closeErr)
	}
	if runErr != nil || closeErr != nil {
		return nil, errors.Join(runErr, closeErr)
	}
	value, err := compute.DecodeResult(payload)
	if err != nil {
		return nil, err
	}
	if uint64(len(value)) > compute.MaxResultBytes {
		return nil, ErrGuestResultTooLarge
	}
	return append([]byte(nil), value...), nil
}

func decodeQualifiedGuestResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &response); err != nil || response.Status != "ok" ||
		len(response.Result) == 0 || !canonicalJSON(response.Result) {
		return nil, ErrInvalidGuestCompute
	}
	return append([]byte(nil), response.Result...), nil
}

// GuestRequestContractSHA256 binds the exact untrusted compatibility and
// requirements declarations that must be admitted before a retained hit.
func GuestRequestContractSHA256(raw []byte) (string, error) {
	request, err := runtimeconfig.DecodeRunRequest(raw)
	if err != nil || request.Compatibility == nil {
		return "", ErrGuestQualification
	}
	descriptor := struct {
		Compatibility runtimeconfig.CompatibilityDeclaration `json:"compatibility"`
		Requirements  []runtimeconfig.RequiredFeature        `json:"requirements"`
	}{
		Compatibility: *request.Compatibility,
		Requirements:  append([]runtimeconfig.RequiredFeature{}, request.Requirements...),
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return "", ErrGuestQualification
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

// ExecuteGuest binds the invocation identity to actual Runner properties before
// single-flight may publish an immutable Guest result; retention stays denied.
func (functionEngine Engine) ExecuteGuest(ctx context.Context, invocation Invocation, compute FreshGuestCompute) (Result, error) {
	return functionEngine.executeGuest(ctx, invocation, compute, false)
}

// ExecuteQualifiedGuest is the only completed-result retention path for a Fresh
// Guest. Only the opaque token constructor can bind all static qualification
// identities; runtime effect probes remain the publication backstop.
func (functionEngine Engine) ExecuteQualifiedGuest(ctx context.Context, qualified QualifiedGuestInvocation, compute FreshGuestCompute) (Result, error) {
	invocation := qualified.invocation
	if err := invocation.Validate(); err != nil ||
		invocation.SemanticAnalysisSHA256 == "" || invocation.SemanticPlanSHA256 == "" ||
		invocation.SemanticAnalyzerSHA256 == "" || invocation.SemanticRegionID == "" ||
		invocation.SemanticRequestContractSHA256 == "" {
		return Result{}, ErrGuestQualification
	}
	if compute.TrustedPrepare != "" {
		return Result{}, ErrGuestQualification
	}
	compute.DecodeResult = decodeQualifiedGuestResult
	result, err := functionEngine.executeGuest(ctx, invocation, compute, true)
	if err == nil && uint64(len(result.Value)) > compute.MaxResultBytes {
		return Result{}, ErrGuestResultTooLarge
	}
	return result, err
}

func (functionEngine Engine) executeGuest(ctx context.Context, invocation Invocation, compute FreshGuestCompute, allowRetention bool) (Result, error) {
	if compute.NewRunner == nil {
		return Result{}, ErrInvalidGuestCompute
	}
	if functionEngine.CacheEnabled && !allowRetention {
		return Result{}, ErrGuestRetention
	}
	if allowRetention {
		requestContract, contractErr := GuestRequestContractSHA256(compute.Request)
		if contractErr != nil || requestContract != invocation.SemanticRequestContractSHA256 {
			return Result{}, ErrGuestQualification
		}
	}
	request, err := runtimeconfig.DecodeRunRequest(compute.Request)
	if err != nil {
		return Result{}, ErrGuestIdentity
	}
	if allowRetention && request.Compatibility == nil {
		return Result{}, ErrGuestQualification
	}
	codeDigest := sha256.Sum256([]byte(request.Code))
	outputSchemaDigest := sha256.Sum256(request.OutputSchema)
	if fmt.Sprintf("sha256:%x", codeDigest[:]) != invocation.FunctionSourceSHA256 ||
		!bytes.Equal(request.Inputs, invocation.CanonicalInputs) ||
		fmt.Sprintf("sha256:%x", outputSchemaDigest[:]) != invocation.OutputSchemaSHA256 {
		return Result{}, ErrGuestIdentity
	}
	originalFactory := compute.NewRunner
	compute.NewRunner = func(runContext context.Context) (string, enginecontract.Runner, error) {
		physicalID, runner, err := originalFactory(runContext)
		if err != nil || runner == nil {
			return physicalID, runner, err
		}
		properties := runner.Properties()
		if properties.Backend != "wazero" || properties.ArtifactSHA256 != invocation.ArtifactSHA256 ||
			properties.ExecutionProfileBindingSHA256 != invocation.ExecutionProfileSHA256 ||
			properties.DeterministicProfileSHA256 != invocation.DeterministicSettingsSHA256 ||
			ImportClosureIdentity(properties.AvailableImports, properties.QualifiedImports) != invocation.ImportClosureSHA256 ||
			properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
			_ = runner.Close(context.Background())
			return "", nil, ErrGuestNotShareable
		}
		return physicalID, runner, nil
	}
	return functionEngine.execute(ctx, invocation, compute.Run, "fresh-guest")
}

// ImportClosureIdentity binds the exact artifact import inventory reported by
// the Runner. Sorting makes the identity independent of slice construction.
func ImportClosureIdentity(available, qualified []string) string {
	available = append([]string(nil), available...)
	qualified = append([]string(nil), qualified...)
	sort.Strings(available)
	sort.Strings(qualified)
	descriptor := append([]string{"pysolate.import-closure.v1", "available"}, available...)
	descriptor = append(descriptor, "qualified")
	descriptor = append(descriptor, qualified...)
	digest := sha256.Sum256([]byte(strings.Join(descriptor, "\x00")))
	return fmt.Sprintf("sha256:%x", digest[:])
}
