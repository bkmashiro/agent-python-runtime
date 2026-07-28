package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

const probeTimeout = 3 * time.Minute

type checkpointMessage struct {
	Phase string `json:"phase"`
}

type checkpointEvidence struct {
	Phase       string                         `json:"phase"`
	Process     runtimeevidence.ProcessMetrics `json:"process"`
	COWMappings runtimeevidence.MappingMetrics `json:"cow_mappings"`
}

type processSample struct {
	RequestedSlots uint32               `json:"requested_slots"`
	Checkpoints    []checkpointEvidence `json:"checkpoints"`
}

type probeEvidence struct {
	SchemaVersion        int             `json:"schema_version"`
	ArtifactSHA256       string          `json:"artifact_sha256"`
	CompilationCacheMode string          `json:"compilation_cache_mode"`
	Samples              []processSample `json:"samples"`
}

type checkpointWriter struct {
	mutex   sync.Mutex
	encoder *json.Encoder
	input   *bufio.Scanner
	err     error
}

func (writer *checkpointWriter) emit(phase string) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()
	if writer.err != nil {
		return
	}
	if err := writer.encoder.Encode(checkpointMessage{Phase: phase}); err != nil {
		writer.err = err
		return
	}
	if !writer.input.Scan() || writer.input.Text() != "continue" {
		writer.err = errors.New("checkpoint continuation protocol failed")
	}
}

func (writer *checkpointWriter) emitPair(phase string) {
	writer.emit(phase)
	runtime.GC()
	debug.FreeOSMemory()
	writer.emit(phase + "-settled")
}

func selectedObserverPhase(phase string) bool {
	switch phase {
	case "instantiate_host", "compile", "cow_image_instantiate_guest", "cow_image__initialize", "cow_image_runtime_init", "cow_image_seal":
		return true
	default:
		return false
	}
}

func main() {
	artifact := flag.String("artifact", "", "fixed-memory CPython-WASI artifact")
	output := flag.String("output", "", "JSON evidence path")
	slotsText := flag.String("slots", "0,1,4,64", "comma-separated controls")
	child := flag.Bool("child", false, "internal child mode")
	childSlots := flag.Uint("child-slots", 0, "internal requested slots")
	profileDir := flag.String("profile-dir", "", "optional private directory for settled child heap profiles")
	cacheDir := flag.String("cache-dir", "", "optional user-owned wazero disk compilation cache")
	childProfile := flag.String("child-profile", "", "internal settled heap profile path")
	flag.Parse()
	if runtime.GOOS != "linux" {
		fatal(errors.New("apyrun-memory-probe is Linux-only"))
	}
	if *artifact == "" {
		fatal(errors.New("-artifact is required"))
	}
	if *child {
		fatal(runChild(*artifact, uint32(*childSlots), *childProfile, *cacheDir))
		return
	}
	if *output == "" {
		fatal(errors.New("-output is required"))
	}
	slots, err := parseSlots(*slotsText)
	if err != nil {
		fatal(err)
	}
	fatal(runParent(*artifact, *output, *profileDir, *cacheDir, slots))
}

