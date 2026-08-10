package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/verification/workspacecapsule"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(arguments []string) int {
	flags := flag.NewFlagSet("apyrun-verify-workspace", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	artifactPath := flags.String("artifact", "", "CPython-WASI reactor artifact")
	outputPath := flags.String("output", "-", "JSON report path, or - for stdout")
	strategyText := flags.String("strategy", string(enginecontract.StrategySingleUsePrepared), "fresh-instance, single-use-preinitialized, or cow-ready-single-use")
	stressIterations := flags.Int("stress-iterations", 0, "additional disposable instance Runs, 0 to 1000")
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *artifactPath == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "-artifact is required and positional arguments are not accepted")
		return 2
	}
	if *stressIterations < 0 || *stressIterations > workspacecapsule.MaxStressIterations {
		_, _ = fmt.Fprintf(os.Stderr, "-stress-iterations must be between 0 and %d\n", workspacecapsule.MaxStressIterations)
		return 2
	}
	strategy, err := parseStrategy(*strategyText)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}
	wasm, err := readArtifact(*artifactPath)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "artifact must be a bounded regular file")
		return 2
	}
	factory := wazeroengine.Factory{Strategy: strategy}
	if strategy != enginecontract.StrategyFreshInstance {
		factory.PreparedCapacity = 1
		factory.PreparedMaxCapacity = 1
	}
	options := workspacecapsule.DefaultOptions()
	options.StressIterations = *stressIterations
	report, verifyErr := workspacecapsule.VerifyWithOptions(
		context.Background(),
		wasm,
		runtimeconfig.DefaultRunConfig(),
		factory,
		options,
	)
	encoded, encodeErr := json.MarshalIndent(report, "", "  ")
	if encodeErr == nil {
		encodeErr = workspacecapsule.ValidateReportJSON(encoded)
	}
	if encodeErr == nil {
		encoded = append(encoded, '\n')
		encodeErr = writeReport(*outputPath, encoded)
	}
	if encodeErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, "write verification report failed")
		return 2
	}
	if verifyErr != nil {
		_, _ = fmt.Fprintln(os.Stderr, verifyErr)
		return 2
	}
	if report.Status != workspacecapsule.StatusVerified {
		_, _ = fmt.Fprintln(os.Stderr, "workspace capsule verification failed")
		return 1
	}
	return 0
}

const maxArtifactBytes int64 = 1 << 30

func readArtifact(path string) ([]byte, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Base(absolute))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxArtifactBytes {
		return nil, errors.New("artifact is not a bounded regular file")
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxArtifactBytes+1))
	if err != nil || int64(len(payload)) > maxArtifactBytes {
		return nil, errors.New("artifact exceeds read bound")
	}
	return payload, nil
}

func parseStrategy(value string) (enginecontract.ExecutionStrategy, error) {
	strategy := enginecontract.ExecutionStrategy(value)
	switch strategy {
	case enginecontract.StrategyFreshInstance, enginecontract.StrategySingleUsePrepared, enginecontract.StrategyCOWReadySingleUse:
		return strategy, nil
	default:
		return "", errors.New("strategy must be fresh-instance, single-use-preinitialized, or cow-ready-single-use")
	}
}

func writeReport(path string, payload []byte) error {
	if path == "-" {
		_, err := os.Stdout.Write(payload)
		return err
	}
	if path == "" {
		return errors.New("output path is empty")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(absolute)
	if info, statErr := root.Lstat(name); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("output must be a regular file")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(payload)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}
