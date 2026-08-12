// Command pysolate-research provides bounded local research inspection and
// planning without moving Agent orchestration or source authority into Runtime.
package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/research/evaluationlab"
	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
	"github.com/bkmashiro/agent-python-runtime/research/operator"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

const (
	maxOutputBytes     = 4 << 20
	maxPathBytes       = 4096
	defaultCalls       = 64
	maximumCalls       = 256
	maxOverrideBytes   = 1 << 20
	maxRelations       = 1024
	maxJSONNodes       = 65536
	maxDiagnosticBytes = 64 << 10
)

const branchPlanSchemaVersion = "pysolate.research-branch-plan.v1"

type commandError struct {
	err   error
	usage bool
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr))
}

func execute(arguments []string, stdout, stderr io.Writer) int {
	if stdout == nil || stderr == nil {
		return 1
	}
	if len(arguments) == 0 {
		_ = writeBounded(stderr, []byte(helpText()))
		return 2
	}
	if arguments[0] == "help" || arguments[0] == "-help" || arguments[0] == "--help" {
		_ = writeBounded(stdout, []byte(helpText()))
		return 0
	}
	var failure commandError
	switch arguments[0] {
	case "inspect":
		failure = inspectCommand(arguments[1:], stdout, stderr)
	case "compare":
		failure = compareCommand(arguments[1:], stdout, stderr)
	case "branch":
		failure = branchCommand(arguments[1:], stdout, stderr)
	case "store":
		failure = storeCommand(arguments[1:], stdout, stderr)
	case "lab":
		failure = labCommand(arguments[1:], stdout, stderr)
	default:
		failure = commandError{err: fmt.Errorf("unknown command %q", boundedLabel(arguments[0])), usage: true}
	}
	if failure.err == nil {
		return 0
	}
	diagnostic := []byte(fmt.Sprintf("pysolate-research: %v\n", failure.err))
	if len(diagnostic) > maxDiagnosticBytes {
		diagnostic = []byte("pysolate-research: command failed with an oversized diagnostic\n")
	}
	_ = writeBounded(stderr, diagnostic)
	if failure.usage {
		return 2
	}
	return 1
}

func labCommand(arguments []string, stdout, stderr io.Writer) commandError {
	if len(arguments) == 0 || arguments[0] != "project" {
		return commandError{err: errors.New("lab requires project"), usage: true}
	}
	flags := newFlagSet("lab project", stderr)
	var reportPath, rowID, kindValue string
	flags.StringVar(&reportPath, "report", "", "absolute protected canonical evaluation report path")
	flags.StringVar(&rowID, "row", "", "exact evaluation row identity")
	flags.StringVar(&kindValue, "kind", "", "Lab v1 document kind")
	if err := flags.Parse(arguments[1:]); err != nil {
		return commandError{err: err, usage: true}
	}
	if flags.NArg() != 0 || reportPath == "" || rowID == "" || kindValue == "" {
		return commandError{err: errors.New("lab project requires -report, -row and -kind"), usage: true}
	}
	kind := labview.Kind(kindValue)
	valid := false
	for _, candidate := range labview.AllKinds() {
		valid = valid || candidate == kind
	}
	if !valid {
		return commandError{err: errors.New("unknown Lab v1 document kind"), usage: true}
	}
	raw, err := readProtectedFile(reportPath, labview.MaxDocumentBytes)
	if err != nil {
		return commandError{err: fmt.Errorf("read evaluation report: %w", err)}
	}
	set, err := evaluationlab.Project(raw, rowID)
	if err != nil {
		return commandError{err: fmt.Errorf("project evaluation report: %w", err)}
	}
	var value any
	switch kind {
	case labview.KindIndex:
		value = set.Index
	case labview.KindStudySummary:
		value = set.Study
	case labview.KindRunDetail:
		value = set.Run
	case labview.KindTimelinePage:
		value = set.Timeline
	case labview.KindBranchDAG:
		value = set.DAG
	case labview.KindWorkspaceDiff:
		value = set.Workspace
	case labview.KindRunComparison:
		value = set.Comparison
	case labview.KindObjectRef:
		value = set.Refs
	case labview.KindProblem:
		value = set.Problem
	}
	encoded, _, err := labview.Encode(kind, value)
	if err != nil {
		return commandError{err: fmt.Errorf("encode Lab v1 document: %w", err)}
	}
	if err := writeBounded(stdout, encoded); err != nil {
		return commandError{err: err}
	}
	return commandError{}
}

