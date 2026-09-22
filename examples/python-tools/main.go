package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type priceRequest struct {
	Symbol string `json:"symbol"`
}

type display struct {
	Result        json.RawMessage `json:"result"`
	HostToolCalls int64           `json:"host_tool_calls"`
}

func main() {
	guestPath := flag.String("guest", "dist/pysolate.wasm", "path to the Pysolate Guest artifact")
	flag.Parse()

	if err := run(*guestPath); err != nil {
		fmt.Fprintln(os.Stderr, "python-tools demo:", err)
		os.Exit(1)
	}
}

func run(guestPath string) error {
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return fmt.Errorf("read Guest: %w", err)
	}
	source, err := os.ReadFile("examples/python-tools/strategy.py")
	if err != nil {
		return fmt.Errorf("read Python strategy: %w", err)
	}

	prices := map[string]float64{
		"AAPL": 225.50,
		"MSFT": 418.20,
		"NVDA": 176.40,
	}
	var calls atomic.Int64
	manifest := pysolate.Manifest{
		"market/get-price": {
			PythonPath:     "market.get_price",
			Description:    "Return the current price for an allowlisted symbol",
			InputSchema:    json.RawMessage(`{"type":"object","required":["symbol"],"properties":{"symbol":{"type":"string"}}}`),
			AllowEarlyRead: true,
			Annotations:    pysolate.ToolAnnotations{ReadOnlyHint: true},
			Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				var request priceRequest
				if err := json.Unmarshal(raw, &request); err != nil {
					return nil, fmt.Errorf("decode request: %w", err)
				}
				price, ok := prices[request.Symbol]
				if !ok {
					return nil, errors.New("symbol is not allowlisted")
				}
				calls.Add(1)
				return map[string]any{"symbol": request.Symbol, "price": price}, nil
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	runner, err := pysolate.New(ctx, wasm, manifest)
	if err != nil {
		return fmt.Errorf("create Runner: %w", err)
	}
	defer runner.Close(context.Background())

	output, err := runner.Run(ctx, string(source), map[string]any{
		"symbols": []string{"AAPL", "MSFT", "NVDA"},
		"quantities": map[string]int{
			"AAPL": 2,
			"MSFT": 1,
			"NVDA": 4,
		},
	})
	if err != nil {
		return fmt.Errorf("run Python: %w", err)
	}

	encoded, err := json.MarshalIndent(display{Result: output.Value, HostToolCalls: calls.Load()}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	fmt.Println(string(encoded))
	return nil
}
