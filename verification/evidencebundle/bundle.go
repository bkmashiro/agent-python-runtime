// Package evidencebundle connects already-qualified evidence across Host,
// Runtime, transaction, and checkpoint planes without upgrading any source's
// replay or authority qualification.
package evidencebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const Version = "evidence-bundle/v1"

const (
	maxNodes = 16
	maxEdges = 32
)

var (
	ErrInvalidBundle      = errors.New("evidence bundle: invalid graph")
	ErrContradictedSource = errors.New("evidence bundle: contradicted source")
)

type NodeKind string

type EdgeKind string

type Profile string

type Status string

type Requirement string

const (
	NodeAgentRun            NodeKind = "agent-run"
	NodeExecution           NodeKind = "execution"
	NodeClaimManifest       NodeKind = "claim-manifest"
	NodeTransactionEvidence NodeKind = "transaction-evidence"
	NodeCheckpoint          NodeKind = "checkpoint"
)

const (
	EdgeManifestSupportsExecution      EdgeKind = "manifest-supports-execution"
	EdgeExecutionBelongsToRun          EdgeKind = "execution-belongs-to-run"
	EdgeTransactionReconcilesExecution EdgeKind = "transaction-reconciles-execution"
	EdgeCheckpointBelongsToRun         EdgeKind = "checkpoint-belongs-to-run"
	EdgeCheckpointAncestor             EdgeKind = "checkpoint-ancestor-of-execution"
)

const (
	ProfileStructuralExecution Profile = "structural-execution/v1"
	ProfileCurrentCrossPlane   Profile = "current-cross-plane/v1"
	ProfileFullOutcome         Profile = "full-outcome/v1"
)

const (
	StatusVerified     Status = "verified"
	StatusInsufficient Status = "insufficient"
)

const (
	RequirementManifestBinding      Requirement = "claim-manifest-binding"
	RequirementExecutionRunBinding  Requirement = "execution-run-binding"
	RequirementReconciledEffect     Requirement = "reconciled-effect"
	RequirementCheckpointMembership Requirement = "checkpoint-run-membership"
	RequirementCheckpointLineage    Requirement = "checkpoint-execution-lineage"
	RequirementFinalStateOracle     Requirement = "final-state-oracle"
)

type Node struct {
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	Ref  string   `json:"ref"`
}

type Edge struct {
	Kind EdgeKind `json:"kind"`
	From string   `json:"from"`
	To   string   `json:"to"`
}