func inspectCommand(arguments []string, stdout, stderr io.Writer) commandError {
	flags := newFlagSet("inspect", stderr)
	var bundlePath string
	var maximum uint
	var jsonOutput bool
	flags.StringVar(&bundlePath, "bundle", "", "absolute protected Playback Bundle path")
	flags.UintVar(&maximum, "max-calls", defaultCalls, "maximum source calls to summarize (1..256)")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	if flags.NArg() != 0 || bundlePath == "" || maximum == 0 || maximum > maximumCalls {
		return commandError{err: errors.New("inspect requires -bundle and -max-calls in 1..256"), usage: true}
	}
	bundle, err := readBundle(bundlePath)
	if err != nil {
		return commandError{err: fmt.Errorf("inspect bundle: %w", err)}
	}
	summary := operator.InspectBundle(bundle, uint32(maximum))
	if jsonOutput {
		return outputJSON(stdout, summary)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "bundle: %s\nstatus: %s\nsource calls: %d\n", shortIdentity(summary.BundleSHA256), summary.Status, summary.SourceCalls)
	if len(summary.Capabilities) == 0 {
		output.WriteString("capabilities: none\n")
	} else {
		output.WriteString("capabilities:\n")
		for _, capability := range summary.Capabilities {
			fmt.Fprintf(&output, "  - %s\n", capability)
		}
	}
	if summary.CallsTruncated {
		output.WriteString("calls: truncated\n")
	}
	fmt.Fprintf(&output, "workspace: %s\n", presentWord(summary.HasWorkspace))
	return outputHuman(stdout, output.String())
}

func compareCommand(arguments []string, stdout, stderr io.Writer) commandError {
	flags := newFlagSet("compare", stderr)
	var leftPath, rightPath string
	var maximum uint
	var jsonOutput bool
	flags.StringVar(&leftPath, "left", "", "absolute protected left Playback Bundle path")
	flags.StringVar(&rightPath, "right", "", "absolute protected right Playback Bundle path")
	flags.UintVar(&maximum, "max-calls", defaultCalls, "maximum source calls to compare (1..256)")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	if flags.NArg() != 0 || leftPath == "" || rightPath == "" || maximum == 0 || maximum > maximumCalls {
		return commandError{err: errors.New("compare requires -left, -right and -max-calls in 1..256"), usage: true}
	}
	left, err := readBundle(leftPath)
	if err != nil {
		return commandError{err: fmt.Errorf("read left bundle: %w", err)}
	}
	right, err := readBundle(rightPath)
	if err != nil {
		return commandError{err: fmt.Errorf("read right bundle: %w", err)}
	}
	comparison := operator.CompareBundles(left, right, uint32(maximum))
	if jsonOutput {
		return outputJSON(stdout, comparison)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "left: %s\nright: %s\n", shortIdentity(comparison.LeftBundleSHA256), shortIdentity(comparison.RightBundleSHA256))
	fmt.Fprintf(&output, "status: %s\nresult: %s\nplan: %s\nartifact: %s\n", sameWord(comparison.SameStatus), sameWord(comparison.SameResult), sameWord(comparison.SamePlan), sameWord(comparison.SameArtifact))
	fmt.Fprintf(&output, "initial workspace: %s\nfinal workspace: %s\n", sameWord(comparison.SameInitialWorkspace), sameWord(comparison.SameFinalWorkspace))
	fmt.Fprintf(&output, "source call differences: %d of %d", comparison.CallDifferences, comparison.ComparedCalls)
	if comparison.CallComparisonTruncated {
		output.WriteString(" (truncated)")
	}
	output.WriteByte('\n')
	return outputHuman(stdout, output.String())
}