func fatal(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseSlots(value string) ([]uint32, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 16 {
		return nil, errors.New("slot controls are empty or excessive")
	}
	result := make([]uint32, 0, len(parts))
	seen := map[uint32]bool{}
	for _, part := range parts {
		parsed, err := strconv.ParseUint(part, 10, 32)
		if err != nil || parsed > 4096 {
			return nil, fmt.Errorf("invalid slot control %q", part)
		}
		slots := uint32(parsed)
		if seen[slots] {
			return nil, errors.New("slot controls must be unique")
		}
		seen[slots] = true
		result = append(result, slots)
	}
	return result, nil
}

func runChild(artifactPath string, slots uint32, profilePath, cacheDir string) error {
	protocol := &checkpointWriter{encoder: json.NewEncoder(os.Stdout), input: bufio.NewScanner(os.Stdin)}
	protocol.emitPair("process-start")
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	protocol.emitPair("artifact-loaded")
	observer := func(observation wazeroengine.Observation) {
		if observation.Success && selectedObserverPhase(observation.Phase) {
			protocol.emitPair(observation.Phase)
		}
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = probeTimeout
	factory := wazeroengine.Factory{Observer: observer}
	if cacheDir != "" {
		cache, err := wazeroengine.NewCompilationCacheWithDir(cacheDir)
		if err != nil {
			return err
		}
		defer cache.Close(context.Background())
		factory.CompilationCache = cache
	}
	if slots > 0 {
		factory.Strategy = enginecontract.StrategyCOWReadySingleUse
		factory.PreparedCapacity = slots
		factory.PreparedMaxCapacity = slots
		factory.PreparedRefillWorkers = 1
	}
	runner, err := factory.New(context.Background(), wasm, config)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	if protocol.err != nil {
		return protocol.err
	}
	if slots > 0 {
		diagnostics, ok := runner.(interface{ PreparedReady() int })
		if !ok || diagnostics.PreparedReady() != int(slots) {
			return errors.New("prepared pool did not reach requested readiness")
		}
	}
	protocol.emitPair(fmt.Sprintf("ready-%d", slots))
	if profilePath != "" {
		if err := writeHeapProfile(profilePath); err != nil {
			return err
		}
	}
	return protocol.err
}

func runParent(artifactPath, outputPath, profileDir, cacheDir string, slots []uint32) error {
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(wasm)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cacheMode := "disabled"
	if cacheDir != "" {
		cacheMode = "disk"
	}
	evidence := probeEvidence{SchemaVersion: 2, ArtifactSHA256: fmt.Sprintf("%x", digest[:]), CompilationCacheMode: cacheMode}
	if profileDir != "" {
		if !filepath.IsAbs(profileDir) {
			return errors.New("profile directory must be absolute")
		}
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return err
		}
	}
	if cacheDir != "" {
		if !filepath.IsAbs(cacheDir) {
			return errors.New("compilation cache directory must be absolute")
		}
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(cacheDir, 0o700); err != nil {
			return err
		}
	}
	for _, requested := range slots {
		profilePath := ""
		if profileDir != "" {
			profilePath = filepath.Join(profileDir, fmt.Sprintf("ready-%d.heap.pprof", requested))
		}
		sample, err := collectChild(executable, artifactPath, profilePath, cacheDir, requested)
		if err != nil {
			return fmt.Errorf("collect %d slots: %w", requested, err)
		}
		evidence.Samples = append(evidence.Samples, sample)
	}
	return writeEvidence(outputPath, evidence)
}

func collectChild(executable, artifactPath, profilePath, cacheDir string, slots uint32) (processSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	args := []string{"-child", "-artifact", artifactPath, "-child-slots", strconv.FormatUint(uint64(slots), 10)}
	if profilePath != "" {
		args = append(args, "-child-profile", profilePath)
	}
	if cacheDir != "" {
		args = append(args, "-cache-dir", cacheDir)
	}
	command := exec.CommandContext(ctx, executable, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return processSample{}, err
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		return processSample{}, err
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return processSample{}, err
	}
	result := processSample{RequestedSlots: slots}
	decoder := json.NewDecoder(stdout)
	collector := runtimeevidence.DefaultLinuxCollector()
	collector.ProcessID = command.Process.Pid
	for {
		var message checkpointMessage
		err := decoder.Decode(&message)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return result, err
		}
		snapshot, err := collector.Collect()
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return result, err
		}
		mappings, err := collector.CollectNamedMappings("memfd:apyrun-cow-image")
		if err != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			return result, err
		}
		result.Checkpoints = append(result.Checkpoints, checkpointEvidence{Phase: message.Phase, Process: snapshot.Process, COWMappings: mappings})
		if _, err := fmt.Fprintln(stdin, "continue"); err != nil {
			return result, err
		}
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		return result, fmt.Errorf("child failed: %w: %s", err, stderr.String())
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if len(result.Checkpoints) < 8 || result.Checkpoints[0].Phase != "process-start" || result.Checkpoints[len(result.Checkpoints)-1].Phase != fmt.Sprintf("ready-%d-settled", slots) {
		return result, errors.New("child checkpoint sequence is incomplete")
	}
	return result, nil
}

func writeHeapProfile(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("heap profile path must be absolute")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = pprof.WriteHeapProfile(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func writeEvidence(path string, evidence probeEvidence) error {
	if !filepath.IsAbs(path) {
		return errors.New("output path must be absolute")
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
