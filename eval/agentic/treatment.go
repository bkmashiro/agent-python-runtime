package agentic

import (
	"errors"
	"os"
)

var ErrDevelopmentTreatment = errors.New("invalid agentic development treatment")

const (
	TreatmentBaselineV1              = "baseline-v1"
	TreatmentStructuredHostContextV1 = "structured-host-context-v1"
	TreatmentPythonSafeRepairV1      = "python-safe-repair-v1"
	TreatmentHybridTwoStageRouterV1  = "hybrid-two-stage-router-v1"
)

const (
	baselineTreatmentJSON              = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"baseline-v1","host_context_policy":"none","python_repair_policy":"none","hybrid_strategy":"combined-surface-v1"}`
	structuredHostContextTreatmentJSON = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"structured-host-context-v1","host_context_policy":"prior-successful-effects","python_repair_policy":"none","hybrid_strategy":"combined-surface-v1"}`
	pythonSafeRepairTreatmentJSON      = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"python-safe-repair-v1","host_context_policy":"none","python_repair_policy":"one-zero-host-call","hybrid_strategy":"combined-surface-v1"}`
	hybridTwoStageRouterTreatmentJSON  = `{"schema_version":"agentic-development-treatment/v1","status":"frozen","id":"hybrid-two-stage-router-v1","host_context_policy":"none","python_repair_policy":"none","hybrid_strategy":"two-stage-v1"}`
)

type DevelopmentTreatment struct {
	SchemaVersion      string `json:"schema_version"`
	Status             string `json:"status"`
	ID                 string `json:"id"`
	HostContextPolicy  string `json:"host_context_policy"`
	PythonRepairPolicy string `json:"python_repair_policy"`
	HybridStrategy     string `json:"hybrid_strategy"`
	Digest             string `json:"-"`
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
	if treatment.SchemaVersion != "agentic-development-treatment/v1" || treatment.Status != "frozen" {
		return false
	}
	switch treatment.ID {
	case TreatmentBaselineV1:
		return treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "combined-surface-v1"
	case TreatmentStructuredHostContextV1:
		return treatment.HostContextPolicy == "prior-successful-effects" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "combined-surface-v1"
	case TreatmentPythonSafeRepairV1:
		return treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "one-zero-host-call" && treatment.HybridStrategy == "combined-surface-v1"
	case TreatmentHybridTwoStageRouterV1:
		return treatment.HostContextPolicy == "none" && treatment.PythonRepairPolicy == "none" && treatment.HybridStrategy == "two-stage-v1"
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
	default:
		return ""
	}
}

func (treatment DevelopmentTreatment) Implemented() bool {
	return treatment.valid() && treatment.ID == TreatmentBaselineV1
}
