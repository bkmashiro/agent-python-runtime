package workflowbench

import (
	"errors"
	"fmt"
	"regexp"
)

const EvidenceSuiteSchema = "pysolate.workflow-evidence-suite.v1"

// EvidenceLayer keeps compatibility, trace-derived causality, and mechanism
// stress evidence separate so one workload cannot silently support all claims.
type EvidenceLayer string

const (
	EvidenceNaturalCensus EvidenceLayer = "natural_opportunity_census"
	EvidenceTraceDAG      EvidenceLayer = "trace_derived_dag"
	EvidenceMechanism     EvidenceLayer = "mechanism_stress"
)

type WorkflowNodeKind string

type WorkflowEffect string

const (
	NodeModelTurn    WorkflowNodeKind = "model_turn"
	NodeToolCall     WorkflowNodeKind = "tool_call"
	NodeLocalCompute WorkflowNodeKind = "local_compute"
	NodeBarrier      WorkflowNodeKind = "effect_barrier"

	EffectNone  WorkflowEffect = "none"
	EffectPure  WorkflowEffect = "pure"
	EffectRead  WorkflowEffect = "external_read"
	EffectWrite WorkflowEffect = "external_write"
)

type WorkflowNode struct {
	ID               string           `json:"id"`
	Kind             WorkflowNodeKind `json:"kind"`
	Effect           WorkflowEffect   `json:"effect"`
	CapabilitySHA256 string           `json:"capability_sha256,omitempty"`
	ConcurrencySafe  bool             `json:"concurrency_safe"`
	LogicalAgent     string           `json:"logical_agent"`
	ImportShard      string           `json:"import_shard,omitempty"`
}

type WorkflowEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type WorkflowFactors struct {
	LogicalAgents   uint32 `json:"logical_agents"`
	PhysicalSlots   uint32 `json:"physical_slots"`
	CallsPerProgram uint32 `json:"calls_per_program"`
	LocalComputePct uint32 `json:"local_compute_pct"`
	PayloadClass    string `json:"payload_class"`
	FailureMode     string `json:"failure_mode"`
}

type WorkflowMetricContract struct {
	TaskOracle               bool `json:"task_oracle"`
	EffectReceiptEquivalence bool `json:"effect_receipt_equivalence"`
	AdmissionAndFallback     bool `json:"admission_and_fallback"`
	ModelTurns               bool `json:"model_turns"`
	ProviderCalls            bool `json:"provider_calls"`
	LogicalAndPhysicalCalls  bool `json:"logical_and_physical_calls"`
	WallAndTailLatency       bool `json:"wall_and_tail_latency"`
	CPUAndPeakRSS            bool `json:"cpu_and_peak_rss"`
	GuestStartsAndCompiles   bool `json:"guest_starts_and_compiles"`
}

type EvidenceWorkload struct {
	ID                         string                 `json:"id"`
	Layer                      EvidenceLayer          `json:"layer"`
	Origin                     string                 `json:"origin"`
	CaseSHA256                 string                 `json:"case_sha256"`
	OracleSHA256               string                 `json:"oracle_sha256"`
	LaneConfigSHA256           string                 `json:"lane_config_sha256"`
	OracleAuthority            string                 `json:"oracle_authority"`
	OracleFailClosed           bool                   `json:"oracle_fail_closed"`
	SourceTraceSHA256          string                 `json:"source_trace_sha256,omitempty"`
	RawBodiesRetainedPrivately bool                   `json:"raw_bodies_retained_privately"`
	OptimizationRequired       bool                   `json:"optimization_required"`
	TargetMechanisms           []string               `json:"target_mechanisms,omitempty"`
	Nodes                      []WorkflowNode         `json:"nodes"`
	Edges                      []WorkflowEdge         `json:"edges"`
	Factors                    WorkflowFactors        `json:"factors"`
	Lanes                      []string               `json:"lanes"`
	Metrics                    WorkflowMetricContract `json:"metrics"`
	ClaimBoundary              string                 `json:"claim_boundary"`
}

type EvidenceSuite struct {
	SchemaVersion string             `json:"schema_version"`
	SuiteID       string             `json:"suite_id"`
	Workloads     []EvidenceWorkload `json:"workloads"`
}

