package labstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const BenchmarkSchemaVersion = "pysolate.labstore-benchmark.v1"

type BenchmarkConfig struct {
	LongSteps      uint32 `json:"long_steps"`
	BranchChildren uint32 `json:"branch_children"`
	SwarmAgents    uint32 `json:"swarm_agents"`
	SwarmSteps     uint32 `json:"swarm_steps"`
	LowReuseItems  uint32 `json:"low_reuse_items"`
	PayloadBytes   uint32 `json:"payload_bytes"`
}

func DefaultBenchmarkConfig() BenchmarkConfig {
	return BenchmarkConfig{
		LongSteps: 128, BranchChildren: 64, SwarmAgents: 16,
		SwarmSteps: 12, LowReuseItems: 64, PayloadBytes: 2048,
	}
}

func (config BenchmarkConfig) normalized() (BenchmarkConfig, error) {
	defaults := DefaultBenchmarkConfig()
	if config.LongSteps == 0 {
		config.LongSteps = defaults.LongSteps
	}
	if config.BranchChildren == 0 {
		config.BranchChildren = defaults.BranchChildren
	}
	if config.SwarmAgents == 0 {
		config.SwarmAgents = defaults.SwarmAgents
	}
	if config.SwarmSteps == 0 {
		config.SwarmSteps = defaults.SwarmSteps
	}
	if config.LowReuseItems == 0 {
		config.LowReuseItems = defaults.LowReuseItems
	}
	if config.PayloadBytes == 0 {
		config.PayloadBytes = defaults.PayloadBytes
	}
	if config.LongSteps > 2_000 || config.BranchChildren > 2_000 || config.SwarmAgents > 256 || config.SwarmSteps > 256 || uint64(config.SwarmAgents)*uint64(config.SwarmSteps) > 20_000 || config.LowReuseItems > 2_000 || config.PayloadBytes > 1<<20 {
		return BenchmarkConfig{}, fmt.Errorf("%w: benchmark configuration exceeds fixed bounds", ErrInvalid)
	}
	return config, nil
}

type BenchmarkReport struct {
	SchemaVersion string             `json:"schema_version"`
	StoreFormat   string             `json:"store_format"`
	GoVersion     string             `json:"go_version"`
	GOOS          string             `json:"goos"`
	GOARCH        string             `json:"goarch"`
	Config        BenchmarkConfig    `json:"config"`
	Shapes        []BenchmarkMetrics `json:"shapes"`
}

type BenchmarkMetrics struct {
	Shape              string  `json:"shape"`
	RawDuplicatedBytes uint64  `json:"raw_duplicated_bytes"`
	StoredBytes        uint64  `json:"stored_bytes"`
	ObjectFileBytes    uint64  `json:"object_file_bytes"`
	IndexBytes         uint64  `json:"index_bytes"`
	UniqueObjects      uint64  `json:"unique_objects"`
	PrivateObjects     uint64  `json:"private_objects"`
	PortableObjects    uint64  `json:"portable_objects"`
	PutOperations      uint64  `json:"put_operations"`
	ReusedPuts         uint64  `json:"reused_puts"`
	LinkCount          uint64  `json:"link_count"`
	RootCount          uint64  `json:"root_count"`
	IngestNanoseconds  int64   `json:"ingest_nanoseconds"`
	QueryNanoseconds   int64   `json:"query_nanoseconds"`
	StorageToRawRatio  float64 `json:"storage_to_raw_ratio"`
	IndexToStoredRatio float64 `json:"index_to_stored_ratio"`
	DedupSavingsBytes  int64   `json:"dedup_savings_bytes"`
}