func branchCommand(arguments []string, stdout, stderr io.Writer) commandError {
	if len(arguments) == 0 {
		return commandError{err: errors.New("branch requires plan or dag; execution uses research/operator.RunBranch with an explicitly sealed Host source plan"), usage: true}
	}
	switch arguments[0] {
	case "plan":
		return branchPlanCommand(arguments[1:], stdout, stderr)
	case "dag":
		return branchDAGCommand(arguments[1:], stdout, stderr)
	case "run":
		return commandError{err: errors.New("branch run is available only through research/operator.RunBranch with a fresh Guest and explicitly sealed Host Plan, Grants and source handlers"), usage: true}
	default:
		return commandError{err: fmt.Errorf("unknown branch subcommand %q", boundedLabel(arguments[0])), usage: true}
	}
}

func storeCommand(arguments []string, stdout, stderr io.Writer) commandError {
	if len(arguments) == 0 {
		return commandError{err: errors.New("store requires stats or benchmark"), usage: true}
	}
	switch arguments[0] {
	case "stats":
		return storeStatsCommand(arguments[1:], stdout, stderr)
	case "benchmark":
		return storeBenchmarkCommand(arguments[1:], stdout, stderr)
	default:
		return commandError{err: fmt.Errorf("unknown store subcommand %q", boundedLabel(arguments[0])), usage: true}
	}
}

func storeStatsCommand(arguments []string, stdout, stderr io.Writer) commandError {
	flags := newFlagSet("store stats", stderr)
	var rootPath string
	var jsonOutput bool
	flags.StringVar(&rootPath, "root", "", "absolute existing research-store root")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	if flags.NArg() != 0 || rootPath == "" {
		return commandError{err: errors.New("store stats requires -root"), usage: true}
	}
	store, err := labstore.Open(rootPath, labstore.Options{ReadOnly: true})
	if err != nil {
		return commandError{err: fmt.Errorf("open read-only store: %w", err)}
	}
	stats, statsErr := store.Stats()
	closeErr := store.Close()
	if err := errors.Join(statsErr, closeErr); err != nil {
		return commandError{err: fmt.Errorf("inspect store: %w", err)}
	}
	if jsonOutput {
		return outputJSON(stdout, stats)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "objects: %d (%d private, %d portable)\nroots: %d\nlinks: %d\n", stats.ObjectCount, stats.PrivateObjects, stats.PortableObjects, stats.RootCount, stats.LinkCount)
	fmt.Fprintf(&output, "logical bodies: %d bytes\nobject files: %d bytes\nindexes: %d bytes\nstored: %d bytes\n", stats.LogicalBodyBytes, stats.ObjectFileBytes, stats.IndexBytes, stats.StoredBytes)
	return outputHuman(stdout, output.String())
}

