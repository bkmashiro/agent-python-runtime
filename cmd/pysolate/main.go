package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/tetratelabs/wazero"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cacheDir := flag.String("cache", "", "optional private wazero compilation cache directory")
	artifact := flag.String("wasm", "dist/pysolate.wasm", "CPython/WASI artifact")
	source := flag.String("source", "-", "Python file, or - for stdin")
	input := flag.String("inputs", "{}", "JSON input")
	mode := flag.String("mode", "normal", "normal or early-reads")
	prepared := flag.String("prepared", "fresh", "fresh, copy, or Linux cow")
	timeout := flag.Duration("timeout", 30*time.Second, "compile and execution deadline")
	flag.Parse()
	var inputs any
	if err := json.Unmarshal([]byte(*input), &inputs); err != nil {
		return err
	}
	var reader io.Reader = os.Stdin
	if *source != "-" {
		file, err := os.Open(*source)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}
	code, err := io.ReadAll(io.LimitReader(reader, (1<<20)+1))
	if err != nil {
		return err
	}
	if len(code) > 1<<20 {
		return fmt.Errorf("source exceeds 1 MiB")
	}
	wasm, err := os.ReadFile(*artifact)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if *cacheDir != "" {
		cache, e := wazero.NewCompilationCacheWithDir(*cacheDir)
		if e != nil {
			return e
		}
		defer cache.Close(context.Background())
		ctx = pysolate.WithCompilationCache(ctx, cache)
	}

	// The CLI grants only a pure demonstration tool. Applications supply their own manifest.
	tools := pysolate.Manifest{"echo": {AllowEarlyRead: true, Call: func(_ context.Context, args json.RawMessage) (any, error) { return args, nil }}}
	var runner *pysolate.Runner
	switch *prepared {
	case "fresh":
		runner, err = pysolate.New(ctx, wasm, tools)
	case "copy":
		runner, err = pysolate.NewPrepared(ctx, wasm, tools)
	case "cow":
		runner, err = pysolate.NewPreparedCOW(ctx, wasm, tools)
	default:
		return fmt.Errorf("unknown preparation mode %q", *prepared)
	}
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	var out pysolate.Output
	switch *mode {
	case "normal":
		out, err = runner.Run(ctx, string(code), inputs)
	case "early-reads":
		out, err = runner.RunWithEarlyReads(ctx, string(code), inputs)
	default:
		return fmt.Errorf("unknown execution mode %q", *mode)
	}
	fmt.Fprint(os.Stderr, out.Stdout)
	if err != nil {
		return err
	}
	fmt.Println(string(out.Value))
	return nil
}
