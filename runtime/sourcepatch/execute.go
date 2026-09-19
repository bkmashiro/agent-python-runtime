package sourcepatch

import (
	"context"
	"sort"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

// Execution describes one execution, including a transform that fell back before
// Guest execution began. A failure after derived execution begins is returned.
type Execution struct {
	Payload   []byte
	Patch     Patch
	Applied   bool
	PassError error
}

type SourcePatchRunner interface {
	Run(context.Context, []byte, string) ([]byte, error)
	RunSourcePatchDerived(context.Context, []byte, Patch, passregistration.Registration) ([]byte, error)
}

type CapabilitySourcePatchRunner interface {
	Run(context.Context, []byte, string) ([]byte, error)
	RunCapabilitySourcePatchInline(context.Context, []byte, passregistration.Registration, string, []CapabilityProjection) (Execution, error)
}

func (pass PureScalarCSE) Execute(ctx context.Context, transformer Transformer, runner SourcePatchRunner, request []byte) (Execution, error) {
	return executeSourcePatch(ctx, pass.registration, pass.Transform, transformer, runner, request)
}

func (pass PureScalarFold) Execute(ctx context.Context, transformer Transformer, runner SourcePatchRunner, request []byte) (Execution, error) {
	return executeSourcePatch(ctx, pass.registration, pass.Transform, transformer, runner, request)
}

func executeSourcePatch(ctx context.Context, registration passregistration.Registration, transform func(context.Context, Transformer, string) (Patch, error), transformer Transformer, runner SourcePatchRunner, request []byte) (Execution, error) {
	if runner == nil || registration.IdentitySHA256() == "" {
		return Execution{}, ErrInvalidPatch
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return Execution{}, err
	}
	patch, passErr := transform(ctx, transformer, runRequest.Code)
	if passErr != nil || !patch.Applied() {
		payload, runErr := runner.Run(ctx, request, "")
		return Execution{Payload: payload, Patch: patch, PassError: passErr}, runErr
	}
	payload, runErr := runner.RunSourcePatchDerived(ctx, request, patch, registration)
	return Execution{Payload: payload, Patch: patch, Applied: runErr == nil}, runErr
}

// Execute lowers PLM inside the same Guest that executes the request. Calling
// ordinary Runner.Run instead leaves the program unchanged and does not prepare
// speculative work. There is no second registry or enablement flag here.
func (pass PLMCapabilityCalls) Execute(ctx context.Context, runner CapabilitySourcePatchRunner, request []byte, trustedPrepare string, projections []CapabilityProjection) (Execution, error) {
	if runner == nil || pass.registration.IdentitySHA256() == "" || len(projections) == 0 {
		return Execution{}, ErrInvalidPatch
	}
	return runner.RunCapabilitySourcePatchInline(ctx, request, pass.registration, trustedPrepare, projections)
}

// PLMCapabilityProjections derives the Guest-visible projection allowlist from
// the Host's sealed capability Plan. Handlers and grants remain on the Host.
func PLMCapabilityProjections(plan *capability.Plan) []CapabilityProjection {
	if plan == nil {
		return nil
	}
	projections := make([]CapabilityProjection, 0)
	for _, spec := range plan.Specs() {
		if spec.PLM == nil || spec.PLM.PrepareEffect == capability.PrepareNone || spec.Python == nil {
			continue
		}
		projections = append(projections, CapabilityProjection{
			Capability: spec.Name, Module: spec.Python.Module, Method: spec.Python.Method,
			Arguments: append([]string(nil), spec.Python.Arguments...), ResultField: spec.Python.ResultField,
		})
	}
	sort.Slice(projections, func(left, right int) bool {
		return projections[left].Module+"."+projections[left].Method < projections[right].Module+"."+projections[right].Method
	})
	return projections
}