func storeBenchmarkCommand(arguments []string, stdout, stderr io.Writer) commandError {
	defaults := labstore.DefaultBenchmarkConfig()
	flags := newFlagSet("store benchmark", stderr)
	var rootPath string
	longSteps := uint64(defaults.LongSteps)
	branchChildren := uint64(defaults.BranchChildren)
	swarmAgents := uint64(defaults.SwarmAgents)
	swarmSteps := uint64(defaults.SwarmSteps)
	lowReuseItems := uint64(defaults.LowReuseItems)
	payloadBytes := uint64(defaults.PayloadBytes)
	var jsonOutput bool
	flags.StringVar(&rootPath, "root", "", "new absolute benchmark destination")
	flags.Uint64Var(&longSteps, "long-steps", longSteps, "long sequential step count")
	flags.Uint64Var(&branchChildren, "branch-children", branchChildren, "branch child count")
	flags.Uint64Var(&swarmAgents, "swarm-agents", swarmAgents, "swarm agent count")
	flags.Uint64Var(&swarmSteps, "swarm-steps", swarmSteps, "steps per swarm agent")
	flags.Uint64Var(&lowReuseItems, "low-reuse-items", lowReuseItems, "low-reuse control item count")
	flags.Uint64Var(&payloadBytes, "payload-bytes", payloadBytes, "synthetic body byte count")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	values := []uint64{longSteps, branchChildren, swarmAgents, swarmSteps, lowReuseItems, payloadBytes}
	if flags.NArg() != 0 || rootPath == "" {
		return commandError{err: errors.New("store benchmark requires -root and bounded fixture flags"), usage: true}
	}
	for _, value := range values {
		if value > uint64(^uint32(0)) {
			return commandError{err: errors.New("benchmark numeric flag exceeds uint32"), usage: true}
		}
	}
	report, err := labstore.RunBenchmarks(rootPath, labstore.BenchmarkConfig{
		LongSteps: uint32(longSteps), BranchChildren: uint32(branchChildren),
		SwarmAgents: uint32(swarmAgents), SwarmSteps: uint32(swarmSteps),
		LowReuseItems: uint32(lowReuseItems), PayloadBytes: uint32(payloadBytes),
	})
	if err != nil {
		return commandError{err: fmt.Errorf("run store benchmark: %w", err)}
	}
	if jsonOutput {
		return outputJSON(stdout, report)
	}
	var output strings.Builder
	output.WriteString("shape                 raw bytes  stored bytes  objects  reused puts  storage/raw\n")
	for _, metrics := range report.Shapes {
		fmt.Fprintf(&output, "%-21s %9d  %12d  %7d  %11d  %.4f\n", metrics.Shape, metrics.RawDuplicatedBytes, metrics.StoredBytes, metrics.UniqueObjects, metrics.ReusedPuts, metrics.StorageToRawRatio)
	}
	return outputHuman(stdout, output.String())
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(&cappedWriter{destination: stderr, remaining: maxDiagnosticBytes})
	return flags
}

type cappedWriter struct {
	destination io.Writer
	remaining   int
}

func (writer *cappedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if writer.remaining <= 0 {
		return original, nil
	}
	if len(value) > writer.remaining {
		value = value[:writer.remaining]
	}
	written, err := writer.destination.Write(value)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	if written != len(value) {
		return written, io.ErrShortWrite
	}
	return original, nil
}

func readBundle(path string) (playback.Bundle, error) {
	raw, err := readProtectedFile(path, playback.MaxEncodedBytes)
	if err != nil {
		return playback.Bundle{}, err
	}
	bundle, err := playback.Decode(raw)
	if err != nil {
		return playback.Bundle{}, err
	}
	return bundle, nil
}

