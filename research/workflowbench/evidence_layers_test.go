package workflowbench

import "testing"

func validEvidenceMetrics() WorkflowMetricContract {
	return WorkflowMetricContract{
		TaskOracle: true, EffectReceiptEquivalence: true, AdmissionAndFallback: true,
		ModelTurns: true, ProviderCalls: true, LogicalAndPhysicalCalls: true,
		WallAndTailLatency: true, CPUAndPeakRSS: true, GuestStartsAndCompiles: true,
	}
}

func evidenceReadNode(id, agent string) WorkflowNode {
	return WorkflowNode{ID: id, Kind: NodeToolCall, Effect: EffectRead, CapabilitySHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ConcurrencySafe: true, LogicalAgent: agent}
}

func validEvidenceSuite() EvidenceSuite {
	lanes := []string{"direct_code_worker", "pysolate_fresh", "pysolate_prepared"}
	factors := WorkflowFactors{LogicalAgents: 1, PhysicalSlots: 1, CallsPerProgram: 1, PayloadClass: "small", FailureMode: "none"}
	return EvidenceSuite{SchemaVersion: EvidenceSuiteSchema, SuiteID: "workflow-evidence-v1", Workloads: []EvidenceWorkload{
		{
			ID: "natural-tau2-read", Layer: EvidenceNaturalCensus, Origin: "natural_benchmark", OracleAuthority: "upstream_official", OracleFailClosed: true,
			CaseSHA256: "sha256:1111111111111111111111111111111111111111111111111111111111111111", OracleSHA256: "sha256:2222222222222222222222222222222222222222222222222222222222222222", LaneConfigSHA256: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
			Nodes: []WorkflowNode{evidenceReadNode("read", "agent-1")}, Factors: factors, Lanes: lanes,
			Metrics: validEvidenceMetrics(), ClaimBoundary: "compatibility_and_opportunity_only",
		},
		{
			ID: "trace-read-fan-in", Layer: EvidenceTraceDAG, Origin: "private_trace_projection", OracleAuthority: "private_projection_validator", OracleFailClosed: true,
			CaseSHA256: "sha256:4444444444444444444444444444444444444444444444444444444444444444", OracleSHA256: "sha256:5555555555555555555555555555555555555555555555555555555555555555", LaneConfigSHA256: "sha256:6666666666666666666666666666666666666666666666666666666666666666",
			SourceTraceSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", RawBodiesRetainedPrivately: true,
			Nodes:   []WorkflowNode{evidenceReadNode("read-a", "agent-1"), evidenceReadNode("read-b", "agent-1"), {ID: "join", Kind: NodeLocalCompute, Effect: EffectPure, ConcurrencySafe: true, LogicalAgent: "agent-1"}},
			Edges:   []WorkflowEdge{{From: "read-a", To: "join"}, {From: "read-b", To: "join"}},
			Factors: WorkflowFactors{LogicalAgents: 1, PhysicalSlots: 2, CallsPerProgram: 2, LocalComputePct: 25, PayloadClass: "medium", FailureMode: "none"},
			Lanes:   lanes, Metrics: validEvidenceMetrics(), ClaimBoundary: "trace_shape_bounded",
		},
		{
			ID: "stress-shared-substrate", Layer: EvidenceMechanism, Origin: "authored_mechanism_case", OracleAuthority: "independent_fixture", OracleFailClosed: true, OptimizationRequired: true,
			CaseSHA256: "sha256:7777777777777777777777777777777777777777777777777777777777777777", OracleSHA256: "sha256:8888888888888888888888888888888888888888888888888888888888888888", LaneConfigSHA256: "sha256:9999999999999999999999999999999999999999999999999999999999999999",
			TargetMechanisms: []string{"prepared_runtime", "memory_cow", "shared_physical_guest"},
			Nodes:            []WorkflowNode{evidenceReadNode("read-a", "agent-1"), evidenceReadNode("read-b", "agent-2")},
			Factors:          WorkflowFactors{LogicalAgents: 16, PhysicalSlots: 4, CallsPerProgram: 4, LocalComputePct: 75, PayloadClass: "medium", FailureMode: "none"},
			Lanes:            lanes, Metrics: validEvidenceMetrics(), ClaimBoundary: "mechanism_fixture_bounded",
		},
	}}
}

func TestEvidenceSuiteSeparatesThreeClaimLayers(t *testing.T) {
	if err := validEvidenceSuite().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceSuiteRejectsNaturalOptimizationClaim(t *testing.T) {
	suite := validEvidenceSuite()
	suite.Workloads[0].OptimizationRequired = true
	if err := suite.Validate(); err == nil {
		t.Fatal("natural benchmark optimization claim accepted")
	}
}

func TestEvidenceSuiteRejectsTraceWithoutFrozenIdentity(t *testing.T) {
	suite := validEvidenceSuite()
	suite.Workloads[1].SourceTraceSHA256 = ""
	if err := suite.Validate(); err == nil {
		t.Fatal("unbound trace projection accepted")
	}
}

func TestEvidenceSuiteRejectsSelfAttestedOracle(t *testing.T) {
	suite := validEvidenceSuite()
	suite.Workloads[2].OracleSHA256 = ""
	if err := suite.Validate(); err == nil {
		t.Fatal("boolean-only task oracle accepted without a frozen independent oracle")
	}
	suite = validEvidenceSuite()
	suite.Workloads[0].OracleAuthority = "independent_fixture"
	if err := suite.Validate(); err == nil {
		t.Fatal("natural benchmark accepted a non-upstream oracle authority")
	}
}

func TestEvidenceSuiteRejectsCyclesAndConcurrentWrites(t *testing.T) {
	suite := validEvidenceSuite()
	suite.Workloads[1].Edges = append(suite.Workloads[1].Edges, WorkflowEdge{From: "join", To: "read-a"})
	if err := suite.Validate(); err == nil {
		t.Fatal("cyclic workflow accepted")
	}
	suite = validEvidenceSuite()
	suite.Workloads[2].Nodes[0].Effect = EffectWrite
	if err := suite.Validate(); err == nil {
		t.Fatal("concurrency-safe external write accepted")
	}
}
