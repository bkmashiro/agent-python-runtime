package workflowbench

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

const ManifestSchemaVersion = "pysolate.workflow-benchmark-manifest.v0"

var (
	ErrInvalidManifest = errors.New("invalid workflow benchmark manifest")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	opaquePattern      = regexp.MustCompile(`^(task|workload|node|occurrence)-[0-9a-f]{16,64}$`)
)

type RuntimeIdentity struct {
	SourceCommit           string `json:"source_commit"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	HarnessSHA256          string `json:"harness_sha256"`
}

type Node struct {
	NodeID                 string `json:"node_id"`
	OccurrenceID           string `json:"occurrence_id"`
	Kind                   string `json:"kind"`
	FixtureDurationMillis  uint32 `json:"fixture_duration_millis"`
	BoundaryIdentitySHA256 string `json:"boundary_identity_sha256,omitempty"`
	ResultSHA256           string `json:"result_sha256,omitempty"`
	AuthoritySHA256        string `json:"authority_sha256,omitempty"`
	FreshnessSHA256        string `json:"freshness_sha256,omitempty"`
	PrivacySHA256          string `json:"privacy_sha256,omitempty"`
	ResourceSHA256         string `json:"resource_sha256,omitempty"`
}

type Task struct {
	TaskID               string `json:"task_id"`
	WorkloadID           string `json:"workload_id"`
	Class                string `json:"class"`
	NegativeDimension    string `json:"negative_dimension,omitempty"`
	SubmissionOrder      uint32 `json:"submission_order"`
	ExpectedOutputSHA256 string `json:"expected_output_sha256"`
	Nodes                []Node `json:"nodes"`
}

type Manifest struct {
	SchemaVersion string          `json:"schema_version"`
	Seed          uint64          `json:"seed"`
	Identity      RuntimeIdentity `json:"runtime_identity"`
	Tasks         []Task          `json:"tasks"`
	SealSHA256    string          `json:"seal_sha256"`
}

func GenerateManifest(seed uint64, identity RuntimeIdentity) (Manifest, error) {
	if seed == 0 || !validIdentity(identity) {
		return Manifest{}, ErrInvalidManifest
	}
	specifications := []struct {
		class     string
		dimension string
	}{
		{class: "preissue"},
		{class: "declared_parallel"},
		{class: "coalesced"},
		{class: "retained_reuse"},
		{class: "ordinary"},
		{class: "ordinary"},
	}
	for _, dimension := range []string{"arguments", "freshness", "resource", "privacy", "authority", "source", "artifact", "workflow"} {
		specifications = append(specifications, struct {
			class     string
			dimension string
		}{class: "near_match", dimension: dimension})
	}
	tasks := make([]Task, len(specifications))
	for index, specification := range specifications {
		tasks[index] = makeTask(seed, index, specification.class, specification.dimension)
	}
	shuffle(tasks, seed)
	for index := range tasks {
		tasks[index].SubmissionOrder = uint32(index + 1)
	}
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Seed: seed, Identity: identity, Tasks: tasks}
	manifest.SealSHA256 = manifestSeal(manifest)
	if manifest.Validate() != nil {
		return Manifest{}, ErrInvalidManifest
	}
	return manifest, nil
}

func makeTask(seed uint64, index int, class, dimension string) Task {
	base := fmt.Sprintf("%d:%d:%s:%s", seed, index, class, dimension)
	task := Task{
		TaskID: opaque("task", base), WorkloadID: opaque("workload", base), Class: class,
		NegativeDimension: dimension, Nodes: []Node{},
	}
	appendNode := func(kind string, duration uint32, toolOrdinal int, variation string) {
		nodeSeed := fmt.Sprintf("%s:%s:%d:%s", base, kind, toolOrdinal, variation)
		node := Node{NodeID: opaque("node", nodeSeed), OccurrenceID: opaque("occurrence", nodeSeed), Kind: kind, FixtureDurationMillis: duration}
		if kind == "tool.read" {
			exactSeed := base + ":exact"
			node.BoundaryIdentitySHA256 = digest(exactSeed + ":boundary")
			node.ResultSHA256 = digest(exactSeed + ":result")
			node.AuthoritySHA256 = digest(exactSeed + ":authority")
			node.FreshnessSHA256 = digest(exactSeed + ":freshness")
			node.PrivacySHA256 = digest(exactSeed + ":privacy")
			node.ResourceSHA256 = digest(exactSeed + ":resource")
			if variation != "" {
				switch variation {
				case "freshness":
					node.FreshnessSHA256 = digest(nodeSeed + ":freshness")
				case "privacy":
					node.PrivacySHA256 = digest(nodeSeed + ":privacy")
				case "authority":
					node.AuthoritySHA256 = digest(nodeSeed + ":authority")
				case "resource":
					node.ResourceSHA256 = digest(nodeSeed + ":resource")
				default:
					node.BoundaryIdentitySHA256 = digest(nodeSeed + ":" + variation)
					node.ResultSHA256 = digest(nodeSeed + ":result")
				}
			}
		}
		task.Nodes = append(task.Nodes, node)
	}
	appendNode("model.invocation", 2, 0, "")
	switch class {
	case "preissue", "ordinary":
		appendNode("tool.read", 7, 0, "")
	case "declared_parallel":
		appendNode("tool.read", 8, 0, "left")
		appendNode("tool.read", 8, 1, "right")
	case "coalesced", "retained_reuse":
		appendNode("tool.read", 9, 0, "")
		appendNode("tool.read", 9, 1, "")
	case "near_match":
		appendNode("tool.read", 6, 0, "")
		appendNode("tool.read", 6, 1, dimension)
	}
	appendNode("wait", 1, 0, "")
	appendNode("wasm.compute", 2, 0, "")
	parts := []byte(task.TaskID)
	for _, node := range task.Nodes {
		parts = append(parts, []byte(node.ResultSHA256)...)
	}
	task.ExpectedOutputSHA256 = digest(string(parts))
	return task
}

func (manifest Manifest) Validate() error {
	if manifest.SchemaVersion != ManifestSchemaVersion || manifest.Seed == 0 || !validIdentity(manifest.Identity) ||
		len(manifest.Tasks) != 14 || !digestPattern.MatchString(manifest.SealSHA256) || manifest.SealSHA256 != manifestSeal(manifest) {
		return ErrInvalidManifest
	}
	seenTask := map[string]bool{}
	seenWorkload := map[string]bool{}
	for index, task := range manifest.Tasks {
		if !opaquePattern.MatchString(task.TaskID) || !opaquePattern.MatchString(task.WorkloadID) ||
			seenTask[task.TaskID] || seenWorkload[task.WorkloadID] || task.SubmissionOrder != uint32(index+1) ||
			!slices.Contains([]string{"preissue", "declared_parallel", "coalesced", "retained_reuse", "near_match", "ordinary"}, task.Class) ||
			!digestPattern.MatchString(task.ExpectedOutputSHA256) || task.Nodes == nil || len(task.Nodes) < 4 || len(task.Nodes) > 5 {
			return ErrInvalidManifest
		}
		seenTask[task.TaskID], seenWorkload[task.WorkloadID] = true, true
		if (task.Class == "near_match") != slices.Contains([]string{"arguments", "freshness", "resource", "privacy", "authority", "source", "artifact", "workflow"}, task.NegativeDimension) {
			return ErrInvalidManifest
		}
		seenNode := map[string]bool{}
		seenOccurrence := map[string]bool{}
		for _, node := range task.Nodes {
			if !validNode(node) || seenNode[node.NodeID] || seenOccurrence[node.OccurrenceID] {
				return ErrInvalidManifest
			}
			seenNode[node.NodeID], seenOccurrence[node.OccurrenceID] = true, true
		}
	}
	return nil
}

func validNode(node Node) bool {
	if !opaquePattern.MatchString(node.NodeID) || !opaquePattern.MatchString(node.OccurrenceID) || node.FixtureDurationMillis == 0 ||
		!slices.Contains([]string{"model.invocation", "tool.read", "wait", "wasm.compute"}, node.Kind) {
		return false
	}
	fields := []string{node.BoundaryIdentitySHA256, node.ResultSHA256, node.AuthoritySHA256, node.FreshnessSHA256, node.PrivacySHA256, node.ResourceSHA256}
	if node.Kind == "tool.read" {
		for _, field := range fields {
			if !digestPattern.MatchString(field) {
				return false
			}
		}
		return true
	}
	return slices.Contains(fields, "") && allEmpty(fields)
}

func allEmpty(values []string) bool {
	for _, value := range values {
		if value != "" {
			return false
		}
	}
	return true
}

func validIdentity(identity RuntimeIdentity) bool {
	return commitPattern.MatchString(identity.SourceCommit) && digestPattern.MatchString(identity.ArtifactSHA256) &&
		digestPattern.MatchString(identity.ExecutionProfileSHA256) && digestPattern.MatchString(identity.CapabilityPlanSHA256) &&
		digestPattern.MatchString(identity.HarnessSHA256)
}

func EncodeManifest(manifest Manifest) ([]byte, error) {
	if manifest.Validate() != nil {
		return nil, ErrInvalidManifest
	}
	encoded, err := json.Marshal(manifest)
	if err != nil || len(encoded) > 1<<20 {
		return nil, ErrInvalidManifest
	}
	return encoded, nil
}

func DecodeManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > 1<<20 || rejectDuplicateJSON(raw) != nil {
		return Manifest{}, ErrInvalidManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if decoder.Decode(&manifest) != nil || manifest.Validate() != nil {
		return Manifest{}, ErrInvalidManifest
	}
	var trailing any
	if !errors.Is(decoder.Decode(&trailing), io.EOF) {
		return Manifest{}, ErrInvalidManifest
	}
	canonical, err := EncodeManifest(manifest)
	if err != nil || !bytes.Equal(raw, canonical) {
		return Manifest{}, ErrInvalidManifest
	}
	return manifest, nil
}

func manifestSeal(manifest Manifest) string {
	manifest.SealSHA256 = ""
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return ""
	}
	return digest(string(encoded))
}

func opaque(prefix, value string) string {
	sum := sha256.Sum256([]byte("pysolate.workflow-benchmark-id.v0\x00" + prefix + "\x00" + value))
	return fmt.Sprintf("%s-%x", prefix, sum[:8])
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:])
}

func shuffle(tasks []Task, seed uint64) {
	state := seed
	next := func() uint64 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		return state
	}
	for index := len(tasks) - 1; index > 0; index-- {
		target := int(next() % uint64(index+1))
		tasks[index], tasks[target] = tasks[target], tasks[index]
	}
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return ErrInvalidManifest
				}
				seen[key] = true
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalidManifest
		}
	}
	if err := visit(); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return ErrInvalidManifest
	}
	return nil
}