var evidenceID = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,95}$`)
var evidenceSHA256 = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (suite EvidenceSuite) Validate() error {
	if suite.SchemaVersion != EvidenceSuiteSchema || !evidenceID.MatchString(suite.SuiteID) || len(suite.Workloads) < 3 {
		return errors.New("invalid workflow evidence suite identity")
	}
	seen := map[string]struct{}{}
	layers := map[EvidenceLayer]bool{}
	for _, workload := range suite.Workloads {
		if _, duplicate := seen[workload.ID]; duplicate {
			return fmt.Errorf("duplicate workflow %q", workload.ID)
		}
		seen[workload.ID] = struct{}{}
		if err := workload.Validate(); err != nil {
			return fmt.Errorf("workflow %q: %w", workload.ID, err)
		}
		layers[workload.Layer] = true
	}
	for _, layer := range []EvidenceLayer{EvidenceNaturalCensus, EvidenceTraceDAG, EvidenceMechanism} {
		if !layers[layer] {
			return fmt.Errorf("evidence layer %q is absent", layer)
		}
	}
	return nil
}

func (workload EvidenceWorkload) Validate() error {
	if !evidenceID.MatchString(workload.ID) || len(workload.Nodes) == 0 || len(workload.Lanes) < 2 || workload.ClaimBoundary == "" {
		return errors.New("invalid workflow identity or comparison contract")
	}
	if workload.Factors.LogicalAgents == 0 || workload.Factors.PhysicalSlots == 0 || workload.Factors.CallsPerProgram == 0 || workload.Factors.LocalComputePct > 100 {
		return errors.New("invalid workflow factors")
	}
	if !evidenceSHA256.MatchString(workload.CaseSHA256) || !evidenceSHA256.MatchString(workload.OracleSHA256) || !evidenceSHA256.MatchString(workload.LaneConfigSHA256) || !workload.OracleFailClosed {
		return errors.New("workload lacks frozen case, independent oracle, or lane configuration identity")
	}
	if !workload.Metrics.TaskOracle || !workload.Metrics.EffectReceiptEquivalence || !workload.Metrics.AdmissionAndFallback || !workload.Metrics.LogicalAndPhysicalCalls || !workload.Metrics.WallAndTailLatency || !workload.Metrics.GuestStartsAndCompiles {
		return errors.New("required correctness, mechanism, and latency metrics are absent")
	}
	switch workload.Layer {
	case EvidenceNaturalCensus:
		if workload.Origin != "natural_benchmark" || workload.OracleAuthority != "upstream_official" || workload.OptimizationRequired || len(workload.TargetMechanisms) != 0 || workload.ClaimBoundary != "compatibility_and_opportunity_only" {
			return errors.New("natural benchmark cannot require or causally claim optimization")
		}
	case EvidenceTraceDAG:
		if workload.Origin != "private_trace_projection" || workload.OracleAuthority != "private_projection_validator" || !evidenceSHA256.MatchString(workload.SourceTraceSHA256) || !workload.RawBodiesRetainedPrivately || workload.ClaimBoundary != "trace_shape_bounded" {
			return errors.New("trace-derived workload lacks private source identity or bounded claim")
		}
	case EvidenceMechanism:
		if workload.Origin != "authored_mechanism_case" || workload.OracleAuthority != "independent_fixture" || len(workload.TargetMechanisms) == 0 || workload.ClaimBoundary != "mechanism_fixture_bounded" {
			return errors.New("mechanism stress workload lacks a named target")
		}
	default:
		return errors.New("unknown workflow evidence layer")
	}
	return validateWorkflowDAG(workload.Nodes, workload.Edges)
}

func validateWorkflowDAG(nodes []WorkflowNode, edges []WorkflowEdge) error {
	byID := map[string]WorkflowNode{}
	indegree := map[string]int{}
	outgoing := map[string][]string{}
	for _, node := range nodes {
		if !evidenceID.MatchString(node.ID) || !evidenceID.MatchString(node.LogicalAgent) {
			return errors.New("invalid node identity")
		}
		if _, duplicate := byID[node.ID]; duplicate {
			return errors.New("duplicate node identity")
		}
		switch node.Kind {
		case NodeModelTurn:
			if node.Effect != EffectNone || node.CapabilitySHA256 != "" {
				return errors.New("model turn claimed an effect")
			}
		case NodeToolCall:
			if node.Effect != EffectRead && node.Effect != EffectWrite || !evidenceSHA256.MatchString(node.CapabilitySHA256) || (node.Effect == EffectWrite && node.ConcurrencySafe) {
				return errors.New("tool node has an invalid capability/effect contract")
			}
		case NodeLocalCompute:
			if node.Effect != EffectPure || node.CapabilitySHA256 != "" {
				return errors.New("local compute is not authority-free")
			}
		case NodeBarrier:
			if node.Effect != EffectWrite || node.ConcurrencySafe {
				return errors.New("effect barrier is not exclusive")
			}
		default:
			return errors.New("unknown workflow node kind")
		}
		byID[node.ID] = node
		indegree[node.ID] = 0
	}
	for _, edge := range edges {
		if _, ok := byID[edge.From]; !ok {
			return errors.New("edge source is absent")
		}
		if _, ok := byID[edge.To]; !ok || edge.From == edge.To {
			return errors.New("edge destination is absent or reflexive")
		}
		outgoing[edge.From] = append(outgoing[edge.From], edge.To)
		indegree[edge.To]++
	}
	queue := make([]string, 0, len(nodes))
	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}
	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, next := range outgoing[id] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	if visited != len(nodes) {
		return errors.New("workflow graph contains a cycle")
	}
	return nil
}
