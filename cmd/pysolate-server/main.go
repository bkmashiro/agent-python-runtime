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
	"syscall"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	workspacepkg "github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	"github.com/bkmashiro/agent-python-runtime/service"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "agent-core Guest artifact")
	address := flag.String("listen", "127.0.0.1:8080", "trusted local control-plane address")
	workspaceRoot := flag.String("workspace-root", "", "private 0700 workspace root; empty uses a temporary root")
	maxActive := flag.Int("max-active", 2, "maximum active Guest executions")
	maxRunDuration := flag.Duration("max-run-duration", 0, "maximum execution duration; zero disables the service cap")
	cowDataImage := flag.Bool("cow-data-image", false, "opt in to the Linux COW data-image preparation path")
	flag.Parse()

	wasm, err := os.ReadFile(*guest)
	if err != nil {
		return err
	}
	root := *workspaceRoot
	removeRoot := false
	if root == "" {
		root, err = os.MkdirTemp("", "pysolate-service-")
		removeRoot = true
	} else {
		root, err = filepath.Abs(root)
		if err == nil {
			err = os.Mkdir(root, 0o700)
			if errors.Is(err, os.ErrExist) {
				err = nil
			}
		}
	}
	if err != nil {
		return err
	}
	if removeRoot {
		defer os.RemoveAll(root)
	}
	manager, err := workspacepkg.NewManager(root)
	if err != nil {
		return err
	}
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelStartup()
	handler, err := service.New(startupCtx, wasm, pysolate.Manifest{}, manager, *maxActive, service.Options{COWDataImage: *cowDataImage, MaxRunDuration: *maxRunDuration})
	if err != nil {
		_ = manager.Close()
		return err
	}
	server := &http.Server{Addr: *address, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	fmt.Printf("pysolate service ready at http://%s\n", *address)
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