func readProtectedFile(path string, maximum int) ([]byte, error) {
	if len(path) == 0 || len(path) > maxPathBytes || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("path must be absolute, canonical and bounded")
	}
	if err := rejectSymlinkDirectoryComponents(filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > int64(maximum) {
		return nil, errors.New("input must be a bounded protected regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("input changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > maximum || int64(len(raw)) != opened.Size() {
		return nil, errors.New("input size is outside bounds")
	}
	return raw, nil
}

type branchPlanReport struct {
	SchemaVersion             string `json:"schema_version"`
	BranchSHA256              string `json:"branch_sha256"`
	ParentBundleSHA256        string `json:"parent_bundle_sha256"`
	ForkOperation             uint32 `json:"fork_operation"`
	ForkCapability            string `json:"fork_capability"`
	SuffixMode                string `json:"suffix_mode"`
	SuffixOperations          uint32 `json:"suffix_operations"`
	ChildCapabilityPlanSHA256 string `json:"child_capability_plan_sha256"`
}

func branchPlanCommand(arguments []string, stdout, stderr io.Writer) commandError {
	flags := newFlagSet("branch plan", stderr)
	var parentPath, modeValue, suffixBundlePath, childBundlePath, outputPath string
	var fork uint64
	var overrideResults stringList
	var jsonOutput bool
	flags.StringVar(&parentPath, "parent", "", "absolute protected parent Playback Bundle path")
	flags.Uint64Var(&fork, "fork", 0, "zero-based capability operation at which the child suffix begins")
	flags.StringVar(&modeValue, "mode", string(playback.BranchOverride), "override, recorded_suffix or live_suffix")
	flags.Var(&overrideResults, "override-result", "protected JSON result file; repeat for consecutive override operations")
	flags.StringVar(&suffixBundlePath, "suffix-bundle", "", "protected alternate Bundle supplying a recorded suffix")
	flags.StringVar(&childBundlePath, "child-binding-bundle", "", "protected Bundle supplying the already sealed child Plan and Grants")
	flags.StringVar(&outputPath, "output", "", "new absolute manifest path; atomically published 0600 without overwrite")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	forkSet := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "fork" {
			forkSet = true
		}
	})
	if flags.NArg() != 0 || parentPath == "" || outputPath == "" || !forkSet || fork > uint64(^uint32(0)) {
		return commandError{err: errors.New("branch plan requires -parent, -fork and -output"), usage: true}
	}
	parent, err := readBundle(parentPath)
	if err != nil {
		return commandError{err: fmt.Errorf("read parent bundle: %w", err)}
	}
	forkOperation := uint32(fork)
	if forkOperation >= uint32(len(parent.Entries)) {
		return commandError{err: errors.New("fork operation is outside the parent transcript")}
	}
	mode := playback.BranchSuffixMode(modeValue)
	authority := parent
	if childBundlePath != "" {
		authority, err = readCompatibleBundle(childBundlePath, parent)
		if err != nil {
			return commandError{err: fmt.Errorf("read child binding bundle: %w", err)}
		}
	}
	var suffix []capability.TranscriptEntry
	switch mode {
	case playback.BranchOverride:
		if len(overrideResults) == 0 || suffixBundlePath != "" || len(overrideResults) > maximumCalls || uint64(forkOperation)+uint64(len(overrideResults)) > uint64(len(parent.Entries)) {
			return commandError{err: errors.New("override mode requires 1..256 -override-result files within the parent transcript and no -suffix-bundle"), usage: true}
		}
		for index, resultPath := range overrideResults {
			result, resultErr := readCanonicalJSON(resultPath, maxOverrideBytes)
			if resultErr != nil {
				return commandError{err: fmt.Errorf("read override result %d: %w", index, resultErr)}
			}
			operation := forkOperation + uint32(index)
			parentEntry := parent.Entries[operation]
			suffix = append(suffix, capability.TranscriptEntry{
				OperationIndex: operation, Capability: parentEntry.Capability,
				Arguments: append(json.RawMessage(nil), parentEntry.Arguments...), ArgumentsSHA256: parentEntry.ArgumentsSHA256,
				Result: append(json.RawMessage(nil), result...), ResultSHA256: playback.SHA256(result),
				Evidence: capability.TransportEvidence{
					Kind: "branch_override", Status: 200, MediaType: "application/json",
					BodyBytes: uint32(len(result)), BodySHA256: playback.SHA256(result),
				},
			})
		}
	case playback.BranchRecordedSuffix:
		if len(overrideResults) != 0 || suffixBundlePath == "" {
			return commandError{err: errors.New("recorded_suffix mode requires -suffix-bundle and no -override-result"), usage: true}
		}
		alternate, alternateErr := readCompatibleBundle(suffixBundlePath, parent)
		if alternateErr != nil {
			return commandError{err: fmt.Errorf("read suffix bundle: %w", alternateErr)}
		}
		if childBundlePath == "" {
			authority = alternate
		}
		for _, entry := range alternate.Entries {
			if entry.OperationIndex >= forkOperation {
				suffix = append(suffix, cloneTranscriptEntry(entry))
			}
		}
		if len(suffix) == 0 {
			return commandError{err: errors.New("suffix bundle contains no operation at or after the fork")}
		}
	case playback.BranchLiveSuffix:
		if len(overrideResults) != 0 || suffixBundlePath != "" {
			return commandError{err: errors.New("live_suffix mode cannot contain recorded results"), usage: true}
		}
	default:
		return commandError{err: errors.New("invalid branch suffix mode"), usage: true}
	}
	manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: forkOperation,
		RequestSHA256: parent.RequestSHA256, ArtifactSHA256: parent.ArtifactSHA256,
		ExecutionProfileSHA256: parent.ExecutionProfileSHA256, InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256,
		ChildCapabilityPlanSHA256: authority.CapabilityPlanSHA256, ChildGrants: authority.Grants, SuffixMode: mode,
	}, parent, suffix)
	if err != nil {
		return commandError{err: fmt.Errorf("author branch manifest: %w", err)}
	}
	encoded, err := playback.EncodeBranchManifest(manifest)
	if err != nil {
		return commandError{err: err}
	}
	decoded, err := playback.DecodeBranchManifest(encoded)
	if err != nil || decoded.ValidateParent(parent) != nil {
		return commandError{err: errors.New("authored branch manifest failed canonical validation")}
	}
	if err := publishProtectedNoOverwrite(outputPath, encoded); err != nil {
		return commandError{err: fmt.Errorf("publish branch manifest: %w", err)}
	}
	report := branchPlanReport{
		SchemaVersion: branchPlanSchemaVersion, BranchSHA256: manifest.Identity, ParentBundleSHA256: parent.Identity,
		ForkOperation: forkOperation, ForkCapability: parent.Entries[forkOperation].Capability,
		SuffixMode: string(mode), SuffixOperations: uint32(len(suffix)), ChildCapabilityPlanSHA256: manifest.ChildCapabilityPlanSHA256,
	}
	if jsonOutput {
		return outputJSON(stdout, report)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "branch: %s\nparent: %s\nfork: operation %d (%s)\nsuffix: %s (%d recorded operations)\npublished: protected local manifest\n",
		shortIdentity(report.BranchSHA256), shortIdentity(report.ParentBundleSHA256), report.ForkOperation,
		report.ForkCapability, report.SuffixMode, report.SuffixOperations)
	return outputHuman(stdout, output.String())
}