type Bundle struct {
	Version string `json:"version"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

type Sources struct {
	Manifest          claimmanifest.Manifest
	Playback          agenttrace.Playback
	Transaction       *transaction.TransactionEvidence
	CheckpointEventID string
}

type Report struct {
	Profile Profile       `json:"profile"`
	Status  Status        `json:"status"`
	Missing []Requirement `json:"missing,omitempty"`
}

func Build(sources Sources) (Bundle, error) {
	facts, err := deriveFacts(sources)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{Version: Version, Nodes: mapNodes(facts.nodes), Edges: mapEdges(facts.edges)}
	sort.Slice(bundle.Nodes, func(left, right int) bool { return bundle.Nodes[left].ID < bundle.Nodes[right].ID })
	sort.Slice(bundle.Edges, func(left, right int) bool { return edgeKey(bundle.Edges[left]) < edgeKey(bundle.Edges[right]) })
	return bundle, nil
}

func Verify(bundle Bundle, sources Sources, profile Profile) (Report, error) {
	requirements, ok := profileRequirements(profile)
	if !ok || bundle.Version != Version || len(bundle.Nodes) > maxNodes || len(bundle.Edges) > maxEdges {
		return Report{}, ErrInvalidBundle
	}
	facts, err := deriveFacts(sources)
	if err != nil {
		return Report{}, err
	}
	presentNodes := make(map[string]Node, len(bundle.Nodes))
	for _, node := range bundle.Nodes {
		if node.ID == "" || node.Ref == "" {
			return Report{}, ErrInvalidBundle
		}
		if _, duplicate := presentNodes[node.ID]; duplicate {
			return Report{}, ErrInvalidBundle
		}
		expected, supported := facts.nodes[node.ID]
		if !supported || expected != node {
			return Report{}, ErrInvalidBundle
		}
		presentNodes[node.ID] = node
	}
	presentEdges := make(map[string]Edge, len(bundle.Edges))
	for _, edge := range bundle.Edges {
		key := edgeKey(edge)
		if _, duplicate := presentEdges[key]; duplicate {
			return Report{}, ErrInvalidBundle
		}
		expected, supported := facts.edges[key]
		if !supported || expected != edge {
			return Report{}, ErrInvalidBundle
		}
		if _, ok := presentNodes[edge.From]; !ok {
			return Report{}, ErrInvalidBundle
		}
		if _, ok := presentNodes[edge.To]; !ok {
			return Report{}, ErrInvalidBundle
		}
		presentEdges[key] = edge
	}

	report := Report{Profile: profile, Status: StatusVerified}
	for _, requirement := range requirements {
		if requirement == RequirementFinalStateOracle || !requirementPresent(requirement, facts, presentNodes, presentEdges) {
			report.Missing = append(report.Missing, requirement)
		}
	}
	if len(report.Missing) != 0 {
		report.Status = StatusInsufficient
	}
	return report, nil
}

type derivedFacts struct {
	nodes       map[string]Node
	edges       map[string]Edge
	requirement map[Requirement]string
}

func deriveFacts(sources Sources) (derivedFacts, error) {
	if sources.Playback.ValidateBounds() != nil {
		return derivedFacts{}, ErrContradictedSource
	}
	if err := sources.Manifest.Validate(); err != nil {
		return derivedFacts{}, ErrContradictedSource
	}
	reconstructed, err := claimmanifest.FromMetadataPlayback(sources.Manifest.ExecutionRef, sources.Playback)
	if err != nil || !reflect.DeepEqual(reconstructed, sources.Manifest) {
		return derivedFacts{}, ErrContradictedSource
	}
	manifestDigest, err := digestManifest(sources.Manifest)
	if err != nil {
		return derivedFacts{}, ErrContradictedSource
	}
	ref := sources.Manifest.ExecutionRef
	runID := "agent-run:" + ref.AgentRunID
	executionID := "execution:" + ref.ExecutionID
	manifestID := "claim-manifest:" + ref.ExecutionID
	facts := derivedFacts{
		nodes: map[string]Node{
			runID:       {ID: runID, Kind: NodeAgentRun, Ref: ref.AgentRunID},
			executionID: {ID: executionID, Kind: NodeExecution, Ref: ref.ExecutedCodeSHA256},
			manifestID:  {ID: manifestID, Kind: NodeClaimManifest, Ref: manifestDigest},
		},
		edges:       make(map[string]Edge),
		requirement: make(map[Requirement]string),
	}
	facts.add(RequirementManifestBinding, Edge{Kind: EdgeManifestSupportsExecution, From: manifestID, To: executionID})
	facts.add(RequirementExecutionRunBinding, Edge{Kind: EdgeExecutionBelongsToRun, From: executionID, To: runID})

	if sources.Transaction != nil {
		if transaction.VerifyTransactionEvidenceDigest(*sources.Transaction) != nil ||
			sources.Transaction.Transaction.RunID != ref.ExecutionID || !hasReconciledEffect(*sources.Transaction) {
			return derivedFacts{}, ErrContradictedSource
		}
		transactionID := "transaction-evidence:" + sources.Transaction.Transaction.ID
		facts.nodes[transactionID] = Node{ID: transactionID, Kind: NodeTransactionEvidence, Ref: sources.Transaction.EvidenceDigest}
		facts.add(RequirementReconciledEffect, Edge{Kind: EdgeTransactionReconcilesExecution, From: transactionID, To: executionID})
	}

	if sources.CheckpointEventID != "" {
		checkpoint, ok := checkpointAncestor(sources.Playback, sources.Manifest.CompletedEventID, sources.CheckpointEventID)
		if !ok {
			return derivedFacts{}, ErrContradictedSource
		}
		checkpointID := "checkpoint:" + checkpoint.EventID
		facts.nodes[checkpointID] = Node{ID: checkpointID, Kind: NodeCheckpoint, Ref: checkpoint.StateFingerprint}
		facts.add(RequirementCheckpointMembership, Edge{Kind: EdgeCheckpointBelongsToRun, From: checkpointID, To: runID})
		facts.add(RequirementCheckpointLineage, Edge{Kind: EdgeCheckpointAncestor, From: checkpointID, To: executionID})
	}
	return facts, nil
}

func (facts *derivedFacts) add(requirement Requirement, edge Edge) {
	key := edgeKey(edge)
	facts.edges[key] = edge
	facts.requirement[requirement] = key
}

func hasReconciledEffect(value transaction.TransactionEvidence) bool {
	if len(value.Operations) == 0 || len(value.Operations) > 1024 || len(value.Attempts) == 0 || len(value.Attempts) > 4096 ||
		value.Transaction.ID == "" || value.Transaction.State != transaction.TransactionCommitted {
		return false
	}
	applied := make(map[string]string, len(value.Operations))
	for _, operation := range value.Operations {
		if operation.ID == "" || operation.TransactionID != value.Transaction.ID {
			return false
		}
		if operation.State == transaction.OperationApplied && operation.EffectClass == transaction.EffectIrreversible {
			applied[operation.ID] = operation.ManifestDigest
		}
	}
	for _, attempt := range value.Attempts {
		if attempt.ID == "" || attempt.TransactionID != value.Transaction.ID {
			return false
		}
		operationManifestDigest, operationApplied := applied[attempt.OperationID]
		if operationApplied && attempt.ProviderRequestDigest == operationManifestDigest &&
			attempt.EffectClass == transaction.EffectIrreversible && attempt.State == transaction.AttemptSucceeded &&
			validDigest(attempt.ProviderReceiptDigest) && validDigest(attempt.ReconciliationDigest) &&
			attempt.ProviderReceiptDigest != attempt.ReconciliationDigest {
			return true
		}
	}
	return false
}

func checkpointAncestor(playback agenttrace.Playback, completedEventID, checkpointEventID string) (agenttrace.Event, bool) {
	if _, err := playback.IntegrityDigest(); err != nil {
		return agenttrace.Event{}, false
	}
	events := make(map[string]agenttrace.Event, len(playback.Events))
	for _, event := range playback.Events {
		events[event.EventID] = event
	}
	checkpoint, ok := events[checkpointEventID]
	if !ok || checkpoint.EventType != agenttrace.EventCheckpointCreated || checkpoint.StateFingerprint == "" {
		return agenttrace.Event{}, false
	}
	current, ok := events[completedEventID]
	if !ok || current.EventType != agenttrace.EventRuntimeCompleted {
		return agenttrace.Event{}, false
	}
	for step := 0; step < len(playback.Events) && current.ParentEventID != ""; step++ {
		if current.ParentEventID == checkpointEventID {
			return checkpoint, true
		}
		current, ok = events[current.ParentEventID]
		if !ok {
			return agenttrace.Event{}, false
		}
	}
	return agenttrace.Event{}, false
}

func digestManifest(manifest claimmanifest.Manifest) (string, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(append([]byte("evidence-bundle/claim-manifest/v1\n"), encoded...))
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func profileRequirements(profile Profile) ([]Requirement, bool) {
	structural := []Requirement{RequirementManifestBinding, RequirementExecutionRunBinding}
	switch profile {
	case ProfileStructuralExecution:
		return structural, true
	case ProfileCurrentCrossPlane:
		return append(structural, RequirementReconciledEffect, RequirementCheckpointMembership, RequirementCheckpointLineage), true
	case ProfileFullOutcome:
		return append(structural, RequirementReconciledEffect, RequirementCheckpointMembership, RequirementCheckpointLineage, RequirementFinalStateOracle), true
	default:
		return nil, false
	}
}

func requirementPresent(requirement Requirement, facts derivedFacts, nodes map[string]Node, edges map[string]Edge) bool {
	key, ok := facts.requirement[requirement]
	if !ok {
		return false
	}
	edge, ok := edges[key]
	if !ok {
		return false
	}
	_, from := nodes[edge.From]
	_, to := nodes[edge.To]
	return from && to
}

func mapNodes(values map[string]Node) []Node {
	result := make([]Node, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func mapEdges(values map[string]Edge) []Edge {
	result := make([]Edge, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func edgeKey(edge Edge) string {
	return string(edge.Kind) + "\x00" + edge.From + "\x00" + edge.To
}
