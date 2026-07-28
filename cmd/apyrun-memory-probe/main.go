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
	SchemaVersion  int             `json:"schema_version"`
	ArtifactSHA256 string          `json:"artifact_sha256"`
	Samples        []processSample `json:"samples"`
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
	flag.Parse()
	if runtime.GOOS != "linux" {
		fatal(errors.New("apyrun-memory-probe is Linux-only"))
	}
	if *artifact == "" {
		fatal(errors.New("-artifact is required"))
	}
	if *child {
		fatal(runChild(*artifact, uint32(*childSlots)))
		return
	}
	if *output == "" {
		fatal(errors.New("-output is required"))
	}
	slots, err := parseSlots(*slotsText)
	if err != nil {
		fatal(err)
	}
	fatal(runParent(*artifact, *output, slots))
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

func runChild(artifactPath string, slots uint32) error {
	protocol := &checkpointWriter{encoder: json.NewEncoder(os.Stdout), input: bufio.NewScanner(os.Stdin)}
	protocol.emit("process-start")
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	protocol.emit("artifact-loaded")
	observer := func(observation wazeroengine.Observation) {
		if observation.Success && selectedObserverPhase(observation.Phase) {
			protocol.emit(observation.Phase)
		}
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = probeTimeout
	factory := wazeroengine.Factory{Observer: observer}
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
	protocol.emit(fmt.Sprintf("ready-%d", slots))
	return protocol.err
}

func runParent(artifactPath, outputPath string, slots []uint32) error {
	wasm, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(wasm)
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	evidence := probeEvidence{SchemaVersion: 1, ArtifactSHA256: fmt.Sprintf("%x", digest[:])}
	for _, requested := range slots {
		sample, err := collectChild(executable, artifactPath, requested)
		if err != nil {
			return fmt.Errorf("collect %d slots: %w", requested, err)
		}
		evidence.Samples = append(evidence.Samples, sample)
	}
	return writeEvidence(outputPath, evidence)
}

func collectChild(executable, artifactPath string, slots uint32) (processSample, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-child", "-artifact", artifactPath, "-child-slots", strconv.FormatUint(uint64(slots), 10))
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
	if len(result.Checkpoints) < 4 || result.Checkpoints[0].Phase != "process-start" || result.Checkpoints[len(result.Checkpoints)-1].Phase != fmt.Sprintf("ready-%d", slots) {
		return result, errors.New("child checkpoint sequence is incomplete")
	}
	return result, nil
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
