// Package workflow implements bounded explicit fresh re-evaluation. It never
// snapshots Guest interpreter state.
package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"time"
)

const (
	GraphSchemaVersion = "pysolate.workflow-graph.v1"
	StateSchemaVersion = "pysolate.workflow-state.v1"
)

var (
	ErrInvalidGraph   = errors.New("invalid explicit workflow graph")
	ErrInvalidState   = errors.New("invalid explicit workflow state")
	ErrResumeDisabled = errors.New("workflow fresh re-evaluation is disabled")
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type NodeKind string

const (
	Compute     NodeKind = "compute"
	Observation NodeKind = "observation"
	Wait        NodeKind = "wait"
	Join        NodeKind = "join"
	Terminal    NodeKind = "terminal"
)

type Guest interface {
	Close(context.Context) error
}

type GuestFactory interface {
	NewGuest(context.Context) (Guest, error)
}

type ComputeFunc func(context.Context, Guest, map[string][]byte) ([]byte, error)
type ObserveFunc func(context.Context, Guest, map[string][]byte) (ObservedValue, error)

type ObservedValue struct {
	Value           []byte
	FreshnessSHA256 string
	PolicySHA256    string
}

type Node struct {
	ID              string
	Kind            NodeKind
	VersionSHA256   string
	Dependencies    []string
	RefreshOnResume bool
	Compute         ComputeFunc
	Observe         ObserveFunc
}

type Graph struct {
	SchemaVersion string
	WorkflowID    string
	Nodes         []Node
}

func (graph Graph) Identity() (string, []byte, error) {
	if err := graph.Validate(); err != nil {
		return "", nil, err
	}
	type nodeDocument struct {
		ID              string   `json:"id"`
		Kind            NodeKind `json:"kind"`
		VersionSHA256   string   `json:"version_sha256"`
		Dependencies    []string `json:"dependencies"`
		RefreshOnResume bool     `json:"refresh_on_resume"`
	}
	type graphDocument struct {
		SchemaVersion string         `json:"schema_version"`
		WorkflowID    string         `json:"workflow_id"`
		Nodes         []nodeDocument `json:"nodes"`
	}
	document := graphDocument{SchemaVersion: graph.SchemaVersion, WorkflowID: graph.WorkflowID, Nodes: make([]nodeDocument, 0, len(graph.Nodes))}
	for _, node := range graph.Nodes {
		document.Nodes = append(document.Nodes, nodeDocument{
			ID: node.ID, Kind: node.Kind, VersionSHA256: node.VersionSHA256,
			Dependencies: append([]string(nil), node.Dependencies...), RefreshOnResume: node.RefreshOnResume,
		})
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return "", nil, err
	}
	return digest(raw), raw, nil
}

func (graph Graph) Validate() error {
	if graph.SchemaVersion != GraphSchemaVersion || !namePattern.MatchString(graph.WorkflowID) || len(graph.Nodes) == 0 {
		return ErrInvalidGraph
	}
	seen := make(map[string]struct{}, len(graph.Nodes))
	terminals := 0
	waits := 0
	for _, node := range graph.Nodes {
		if !namePattern.MatchString(node.ID) || !digestPattern.MatchString(node.VersionSHA256) {
			return ErrInvalidGraph
		}
		if _, duplicate := seen[node.ID]; duplicate {
			return ErrInvalidGraph
		}
		dependencySet := make(map[string]struct{}, len(node.Dependencies))
		for _, dependency := range node.Dependencies {
			if _, exists := seen[dependency]; !exists {
				return ErrInvalidGraph
			}
			if _, duplicate := dependencySet[dependency]; duplicate {
				return ErrInvalidGraph
			}
			dependencySet[dependency] = struct{}{}
		}
		switch node.Kind {
		case Compute:
			if node.Compute == nil || node.Observe != nil || node.RefreshOnResume {
				return ErrInvalidGraph
			}
		case Observation:
			if node.Observe == nil || node.Compute != nil {
				return ErrInvalidGraph
			}
		case Wait:
			if node.Compute != nil || node.Observe != nil {
				return ErrInvalidGraph
			}
			waits++
		case Join:
			if node.Compute != nil || node.Observe != nil {
				return ErrInvalidGraph
			}
		case Terminal:
			if node.Compute != nil || node.Observe != nil {
				return ErrInvalidGraph
			}
			terminals++
		default:
			return ErrInvalidGraph
		}
		seen[node.ID] = struct{}{}
	}
	if terminals != 1 || waits != 1 || graph.Nodes[len(graph.Nodes)-1].Kind != Terminal {
		return ErrInvalidGraph
	}
	return nil
}

type Record struct {
	NodeIdentitySHA256 string `json:"node_identity_sha256"`
	Value              []byte `json:"value"`
	ValueSHA256        string `json:"value_sha256"`
	FreshnessSHA256    string `json:"freshness_sha256,omitempty"`
	PolicySHA256       string `json:"policy_sha256,omitempty"`
}

type State struct {
	SchemaVersion       string            `json:"schema_version"`
	WorkflowID          string            `json:"workflow_id"`
	GraphSHA256         string            `json:"graph_sha256"`
	Disposition         Disposition       `json:"disposition"`
	ImmutableRootSHA256 []string          `json:"immutable_root_sha256"`
	ContinuationInput   json.RawMessage   `json:"continuation_input,omitempty"`
	Records             map[string]Record `json:"records"`
	WaitNodeID          string            `json:"wait_node_id,omitempty"`
}

func (state State) CanonicalJSON() ([]byte, error) {
	if err := state.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

func (state State) Validate() error {
	if state.SchemaVersion != StateSchemaVersion || !namePattern.MatchString(state.WorkflowID) || !digestPattern.MatchString(state.GraphSHA256) ||
		(state.Disposition != Suspended && state.Disposition != Completed) || state.Records == nil || !sort.StringsAreSorted(state.ImmutableRootSHA256) {
		return ErrInvalidState
	}
	if len(state.ContinuationInput) > 0 && !canonicalJSON(state.ContinuationInput) {
		return ErrInvalidState
	}
	if state.WaitNodeID != "" && !namePattern.MatchString(state.WaitNodeID) {
		return ErrInvalidState
	}
	if (state.Disposition == Suspended && state.WaitNodeID == "") || (state.Disposition == Completed && state.WaitNodeID != "") {
		return ErrInvalidState
	}
	for index, root := range state.ImmutableRootSHA256 {
		if !digestPattern.MatchString(root) || (index > 0 && state.ImmutableRootSHA256[index-1] == root) {
			return ErrInvalidState
		}
	}
	for id, record := range state.Records {
		if !namePattern.MatchString(id) || !digestPattern.MatchString(record.NodeIdentitySHA256) || !digestPattern.MatchString(record.ValueSHA256) ||
			(record.FreshnessSHA256 != "" && !digestPattern.MatchString(record.FreshnessSHA256)) ||
			(record.PolicySHA256 != "" && !digestPattern.MatchString(record.PolicySHA256)) || digest(record.Value) != record.ValueSHA256 {
			return ErrInvalidState
		}
	}
	return nil
}

func (state *State) Evict(nodeID string) error {
	if state == nil || !namePattern.MatchString(nodeID) || state.Records == nil {
		return ErrInvalidState
	}
	delete(state.Records, nodeID)
	return nil
}

type Config struct {
	Graph               Graph
	Guests              GuestFactory
	ResumeEnabled       bool
	ImmutableRootSHA256 []string
}

type Evaluator struct {
	config   Config
	graphSHA string
}

func New(config Config) (*Evaluator, error) {
	if config.Guests == nil || config.Graph.Validate() != nil || !sort.StringsAreSorted(config.ImmutableRootSHA256) {
		return nil, ErrInvalidGraph
	}
	for index, root := range config.ImmutableRootSHA256 {
		if !digestPattern.MatchString(root) || (index > 0 && config.ImmutableRootSHA256[index-1] == root) {
			return nil, ErrInvalidGraph
		}
	}
	graphSHA, _, err := config.Graph.Identity()
	if err != nil {
		return nil, err
	}
	return &Evaluator{config: config, graphSHA: graphSHA}, nil
}

type Disposition string

const (
	Suspended Disposition = "suspended"
	Completed Disposition = "completed"
)

type Metrics struct {
	GuestInstances     uint32  `json:"guest_instances"`
	Lookups            uint32  `json:"lookups"`
	Recomputed         uint32  `json:"recomputed"`
	Refreshed          uint32  `json:"refreshed"`
	Invalidated        uint32  `json:"invalidated"`
	RetainedStateBytes uint64  `json:"retained_state_bytes"`
	EvaluationMS       float64 `json:"evaluation_ms"`
}

type Result struct {
	Disposition Disposition
	State       State
	Output      []byte
	Metrics     Metrics
}

func (evaluator *Evaluator) Start(ctx context.Context, continuationInput []byte) (Result, error) {
	state := State{
		SchemaVersion: StateSchemaVersion, WorkflowID: evaluator.config.Graph.WorkflowID, GraphSHA256: evaluator.graphSHA,
		Disposition: Suspended, ImmutableRootSHA256: append([]string(nil), evaluator.config.ImmutableRootSHA256...),
		Records: make(map[string]Record),
	}
	if len(continuationInput) > 0 {
		if !canonicalJSON(continuationInput) {
			return Result{}, ErrInvalidState
		}
		state.ContinuationInput = append(json.RawMessage(nil), continuationInput...)
	}
	return evaluator.evaluate(ctx, state, false)
}

func (evaluator *Evaluator) Resume(ctx context.Context, state State) (Result, error) {
	if evaluator == nil || !evaluator.config.ResumeEnabled {
		return Result{}, ErrResumeDisabled
	}
	if state.Validate() != nil || state.WorkflowID != evaluator.config.Graph.WorkflowID || state.GraphSHA256 != evaluator.graphSHA ||
		state.Disposition != Suspended || !equalStrings(state.ImmutableRootSHA256, evaluator.config.ImmutableRootSHA256) || state.WaitNodeID == "" {
		return Result{}, ErrInvalidState
	}
	return evaluator.evaluate(ctx, cloneState(state), true)
}

func (evaluator *Evaluator) evaluate(ctx context.Context, state State, resuming bool) (result Result, returnErr error) {
	started := time.Now()
	guest, err := evaluator.config.Guests.NewGuest(ctx)
	if err != nil {
		return Result{}, err
	}
	if guest == nil {
		return Result{}, ErrInvalidState
	}
	metrics := Metrics{GuestInstances: 1}
	defer func() {
		if closeErr := guest.Close(ctx); returnErr == nil && closeErr != nil {
			returnErr = closeErr
		}
	}()
	if resuming {
		invalidated, err := evaluator.refreshObservations(ctx, guest, &state, &metrics)
		if err != nil {
			return Result{}, err
		}
		metrics.Invalidated += invalidated
	}
	values := make(map[string][]byte, len(evaluator.config.Graph.Nodes))
	passedWait := !resuming
	for _, node := range evaluator.config.Graph.Nodes {
		dependencies, err := dependencyValues(node.Dependencies, values)
		if err != nil {
			return Result{}, err
		}
		if node.Kind == Wait {
			if resuming && node.ID == state.WaitNodeID {
				passedWait = true
				continue
			}
			if !passedWait || !resuming {
				state.WaitNodeID = node.ID
				return finalize(Result{Disposition: Suspended, State: state, Metrics: metrics}, started)
			}
			state.WaitNodeID = node.ID
			return finalize(Result{Disposition: Suspended, State: state, Metrics: metrics}, started)
		}
		identity := nodeIdentity(node, state.Records)
		if record, ok := state.Records[node.ID]; ok && record.NodeIdentitySHA256 == identity {
			values[node.ID] = append([]byte(nil), record.Value...)
			metrics.Lookups++
			if node.Kind == Terminal {
				state.Disposition = Completed
				state.WaitNodeID = ""
				return finalize(Result{Disposition: Completed, State: state, Output: terminalOutput(node, values), Metrics: metrics}, started)
			}
			continue
		}
		switch node.Kind {
		case Compute:
			value, err := node.Compute(ctx, guest, dependencies)
			if err != nil {
				return Result{}, err
			}
			metrics.Recomputed++
			state.Records[node.ID] = makeRecord(identity, value, "", "")
			values[node.ID] = append([]byte(nil), value...)
		case Observation:
			observed, err := node.Observe(ctx, guest, dependencies)
			if err != nil || !digestPattern.MatchString(observed.FreshnessSHA256) || !digestPattern.MatchString(observed.PolicySHA256) {
				return Result{}, ErrInvalidState
			}
			metrics.Refreshed++
			state.Records[node.ID] = makeRecord(identity, observed.Value, observed.FreshnessSHA256, observed.PolicySHA256)
			values[node.ID] = append([]byte(nil), observed.Value...)
		case Join:
			value := terminalOutput(node, values)
			state.Records[node.ID] = makeRecord(identity, value, "", "")
			values[node.ID] = value
		case Terminal:
			state.Records[node.ID] = makeRecord(identity, terminalOutput(node, values), "", "")
			state.Disposition = Completed
			state.WaitNodeID = ""
			return finalize(Result{Disposition: Completed, State: state, Output: terminalOutput(node, values), Metrics: metrics}, started)
		}
	}
	return Result{}, ErrInvalidGraph
}

func (evaluator *Evaluator) refreshObservations(ctx context.Context, guest Guest, state *State, metrics *Metrics) (uint32, error) {
	var invalidated uint32
	for _, node := range evaluator.config.Graph.Nodes {
		if node.Kind != Observation || !node.RefreshOnResume {
			continue
		}
		dependencies := make(map[string][]byte, len(node.Dependencies))
		ready := true
		for _, dependency := range node.Dependencies {
			record, ok := state.Records[dependency]
			if !ok {
				ready = false
				break
			}
			dependencies[dependency] = append([]byte(nil), record.Value...)
		}
		if !ready {
			continue
		}
		observed, err := node.Observe(ctx, guest, dependencies)
		if err != nil {
			return invalidated, err
		}
		if !digestPattern.MatchString(observed.FreshnessSHA256) || !digestPattern.MatchString(observed.PolicySHA256) {
			return invalidated, ErrInvalidState
		}
		metrics.Refreshed++
		old, existed := state.Records[node.ID]
		identity := nodeIdentity(node, state.Records)
		fresh := makeRecord(identity, observed.Value, observed.FreshnessSHA256, observed.PolicySHA256)
		if existed && recordsEqual(old, fresh) {
			metrics.Lookups++
			continue
		}
		if existed {
			delete(state.Records, node.ID)
			invalidated++
		}
		for _, descendant := range evaluator.descendants(node.ID) {
			if _, exists := state.Records[descendant]; exists {
				delete(state.Records, descendant)
				invalidated++
			}
		}
		state.Records[node.ID] = fresh
	}
	return invalidated, nil
}

func (evaluator *Evaluator) descendants(nodeID string) []string {
	descendants := make(map[string]struct{})
	for _, node := range evaluator.config.Graph.Nodes {
		for _, dependency := range node.Dependencies {
			if dependency == nodeID {
				descendants[node.ID] = struct{}{}
			}
			if _, inherited := descendants[dependency]; inherited {
				descendants[node.ID] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(descendants))
	for value := range descendants {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func nodeIdentity(node Node, records map[string]Record) string {
	type identity struct {
		ID           string   `json:"id"`
		Kind         NodeKind `json:"kind"`
		Version      string   `json:"version_sha256"`
		Dependencies []string `json:"dependency_result_sha256"`
	}
	dependencies := make([]string, 0, len(node.Dependencies))
	for _, dependency := range node.Dependencies {
		dependencies = append(dependencies, records[dependency].ValueSHA256)
	}
	raw, _ := json.Marshal(identity{ID: node.ID, Kind: node.Kind, Version: node.VersionSHA256, Dependencies: dependencies})
	return digest(raw)
}

func makeRecord(identity string, value []byte, freshness, policy string) Record {
	return Record{
		NodeIdentitySHA256: identity, Value: append([]byte(nil), value...), ValueSHA256: digest(value),
		FreshnessSHA256: freshness, PolicySHA256: policy,
	}
}

func recordsEqual(left, right Record) bool {
	return left.NodeIdentitySHA256 == right.NodeIdentitySHA256 && left.ValueSHA256 == right.ValueSHA256 &&
		left.FreshnessSHA256 == right.FreshnessSHA256 && left.PolicySHA256 == right.PolicySHA256
}

func dependencyValues(dependencies []string, values map[string][]byte) (map[string][]byte, error) {
	result := make(map[string][]byte, len(dependencies))
	for _, dependency := range dependencies {
		value, ok := values[dependency]
		if !ok {
			return nil, ErrInvalidState
		}
		result[dependency] = append([]byte(nil), value...)
	}
	return result, nil
}

func terminalOutput(node Node, values map[string][]byte) []byte {
	if len(node.Dependencies) == 0 {
		return nil
	}
	return append([]byte(nil), values[node.Dependencies[len(node.Dependencies)-1]]...)
}

func finalize(result Result, started time.Time) (Result, error) {
	encoded, err := result.State.CanonicalJSON()
	if err != nil {
		return Result{}, err
	}
	result.Metrics.RetainedStateBytes = uint64(len(encoded))
	result.Metrics.EvaluationMS = max(0, float64(time.Since(started).Nanoseconds())/1e6)
	return result, nil
}

func cloneState(state State) State {
	cloned := state
	cloned.ContinuationInput = append(json.RawMessage(nil), state.ContinuationInput...)
	cloned.ImmutableRootSHA256 = append([]string(nil), state.ImmutableRootSHA256...)
	cloned.Records = make(map[string]Record, len(state.Records))
	for id, record := range state.Records {
		record.Value = append([]byte(nil), record.Value...)
		cloned.Records[id] = record
	}
	return cloned
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func canonicalJSON(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return false
	}
	canonical, err := json.Marshal(value)
	return err == nil && bytes.Equal(canonical, raw)
}
