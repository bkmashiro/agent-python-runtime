package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

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
	if err := flags.Parse(arguments); err != nil {
		return 2
	}
	if *artifactPath == "" || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "-artifact is required and positional arguments are not accepted")
		return 2
	}
	strategy, err := parseStrategy(*strategyText)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 2
	}
	info, err := os.Lstat(*artifactPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		_, _ = fmt.Fprintln(os.Stderr, "artifact must be a regular file")
		return 2
	}
	wasm, err := os.ReadFile(*artifactPath)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "read artifact failed")
		return 2
	}
	factory := wazeroengine.Factory{Strategy: strategy}
	if strategy != enginecontract.StrategyFreshInstance {
		factory.PreparedCapacity = 1
		factory.PreparedMaxCapacity = 1
	}
	report, verifyErr := workspacecapsule.Verify(
		context.Background(),
		wasm,
		runtimeconfig.DefaultRunConfig(),
		factory,
	)
	encoded, encodeErr := json.MarshalIndent(report, "", "  ")
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
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("output must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
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
