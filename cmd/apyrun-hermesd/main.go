package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/hermesbridge"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

type options struct {
	artifactPath string
	manifestPath string
	socketPath   string
	tracePath    string
	maxMemoryMiB uint
	maxCPUMS     uint
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, boundedDiagnostic(err.Error()))
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("apyrun-hermesd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var value options
	flags.StringVar(&value.artifactPath, "artifact", "", "absolute path to verified WASI guest")
	flags.StringVar(&value.manifestPath, "manifest", "", "absolute path to distribution manifest")
	flags.StringVar(&value.socketPath, "socket", "", "absolute path to profile-private Unix socket")
	flags.StringVar(&value.tracePath, "trace-db", "", "absolute path to private trace SQLite database")
	flags.UintVar(&value.maxMemoryMiB, "max-memory-mib", 512, "maximum WebAssembly memory in MiB")
	flags.UintVar(&value.maxCPUMS, "max-cpu-ms", 20000, "per-invocation wall/CPU budget in milliseconds")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return options{}, errors.New("invalid apyrun-hermesd arguments")
	}
	for _, path := range []string{value.artifactPath, value.manifestPath, value.socketPath, value.tracePath} {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return options{}, errors.New("all apyrun-hermesd paths must be clean and absolute")
		}
	}
	if value.maxMemoryMiB < 128 || value.maxMemoryMiB > 1024 || value.maxCPUMS == 0 || value.maxCPUMS > 300000 {
		return options{}, errors.New("apyrun-hermesd resource bounds are invalid")
	}
	return value, nil
}

func (value options) runtimePolicy() (runtimeconfig.RunConfig, wazeroengine.Factory, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = time.Duration(value.maxCPUMS) * time.Millisecond
	config.MemoryLimitPages = uint32(value.maxMemoryMiB * 16)
	if err := config.Validate(); err != nil {
		return runtimeconfig.RunConfig{}, wazeroengine.Factory{}, err
	}
	factory := wazeroengine.Factory{
		Strategy: engine.StrategySingleUsePrepared, PreparedCapacity: 1, PreparedMaxCapacity: 1,
		AdaptivePreparedRefill: true,
	}
	return config, factory, nil
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	value, err := parseOptions(args)
	if err != nil {
		return err
	}
	if err := requirePrivateOwnedDir(filepath.Dir(value.socketPath)); err != nil {
		return err
	}
	if filepath.Dir(value.tracePath) != filepath.Dir(value.socketPath) {
		if err := requirePrivateOwnedDir(filepath.Dir(value.tracePath)); err != nil {
			return err
		}
	}
	wasm, provenance, err := hermesbridge.LoadPinnedArtifact(value.artifactPath, value.manifestPath)
	if err != nil {
		return err
	}
	config, factory, err := value.runtimePolicy()
	if err != nil {
		return err
	}
	store, err := agenttrace.OpenSQLiteStore(value.tracePath)
	if err != nil {
		return err
	}
	traceManager, err := hermesbridge.NewTraceManager(store)
	if err != nil {
		_ = store.Close()
		return err
	}
	runner, err := factory.New(ctx, wasm, config)
	if err != nil {
		_ = store.Close()
		return err
	}
	service, err := hermesbridge.NewService(runner, traceManager, randomExecutionID, config.Timeout)
	if err != nil {
		_ = runner.Close(context.Background())
		_ = store.Close()
		return err
	}
	server, err := hermesbridge.NewServer(service, 1, config.Timeout+5*time.Second)
	if err != nil {
		_ = runner.Close(context.Background())
		_ = store.Close()
		return err
	}
	listener, err := hermesbridge.ListenUnix(value.socketPath)
	if err != nil {
		_ = runner.Close(context.Background())
		_ = store.Close()
		return err
	}
	readiness := map[string]any{
		"status": "ready", "protocol": hermesbridge.ProtocolVersion, "socket": value.socketPath,
		"artifact_sha256": provenance.ArtifactSHA256, "manifest_sha256": provenance.ManifestSHA256,
		"guest_repository_commit": provenance.RepositoryCommit, "strategy": engine.StrategySingleUsePrepared,
		"prepared_capacity": 1, "max_concurrency": 1, "max_memory_mib": value.maxMemoryMiB,
		"max_cpu_ms": value.maxCPUMS, "network_capability": false, "provider_mode": "none",
	}
	if err := json.NewEncoder(stdout).Encode(readiness); err != nil {
		_ = listener.Close()
		_ = runner.Close(context.Background())
		_ = store.Close()
		return err
	}
	serveErr := server.Serve(ctx, listener)
	closeErr := errors.Join(runner.Close(context.Background()), store.Close())
	return errors.Join(serveErr, closeErr)
}

func randomExecutionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "execution-" + hex.EncodeToString(value[:]), nil
}

func requirePrivateOwnedDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("bridge state directory must exist with mode 0700")
	}
	return nil
}

func boundedDiagnostic(message string) string {
	if len(message) > 512 {
		return message[:512]
	}
	return message
}
