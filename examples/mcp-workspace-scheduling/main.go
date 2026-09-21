// Command mcp-workspace-scheduling measures a production-shaped two-stage workflow.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	tasks := flag.Int("tasks", 2, "logical workflows per sample")
	iterations := flag.Int("iterations", 3, "samples per scheduling mode")
	toolDelay := flag.Duration("tool-delay", 50*time.Millisecond, "delay inside each MCP tool")
	serveMCP := flag.Bool("mcp-server", false, "run the local stdio MCP fixture")
	flag.Parse()
	if *serveMCP {
		return serveFixture(context.Background(), *toolDelay)
	}
	if *tasks < 1 || *tasks > 16 || *iterations < 1 || *iterations > 20 || *toolDelay < 0 || *toolDelay > 5*time.Second {
		return errors.New("invalid bounded fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return runParent(ctx, *guest, *tasks, *iterations, *toolDelay)
}