func branchDAGCommand(arguments []string, stdout, stderr io.Writer) commandError {
	flags := newFlagSet("branch dag", stderr)
	var parentPath string
	var manifestPaths, childPaths stringList
	var maximum uint
	var jsonOutput bool
	flags.StringVar(&parentPath, "parent", "", "absolute protected parent Playback Bundle path")
	flags.Var(&manifestPaths, "manifest", "protected Branch Manifest path; pair by order with -child")
	flags.Var(&childPaths, "child", "protected child Playback Bundle path; pair by order with -manifest")
	flags.UintVar(&maximum, "max-nodes", 64, "maximum DAG nodes (1..1024)")
	flags.BoolVar(&jsonOutput, "json", false, "emit bounded machine-readable JSON")
	if err := flags.Parse(arguments); err != nil {
		return commandError{err: err, usage: true}
	}
	if flags.NArg() != 0 || parentPath == "" || len(manifestPaths) != len(childPaths) || len(manifestPaths) > maxRelations || maximum == 0 || maximum > maxRelations {
		return commandError{err: errors.New("branch dag requires -parent, paired -manifest/-child values, and -max-nodes in 1..1024"), usage: true}
	}
	parent, err := readBundle(parentPath)
	if err != nil {
		return commandError{err: fmt.Errorf("read DAG parent: %w", err)}
	}
	relations := make([]operator.ChildRelation, 0, len(manifestPaths))
	seenManifest := make(map[string]struct{}, len(manifestPaths))
	seenChild := make(map[string]struct{}, len(childPaths))
	for index := range manifestPaths {
		manifest, manifestErr := readBranchManifest(manifestPaths[index])
		if manifestErr != nil {
			return commandError{err: fmt.Errorf("read manifest %d: %w", index, manifestErr)}
		}
		child, childErr := readBundle(childPaths[index])
		if childErr != nil {
			return commandError{err: fmt.Errorf("read child %d: %w", index, childErr)}
		}
		if _, duplicate := seenManifest[manifest.Identity]; duplicate {
			return commandError{err: errors.New("duplicate branch manifest relation")}
		}
		if _, duplicate := seenChild[child.Identity]; duplicate {
			return commandError{err: errors.New("duplicate child bundle relation")}
		}
		seenManifest[manifest.Identity] = struct{}{}
		seenChild[child.Identity] = struct{}{}
		relations = append(relations, operator.ChildRelation{Manifest: manifest, Child: child})
	}
	dag, err := operator.ExportBranchDAG(parent, relations, uint32(maximum))
	if err != nil {
		return commandError{err: err}
	}
	if jsonOutput {
		return outputJSON(stdout, dag)
	}
	var output strings.Builder
	fmt.Fprintf(&output, "branch DAG: %d nodes, %d edges\nparent: %s\n", len(dag.Nodes), len(dag.Edges), shortIdentity(parent.Identity))
	for _, edge := range dag.Edges {
		fmt.Fprintf(&output, "  %s -> %s at operation %d (%s)\n", shortIdentity(edge.ParentBundleSHA256), shortIdentity(edge.ChildBundleSHA256), edge.ForkOperation, edge.SuffixMode)
	}
	if dag.Truncated {
		output.WriteString("nodes: truncated\n")
	}
	return outputHuman(stdout, output.String())
}

