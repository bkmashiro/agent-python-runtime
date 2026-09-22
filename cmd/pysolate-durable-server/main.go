package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
	durableservice "github.com/bkmashiro/agent-python-runtime/service/durable"
)

const durableServicePreparationSeed = "pysolate-durable-service-v1"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "agent-core Guest artifact")
	database := flag.String("db", "pysolate-runs.db", "absolute or working-directory-relative durable SQLite path")
	address := flag.String("listen", "127.0.0.1:8081", "trusted local control-plane address")
	environment := flag.String("environment", "local-v1", "execution environment version pinned into new Runs")
	maxRunning := flag.Int("max-running", 2, "maximum Guests executing Python")
	maxResident := flag.Int("max-resident", 4, "maximum live Guests including external I/O waits")
	maxInflightTools := flag.Int("max-inflight-tools", 4, "maximum opted-in external Tool calls")
	maxQueued := flag.Int("max-queued", 16, "maximum queued attempts")
	maxRunDuration := flag.Duration("max-run-duration", 0, "maximum duration of one attempt, including queue time; zero disables the service cap")
	cowDataImage := flag.Bool("cow-data-image", false, "opt in to the Linux COW data-image preparation path")
	preparationSeed := flag.String("cow-data-image-seed", durableServicePreparationSeed, "seed required by runs when COW data-image is enabled")
	flag.Parse()
	if *cowDataImage && runtime.GOOS != "linux" {
		return errors.New("COW data-image requires Linux")
	}
	if *cowDataImage && *preparationSeed == "" {
		return errors.New("COW data-image seed must not be empty")
	}

	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	dbPath, err := filepath.Abs(*database)
	if err != nil {
		return err
	}
	store, err := durable.Open(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStartup()
	var preparation []durable.Preparation
	var serviceOptions []durableservice.Options
	if *cowDataImage {
		preparation = []durable.Preparation{{Seed: *preparationSeed, COW: true, COWDataImage: true}}
		serviceOptions = []durableservice.Options{{PreparationSeed: *preparationSeed, MaxRunDuration: *maxRunDuration}}
	} else if *maxRunDuration != 0 {
		serviceOptions = []durableservice.Options{{MaxRunDuration: *maxRunDuration}}
	}
	runner, err := durable.NewRunner(startupCtx, store, wasm, *environment, nil, preparation...)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	handler, err := durableservice.New(runner, *environment, durable.Limits{
		MaxRunning: *maxRunning, MaxResident: *maxResident,
		MaxInflightTools: *maxInflightTools, MaxQueued: *maxQueued,
	}, serviceOptions...)
	if err != nil {
		return err
	}

	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	fmt.Printf("pysolate durable service ready at http://%s db=%s\n", *address, dbPath)
	select {
	case signal := <-stop:
		fmt.Printf("stopping on %s\n", signal)
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			_ = handler.Close(context.Background())
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return errors.Join(server.Shutdown(shutdownCtx), handler.Close(shutdownCtx))
}