// RunBenchmarks creates a new destination and emits measured, machine-readable
// metrics for four deterministic synthetic shapes. It never overwrites an
// existing path and intentionally retains the stores for independent checks.
func RunBenchmarks(destination string, config BenchmarkConfig) (BenchmarkReport, error) {
	config, err := config.normalized()
	if err != nil {
		return BenchmarkReport{}, err
	}
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return BenchmarkReport{}, fmt.Errorf("%w: benchmark destination must be absolute and canonical", ErrInvalid)
	}
	if _, err := os.Lstat(destination); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return BenchmarkReport{}, fmt.Errorf("%w: benchmark destination already exists", ErrInvalid)
	}
	parent, err := os.Lstat(filepath.Dir(destination))
	if err != nil || parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir() {
		return BenchmarkReport{}, fmt.Errorf("%w: benchmark parent must be a real directory", ErrInvalid)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		return BenchmarkReport{}, fmt.Errorf("create benchmark destination: %w", err)
	}
	report := BenchmarkReport{
		SchemaVersion: BenchmarkSchemaVersion,
		StoreFormat:   ObjectSchemaVersion,
		GoVersion:     runtime.Version(),
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		Config:        config,
	}
	shapes := []struct {
		name string
		run  func(*benchmarkWriter, BenchmarkConfig) error
	}{
		{name: "long_sequential", run: benchmarkLong},
		{name: "branch_children", run: benchmarkBranches},
		{name: "shared_swarm", run: benchmarkSwarm},
		{name: "low_reuse_control", run: benchmarkLowReuse},
	}
	for _, shape := range shapes {
		metrics, err := runBenchmarkShape(filepath.Join(destination, shape.name), shape.name, config, shape.run)
		if err != nil {
			return BenchmarkReport{}, fmt.Errorf("benchmark %s: %w", shape.name, err)
		}
		report.Shapes = append(report.Shapes, metrics)
	}
	return report, nil
}

type benchmarkWriter struct {
	store      *Store
	rawBytes   uint64
	puts       uint64
	reusedPuts uint64
}

func runBenchmarkShape(path, name string, config BenchmarkConfig, fixture func(*benchmarkWriter, BenchmarkConfig) error) (BenchmarkMetrics, error) {
	store, err := Open(path, Options{MaxLinks: 20_000, MaxRoots: 4_096, MaxReachableObjects: 100_000, MaxTreeEntries: 10_000})
	if err != nil {
		return BenchmarkMetrics{}, err
	}
	defer store.Close()
	writer := &benchmarkWriter{store: store}
	ingestStarted := time.Now()
	if err := fixture(writer, config); err != nil {
		return BenchmarkMetrics{}, err
	}
	ingestDuration := time.Since(ingestStarted)
	queryStarted := time.Now()
	if _, err := store.PlanRetention(); err != nil {
		return BenchmarkMetrics{}, err
	}
	stats, err := store.Stats()
	if err != nil {
		return BenchmarkMetrics{}, err
	}
	queryDuration := time.Since(queryStarted)
	metrics := BenchmarkMetrics{
		Shape: name, RawDuplicatedBytes: writer.rawBytes, StoredBytes: stats.StoredBytes,
		ObjectFileBytes: stats.ObjectFileBytes, IndexBytes: stats.IndexBytes,
		UniqueObjects: stats.ObjectCount, PrivateObjects: stats.PrivateObjects,
		PortableObjects: stats.PortableObjects, PutOperations: writer.puts,
		ReusedPuts: writer.reusedPuts, LinkCount: stats.LinkCount, RootCount: stats.RootCount,
		IngestNanoseconds: ingestDuration.Nanoseconds(), QueryNanoseconds: queryDuration.Nanoseconds(),
	}
	if metrics.RawDuplicatedBytes != 0 {
		metrics.StorageToRawRatio = float64(metrics.StoredBytes) / float64(metrics.RawDuplicatedBytes)
	}
	if metrics.StoredBytes != 0 {
		metrics.IndexToStoredRatio = float64(metrics.IndexBytes) / float64(metrics.StoredBytes)
	}
	metrics.DedupSavingsBytes = saturatingDifference(metrics.RawDuplicatedBytes, metrics.StoredBytes)
	return metrics, nil
}

func (writer *benchmarkWriter) put(kind Kind, body []byte, links ...Ref) (Ref, error) {
	writer.rawBytes += uint64(len(body))
	writer.puts++
	options := PutOptions{Privacy: PrivacyPrivate, Credentials: CredentialsAbsent, Links: links}
	var ref Ref
	var created bool
	var err error
	if structuredKind(kind) {
		ref, created, err = writer.store.PutJSON(kind, body, options)
	} else {
		ref, created, err = writer.store.Put(kind, body, options)
	}
	if err != nil {
		return Ref{}, err
	}
	if !created {
		writer.reusedPuts++
	}
	return ref, nil
}