func readCompatibleBundle(path string, parent playback.Bundle) (playback.Bundle, error) {
	bundle, err := readBundle(path)
	if err != nil {
		return playback.Bundle{}, err
	}
	if bundle.RequestSHA256 != parent.RequestSHA256 || bundle.ArtifactSHA256 != parent.ArtifactSHA256 ||
		bundle.ExecutionProfileSHA256 != parent.ExecutionProfileSHA256 || bundle.InitialWorkspaceSHA256 != parent.InitialWorkspaceSHA256 {
		return playback.Bundle{}, errors.New("bundle request, artifact, profile or initial workspace does not match parent")
	}
	return bundle, nil
}

func readBranchManifest(path string) (playback.BranchManifest, error) {
	raw, err := readProtectedFile(path, playback.MaxBranchBytes)
	if err != nil {
		return playback.BranchManifest{}, err
	}
	return playback.DecodeBranchManifest(raw)
}

func cloneTranscriptEntry(entry capability.TranscriptEntry) capability.TranscriptEntry {
	entry.Arguments = append(json.RawMessage(nil), entry.Arguments...)
	entry.Result = append(json.RawMessage(nil), entry.Result...)
	return entry
}

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }

func (values *stringList) Set(value string) error {
	if len(*values) >= maxRelations || value == "" || len(value) > maxPathBytes {
		return errors.New("too many or invalid repeated path flags")
	}
	*values = append(*values, value)
	return nil
}

