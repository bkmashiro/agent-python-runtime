package agentic

import (
	"errors"
	"os"
)

var ErrDevelopmentTreatment = errors.New("invalid agentic development treatment")

const (
	TreatmentBaselineV1                          = "baseline-v1"
	TreatmentStructuredHostContextV1             = "structured-host-context-v1"
	TreatmentPythonSafeRepairV1                  = "python-safe-repair-v1"
	TreatmentHybridTwoStageRouterV1              = "hybrid-two-stage-router-v1"
	TreatmentHybridTwoStageSafeRepairV2          = "hybrid-two-stage-safe-repair-v2"
	TreatmentHybridTwoStagePreboundCompactV3     = "hybrid-two-stage-prebound-compact-v3"
	TreatmentHybridTwoStagePreboundCompactJSONV4 = "hybrid-two-stage-prebound-compact-json-v4"
)

const (
	baselineTreatmentJSON                          = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"baseline-v1","host_context_policy":"none","python_repair_policy":"none","hybrid_strategy":"combined-surface-v1","max_python_repairs_per_trial":0,"max_router_calls_per_hybrid_trial":0}`
	structuredHostContextTreatmentJSON             = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"structured-host-context-v1","host_context_policy":"prior-successful-effects","python_repair_policy":"none","hybrid_strategy":"combined-surface-v1","max_python_repairs_per_trial":0,"max_router_calls_per_hybrid_trial":0}`
	pythonSafeRepairTreatmentJSON                  = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"python-safe-repair-v1","host_context_policy":"none","python_repair_policy":"one-zero-host-call","hybrid_strategy":"combined-surface-v1","max_python_repairs_per_trial":1,"max_router_calls_per_hybrid_trial":0}`
	hybridTwoStageRouterTreatmentJSON              = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"hybrid-two-stage-router-v1","host_context_policy":"none","python_repair_policy":"none","hybrid_strategy":"two-stage-v1","max_python_repairs_per_trial":0,"max_router_calls_per_hybrid_trial":1}`
	hybridTwoStageSafeRepairTreatmentJSON          = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"hybrid-two-stage-safe-repair-v2","host_context_policy":"none","python_repair_policy":"one-zero-host-call","hybrid_strategy":"two-stage-v1","max_python_repairs_per_trial":1,"max_router_calls_per_hybrid_trial":1}`
	hybridTwoStagePreboundCompactTreatmentJSON     = `{"schema_version":"agentic-development-treatment/v2","status":"frozen","id":"hybrid-two-stage-prebound-compact-v3","host_context_policy":"none","python_repair_policy":"one-zero-host-call","hybrid_strategy":"two-stage-v1","python_binding_policy":"prebound-authorized-tools","python_result_policy":"default-empty-object","python_source_policy":"compact-no-unused-values","max_python_repairs_per_trial":1,"max_router_calls_per_hybrid_trial":1}`
	hybridTwoStagePreboundCompactJSONTreatmentJSON = `{"schema_version":"agentic-development-treatment/v3","status":"frozen","id":"hybrid-two-stage-prebound-compact-json-v4","host_context_policy":"none","python_repair_policy":"one-zero-host-call","hybrid_strategy":"two-stage-v1","python_binding_policy":"prebound-authorized-tools","python_result_policy":"default-empty-object-explicit-any-json","python_source_policy":"compact-no-unused-values","python_output_schema_policy":"any-json","max_python_repairs_per_trial":1,"max_router_calls_per_hybrid_trial":1}`
)

type DevelopmentTreatment struct {
	SchemaVersion                string `json:"schema_version"`
	Status                       string `json:"status"`
	ID                           string `json:"id"`
	HostContextPolicy            string `json:"host_context_policy"`
	PythonRepairPolicy           string `json:"python_repair_policy"`
	HybridStrategy               string `json:"hybrid_strategy"`
	PythonBindingPolicy          string `json:"python_binding_policy,omitempty"`
	PythonResultPolicy           string `json:"python_result_policy,omitempty"`
	PythonSourcePolicy           string `json:"python_source_policy,omitempty"`
	PythonOutputSchemaPolicy     string `json:"python_output_schema_policy,omitempty"`
	MaxPythonRepairsPerTrial     uint32 `json:"max_python_repairs_per_trial"`
	MaxRouterCallsPerHybridTrial uint32 `json:"max_router_calls_per_hybrid_trial"`
	Digest                       string `json:"-"`
}

func BaselineTreatment() DevelopmentTreatment {
	return DevelopmentTreatment{
		SchemaVersion: "agentic-development-treatment/v1", Status: "frozen", ID: TreatmentBaselineV1,
		HostContextPolicy: "none", PythonRepairPolicy: "none", HybridStrategy: "combined-surface-v1",
		Digest: digest([]byte(baselineTreatmentJSON)),
	}
}

func LoadDevelopmentTreatment(path string) (DevelopmentTreatment, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 8*1024 {
		return DevelopmentTreatment{}, ErrDevelopmentTreatment
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DevelopmentTreatment{}, ErrDevelopmentTreatment
	}
	var treatment DevelopmentTreatment
	if decodeStrict(data, &treatment) != nil || !treatment.validPolicy() {
		return DevelopmentTreatment{}, ErrDevelopmentTreatment
	}
	treatment.Digest = digest(data)
	if !treatment.valid() {
		return DevelopmentTreatment{}, ErrDevelopmentTreatment
	}
	return treatment, nil
}

func (treatment DevelopmentTreatment) validPolicy() bool {
	if treatment.Status != "frozen" {
		return false
	}
	legacy := treatment.SchemaVersion == "agentic-development-treatment/v1" && treatment.PythonBindingPolicy == "" && treatment.PythonResultPolicy == "" && treatment.PythonSourcePolicy == "" && treatment.PythonOutputSchemaPolicy == ""
	switch treatment.ID {
	case TreatmentBaselineV1:
		return legacy && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "combined-surface-v1" && treatment.MaxPythonRepairsPerTrial == 0 && treatment.MaxRouterCallsPerHybridTrial == 0
	case TreatmentStructuredHostContextV1:
		return legacy && treatment.HostContextPolicy == "prior-successful-effects" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "combined-surface-v1" && treatment.MaxPythonRepairsPerTrial == 0 && treatment.MaxRouterCallsPerHybridTrial == 0
	case TreatmentPythonSafeRepairV1:
		return legacy && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.HybridStrategy == "combined-surface-v1" && treatment.MaxPythonRepairsPerTrial == 1 && treatment.MaxRouterCallsPerHybridTrial == 0
	case TreatmentHybridTwoStageRouterV1:
		return legacy && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "two-stage-v1" && treatment.MaxPythonRepairsPerTrial == 0 && treatment.MaxRouterCallsPerHybridTrial == 1
	case TreatmentHybridTwoStageSafeRepairV2:
		return legacy && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.HybridStrategy == "two-stage-v1" && treatment.MaxPythonRepairsPerTrial == 1 && treatment.MaxRouterCallsPerHybridTrial == 1
	case TreatmentHybridTwoStagePreboundCompactV3:
		return treatment.SchemaVersion == "agentic-development-treatment/v2" && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.HybridStrategy == "two-stage-v1" &&
			treatment.PythonBindingPolicy == "prebound-authorized-tools" && treatment.PythonResultPolicy == "default-empty-object" && treatment.PythonSourcePolicy == "compact-no-unused-values" && treatment.PythonOutputSchemaPolicy == "" &&
			treatment.MaxPythonRepairsPerTrial == 1 && treatment.MaxRouterCallsPerHybridTrial == 1
	case TreatmentHybridTwoStagePreboundCompactJSONV4:
		return treatment.SchemaVersion == "agentic-development-treatment/v3" && treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.HybridStrategy == "two-stage-v1" &&
			treatment.PythonBindingPolicy == "prebound-authorized-tools" && treatment.PythonResultPolicy == "default-empty-object-explicit-any-json" && treatment.PythonSourcePolicy == "compact-no-unused-values" && treatment.PythonOutputSchemaPolicy == "any-json" &&
			treatment.MaxPythonRepairsPerTrial == 1 && treatment.MaxRouterCallsPerHybridTrial == 1
	default:
		return false
	}
}

func (treatment DevelopmentTreatment) valid() bool {
	document := expectedTreatmentDocument(treatment.ID)
	return treatment.validPolicy() && document != "" && treatment.Digest == digest([]byte(document))
}

func expectedTreatmentDocument(id string) string {
	switch id {
	case TreatmentBaselineV1:
		return baselineTreatmentJSON
	case TreatmentStructuredHostContextV1:
		return structuredHostContextTreatmentJSON
	case TreatmentPythonSafeRepairV1:
		return pythonSafeRepairTreatmentJSON
	case TreatmentHybridTwoStageRouterV1:
		return hybridTwoStageRouterTreatmentJSON
	case TreatmentHybridTwoStageSafeRepairV2:
		return hybridTwoStageSafeRepairTreatmentJSON
	case TreatmentHybridTwoStagePreboundCompactV3:
		return hybridTwoStagePreboundCompactTreatmentJSON
	case TreatmentHybridTwoStagePreboundCompactJSONV4:
		return hybridTwoStagePreboundCompactJSONTreatmentJSON
	default:
		return ""
	}
}

func (treatment DevelopmentTreatment) Implemented() bool {
	return treatment.valid() && (treatment.ID == TreatmentBaselineV1 || treatment.ID == TreatmentStructuredHostContextV1 ||
		treatment.ID == TreatmentPythonSafeRepairV1 || treatment.ID == TreatmentHybridTwoStageRouterV1 || treatment.ID == TreatmentHybridTwoStageSafeRepairV2 ||
		treatment.ID == TreatmentHybridTwoStagePreboundCompactV3 || treatment.ID == TreatmentHybridTwoStagePreboundCompactJSONV4)
}

func (treatment DevelopmentTreatment) UsesStructuredHostContext() bool {
	return treatment.Implemented() && treatment.HostContextPolicy == "prior-successful-effects"
}

func (treatment DevelopmentTreatment) AllowsPythonRepair() bool {
	return treatment.Implemented() && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.MaxPythonRepairsPerTrial == 1
}

func (treatment DevelopmentTreatment) UsesTwoStageRouter() bool {
	return treatment.Implemented() && treatment.HybridStrategy == "two-stage-v1" && treatment.MaxRouterCallsPerHybridTrial == 1
}

func (treatment DevelopmentTreatment) UsesPreboundCompactPython() bool {
	return treatment.Implemented() && treatment.PythonBindingPolicy == "prebound-authorized-tools" && treatment.PythonSourcePolicy == "compact-no-unused-values" &&
		(treatment.PythonResultPolicy == "default-empty-object" || treatment.PythonResultPolicy == "default-empty-object-explicit-any-json")
}

func (treatment DevelopmentTreatment) AllowsAnyJSONPythonResult() bool {
	return treatment.Implemented() && treatment.PythonResultPolicy == "default-empty-object-explicit-any-json" && treatment.PythonOutputSchemaPolicy == "any-json"
}
