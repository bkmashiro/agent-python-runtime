package numpyproducer

import (
	"context"
	"reflect"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

// VerifiedExecution is an opaque Host-owned proof that the concrete Wazero
// execution used the admission-bound artifact/profile with neither workspace
// nor capability-broker authority. It is not instruction-level DBI evidence.
type VerifiedExecution struct {
	response   []byte
	properties enginecontract.Properties
}

func (execution VerifiedExecution) Response() ([]byte, error) {
	if len(execution.response) == 0 {
		return nil, ErrExecution
	}
	return append([]byte(nil), execution.response...), nil
}

// RunVerifiedExecution executes one fresh Guest through the concrete Wazero
// engine and mints a proof only when the authority and artifact/profile
// properties remain unchanged across the call.
func RunVerifiedExecution(ctx context.Context, engine *wazeroengine.Engine, request []byte, trustedPrepare string, admission Admission) (VerifiedExecution, error) {
	if engine == nil || admission.Validate() != nil {
		return VerifiedExecution{}, ErrExecution
	}
	before := engine.Properties()
	if !executionPropertiesMatch(before, admission) {
		return VerifiedExecution{}, ErrExecution
	}
	response, err := engine.Run(ctx, request, trustedPrepare)
	if err != nil {
		return VerifiedExecution{}, err
	}
	after := engine.Properties()
	if !reflect.DeepEqual(before, after) || !executionPropertiesMatch(after, admission) {
		return VerifiedExecution{}, ErrExecution
	}
	return VerifiedExecution{response: append([]byte(nil), response...), properties: before}, nil
}

func executionPropertiesMatch(properties enginecontract.Properties, admission Admission) bool {
	return properties.Validate() == nil && !properties.WorkspaceMounted && !properties.CapabilityBrokerAvailable &&
		properties.ExecutionProfileID == admission.ExecutionProfileID && properties.ArtifactSHA256 == admission.ArtifactSHA256 &&
		properties.ExecutionProfileBindingSHA256 == admission.ExecutionProfileSHA256
}