func readCanonicalJSON(path string, maximum int) (json.RawMessage, error) {
	raw, err := readProtectedFile(path, maximum)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return nil, errors.New("input is not strict JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("input is not JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("input contains trailing JSON")
	}
	canonical, err := json.Marshal(document)
	if err != nil || len(canonical) == 0 || len(canonical) > maximum {
		return nil, errors.New("canonical JSON exceeds fixed bound")
	}
	return canonical, nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains trailing data")
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxJSONNodes {
		return errors.New("JSON exceeds complexity bounds")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return errors.New("invalid JSON object")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("duplicate JSON key")
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	return nil
}

func publishProtectedNoOverwrite(path string, value []byte) error {
	if len(path) == 0 || len(path) > maxPathBytes || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(value) == 0 || len(value) > playback.MaxBranchBytes {
		return errors.New("output path or value is outside fixed bounds")
	}
	parent := filepath.Dir(path)
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return errors.New("invalid output filename")
	}
	if err := rejectSymlinkDirectoryComponents(parent); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil || parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o077 != 0 {
		if err != nil {
			return fmt.Errorf("output parent must be an existing protected real directory: %w", err)
		}
		return fmt.Errorf("output parent must be an existing protected real directory (mode %04o)", parentInfo.Mode().Perm())
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(base); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return errors.New("output already exists or cannot be inspected")
	}
	stageName, stage, err := createProtectedStage(root, base)
	if err != nil {
		return err
	}
	stageOpen := true
	defer func() {
		if stageOpen {
			_ = stage.Close()
		}
		_ = root.Remove(stageName)
	}()
	if _, err := io.Copy(stage, bytes.NewReader(value)); err != nil {
		return err
	}
	if err := stage.Sync(); err != nil {
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	stageOpen = false
	if err := root.Link(stageName, base); err != nil {
		return err
	}
	stageInfo, stageErr := root.Lstat(stageName)
	destinationInfo, destinationErr := root.Lstat(base)
	if stageErr != nil || destinationErr != nil || !os.SameFile(stageInfo, destinationInfo) || !destinationInfo.Mode().IsRegular() || destinationInfo.Mode().Perm() != 0o600 {
		return errors.New("published output failed protected-file validation")
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func createProtectedStage(root *os.Root, base string) (string, *os.File, error) {
	for attempt := 0; attempt < 16; attempt++ {
		var random [16]byte
		if _, err := cryptorand.Read(random[:]); err != nil {
			return "", nil, err
		}
		name := "." + base + ".stage-" + hex.EncodeToString(random[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return name, file, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", nil, err
		}
	}
	return "", nil, errors.New("could not allocate an exclusive output stage")
}

func rejectSymlinkDirectoryComponents(path string) error {
	current := path
	first := true
	for {
		info, err := os.Lstat(current)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("path crosses a symbolic link or non-directory component")
		}
		if !first && info.Mode().Perm()&0o077 != 0 {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
		first = false
	}
}

func outputJSON(stdout io.Writer, value any) commandError {
	encoded, err := json.Marshal(value)
	if err != nil {
		return commandError{err: fmt.Errorf("encode JSON output: %w", err)}
	}
	encoded = append(encoded, '\n')
	if err := writeBounded(stdout, encoded); err != nil {
		return commandError{err: err}
	}
	return commandError{}
}

func outputHuman(stdout io.Writer, value string) commandError {
	if err := writeBounded(stdout, []byte(value)); err != nil {
		return commandError{err: err}
	}
	return commandError{}
}

func writeBounded(destination io.Writer, value []byte) error {
	if len(value) > maxOutputBytes {
		return errors.New("command output exceeds fixed bound")
	}
	written, err := io.Copy(destination, bytes.NewReader(value))
	if err != nil {
		return err
	}
	if written != int64(len(value)) {
		return io.ErrShortWrite
	}
	return nil
}

func shortIdentity(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) > 12 {
		value = value[:12]
	}
	if value == "" {
		return "none"
	}
	return value
}

func boundedLabel(value string) string {
	if len(value) > 128 {
		return value[:128] + "..."
	}
	return value
}

func sameWord(value bool) string {
	if value {
		return "same"
	}
	return "different"
}

func presentWord(value bool) string {
	if value {
		return "present"
	}
	return "none"
}

func helpText() string {
	return `Pysolate local research tools

Usage:
  pysolate-research inspect -bundle PATH [-max-calls N] [-json]
  pysolate-research compare -left PATH -right PATH [-max-calls N] [-json]
  pysolate-research branch plan [flags]
  pysolate-research branch dag [flags]
  pysolate-research lab project -report PATH -row ID -kind LAB_KIND
  pysolate-research store stats -root PATH [-json]
  pysolate-research store benchmark -root NEW_PATH [fixture flags] [-json]

Human output uses semantic labels and shortened identities. JSON output is
bounded and includes full protected identities. Inspect, compare, DAG and store
stats are read-only. Branch execution is intentionally not inferred here:
research/operator.RunBranch requires a fresh Guest and an explicitly sealed
Host Plan, Grants and source handlers.
`
}