func (writer *benchmarkWriter) putWorkspace(entries []WorkspaceEntry) (Ref, error) {
	writer.puts++
	ref, created, err := writer.store.PutWorkspaceTree(entries, PutOptions{Privacy: PrivacyPrivate, Credentials: CredentialsAbsent})
	if err != nil {
		return Ref{}, err
	}
	object, err := writer.store.Get(ref)
	if err != nil {
		return Ref{}, err
	}
	writer.rawBytes += uint64(len(object.Body))
	if !created {
		writer.reusedPuts++
	}
	return ref, nil
}

func (writer *benchmarkWriter) putBranch(branch Branch) (Ref, error) {
	writer.puts++
	ref, created, err := writer.store.PutBranch(branch, PutOptions{Privacy: PrivacyPrivate, Credentials: CredentialsAbsent})
	if err != nil {
		return Ref{}, err
	}
	object, err := writer.store.Get(ref)
	if err != nil {
		return Ref{}, err
	}
	writer.rawBytes += uint64(len(object.Body))
	if !created {
		writer.reusedPuts++
	}
	return ref, nil
}

func benchmarkLong(writer *benchmarkWriter, config BenchmarkConfig) error {
	prompt := fixturePayload("long-prompt", config.PayloadBytes)
	code := fixturePayload("long-code", config.PayloadBytes)
	tool := fixtureJSONPayload("long-tool", config.PayloadBytes)
	file := fixturePayload("long-file", config.PayloadBytes)
	events := make([]Ref, 0, config.LongSteps)
	for index := uint32(0); index < config.LongSteps; index++ {
		promptRef, err := writer.put(KindPrompt, prompt)
		if err != nil {
			return err
		}
		codeRef, err := writer.put(KindCode, code)
		if err != nil {
			return err
		}
		toolRef, err := writer.put(KindToolPayload, tool)
		if err != nil {
			return err
		}
		fileRef, err := writer.put(KindFile, file)
		if err != nil {
			return err
		}
		event, err := writer.put(KindMetadataEvent, fixtureDocument("long-event", index), promptRef, codeRef, toolRef, fileRef)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	run, err := writer.put(KindRun, []byte(`{"shape":"long_sequential"}`), events...)
	if err != nil {
		return err
	}
	return writer.store.Pin("long.run", run)
}

func benchmarkBranches(writer *benchmarkWriter, config BenchmarkConfig) error {
	promptBody := fixturePayload("branch-prompt", config.PayloadBytes)
	codeBody := fixturePayload("branch-code", config.PayloadBytes)
	fileBody := fixturePayload("branch-workspace", config.PayloadBytes)
	prefixBody := fixtureJSONPayload("branch-prefix", config.PayloadBytes)
	prompt, err := writer.put(KindPrompt, promptBody)
	if err != nil {
		return err
	}
	code, err := writer.put(KindCode, codeBody)
	if err != nil {
		return err
	}
	file, err := writer.put(KindFile, fileBody)
	if err != nil {
		return err
	}
	workspace, err := writer.putWorkspace([]WorkspaceEntry{{Path: "shared/input.txt", Content: file}})
	if err != nil {
		return err
	}
	prefix, err := writer.put(KindToolPayload, prefixBody)
	if err != nil {
		return err
	}
	parent, err := writer.put(KindRun, []byte(`{"run":"parent"}`), prompt, code, workspace, prefix)
	if err != nil {
		return err
	}
	for index := uint32(0); index < config.BranchChildren; index++ {
		if _, err := writer.put(KindPrompt, promptBody); err != nil {
			return err
		}
		if _, err := writer.put(KindCode, codeBody); err != nil {
			return err
		}
		if _, err := writer.put(KindFile, fileBody); err != nil {
			return err
		}
		if _, err := writer.put(KindToolPayload, prefixBody); err != nil {
			return err
		}
		manifest, err := writer.put(KindSemanticDocument, fixtureDocument("branch-manifest", index))
		if err != nil {
			return err
		}
		execution, err := writer.put(KindExecution, fixtureDocument("branch-execution", index), prompt, code)
		if err != nil {
			return err
		}
		branch, err := writer.putBranch(Branch{ParentRun: parent, ChildExecution: execution, ForkOperation: index, Prefix: prefix, InitialWorkspace: workspace, Manifest: manifest})
		if err != nil {
			return err
		}
		if err := writer.store.Pin(fmt.Sprintf("branch.%04d", index), branch); err != nil {
			return err
		}
	}
	return nil
}

func benchmarkSwarm(writer *benchmarkWriter, config BenchmarkConfig) error {
	promptBody := fixturePayload("swarm-prompt", config.PayloadBytes)
	codeBody := fixturePayload("swarm-code", config.PayloadBytes)
	fileBody := fixturePayload("swarm-file", config.PayloadBytes)
	for agent := uint32(0); agent < config.SwarmAgents; agent++ {
		events := make([]Ref, 0, config.SwarmSteps)
		for step := uint32(0); step < config.SwarmSteps; step++ {
			prompt, err := writer.put(KindPrompt, promptBody)
			if err != nil {
				return err
			}
			code, err := writer.put(KindCode, codeBody)
			if err != nil {
				return err
			}
			file, err := writer.put(KindFile, fileBody)
			if err != nil {
				return err
			}
			event, err := writer.put(KindMetadataEvent, fixtureAgentDocument(agent, step), prompt, code, file)
			if err != nil {
				return err
			}
			events = append(events, event)
		}
		run, err := writer.put(KindRun, fixtureDocument("swarm-run", agent), events...)
		if err != nil {
			return err
		}
		if err := writer.store.Pin(fmt.Sprintf("swarm.%04d", agent), run); err != nil {
			return err
		}
	}
	return nil
}

func benchmarkLowReuse(writer *benchmarkWriter, config BenchmarkConfig) error {
	events := make([]Ref, 0, config.LowReuseItems)
	for index := uint32(0); index < config.LowReuseItems; index++ {
		prompt, err := writer.put(KindPrompt, fixturePayload(fmt.Sprintf("low-prompt-%d", index), config.PayloadBytes))
		if err != nil {
			return err
		}
		code, err := writer.put(KindCode, fixturePayload(fmt.Sprintf("low-code-%d", index), config.PayloadBytes))
		if err != nil {
			return err
		}
		provider, err := writer.put(KindProviderBody, fixturePayload(fmt.Sprintf("low-provider-%d", index), config.PayloadBytes))
		if err != nil {
			return err
		}
		file, err := writer.put(KindFile, fixturePayload(fmt.Sprintf("low-file-%d", index), config.PayloadBytes))
		if err != nil {
			return err
		}
		event, err := writer.put(KindMetadataEvent, fixtureDocument("low-event", index), prompt, code, provider, file)
		if err != nil {
			return err
		}
		events = append(events, event)
	}
	run, err := writer.put(KindRun, []byte(`{"shape":"low_reuse_control"}`), events...)
	if err != nil {
		return err
	}
	return writer.store.Pin("low.run", run)
}

func fixturePayload(label string, size uint32) []byte {
	digest := sha256.Sum256([]byte(label))
	seed := hex.EncodeToString(digest[:])
	var builder strings.Builder
	builder.Grow(int(size))
	for builder.Len() < int(size) {
		builder.WriteString(seed)
	}
	return []byte(builder.String()[:size])
}

func fixtureJSONPayload(label string, size uint32) []byte {
	body, _ := json.Marshal(struct {
		ID      string `json:"id"`
		Payload string `json:"payload"`
	}{ID: label, Payload: string(fixturePayload(label+"-body", size))})
	return body
}

func fixtureDocument(label string, index uint32) []byte {
	body, _ := json.Marshal(struct {
		ID    string `json:"id"`
		Index uint32 `json:"index"`
	}{ID: label, Index: index})
	return body
}

func fixtureAgentDocument(agent, step uint32) []byte {
	body, _ := json.Marshal(struct {
		Agent uint32 `json:"agent"`
		Step  uint32 `json:"step"`
	}{Agent: agent, Step: step})
	return body
}

func saturatingDifference(raw, stored uint64) int64 {
	if raw >= stored {
		difference := raw - stored
		if difference > uint64(^uint64(0)>>1) {
			return int64(^uint64(0) >> 1)
		}
		return int64(difference)
	}
	difference := stored - raw
	if difference > uint64(^uint64(0)>>1) {
		return -int64(^uint64(0) >> 1)
	}
	return -int64(difference)
}
