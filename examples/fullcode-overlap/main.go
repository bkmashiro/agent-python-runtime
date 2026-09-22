package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync/atomic"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

const source = `customer = crm.get_profile(customer_id=inputs["customer_id"])
quote = shipping.get_quote(postcode=inputs["postcode"])
result = {"customer_tier": customer["tier"], "shipping_gbp": quote["amount_gbp"]}`

type demoResult struct {
	Result           json.RawMessage `json:"result"`
	ControlledWaitMS int64           `json:"controlled_wait_ms_per_tool"`
	Samples          int             `json:"samples"`
	SequentialMS     float64         `json:"sequential_median_ms"`
	FullCodeMS       float64         `json:"full_code_median_ms"`
	Speedup          float64         `json:"speedup"`
	HostCalls        int64           `json:"host_calls"`
	Transformed      bool            `json:"full_code_prepare_used"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "path to the Guest artifact")
	delay := flag.Duration("tool-delay", 150*time.Millisecond, "controlled delay for each independent Host read")
	samples := flag.Int("samples", 5, "samples per execution mode")
	flag.Parse()
	if err := run(*guest, *delay, *samples); err != nil {
		fmt.Fprintln(os.Stderr, "full-code overlap demo:", err)
		os.Exit(1)
	}
}

func run(guestPath string, delay time.Duration, samples int) error {
	if delay < 0 || samples < 1 {
		return fmt.Errorf("tool delay must be non-negative and samples positive")
	}
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return err
	}
	var calls atomic.Int64
	read := func(value any) pysolate.Tool {
		return func(ctx context.Context, _ json.RawMessage) (any, error) {
			calls.Add(1)
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
				return value, nil
			}
		}
	}
	manifest := pysolate.Manifest{
		"crm/get-profile": {
			PythonPath: "crm.get_profile", AllowEarlyRead: true,
			Annotations: pysolate.ToolAnnotations{ReadOnlyHint: true},
			Call:        read(map[string]any{"tier": "gold"}),
		},
		"shipping/get-quote": {
			PythonPath: "shipping.get_quote", AllowEarlyRead: true,
			Annotations: pysolate.ToolAnnotations{ReadOnlyHint: true},
			Call:        read(map[string]any{"amount_gbp": 8.25}),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := pysolate.NewPrepared(ctx, wasm, manifest)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	inputs := map[string]any{"customer_id": "customer-42", "postcode": "SW7"}

	sequential := make([]time.Duration, 0, samples)
	fullCode := make([]time.Duration, 0, samples)
	var result json.RawMessage
	transformed := false
	for index := 0; index < samples; index++ {
		started := time.Now()
		plain, err := runner.Run(ctx, source, inputs)
		sequential = append(sequential, time.Since(started))
		if err != nil {
			return fmt.Errorf("sequential run: %w", err)
		}
		started = time.Now()
		prepared, err := runner.RunWithEarlyReads(ctx, source, inputs)
		fullCode = append(fullCode, time.Since(started))
		if err != nil {
			return fmt.Errorf("full-code run: %w", err)
		}
		if !corpusEqual(plain.Value, prepared.Value) {
			return fmt.Errorf("execution modes returned different values: %s != %s", plain.Value, prepared.Value)
		}
		result = append(result[:0], prepared.Value...)
		transformed = transformed || prepared.Transformed != ""
	}
	if !transformed {
		return fmt.Errorf("full-code preparation was not admitted")
	}
	sequentialMedian := medianMilliseconds(sequential)
	fullCodeMedian := medianMilliseconds(fullCode)
	report := demoResult{
		Result: result, ControlledWaitMS: delay.Milliseconds(), Samples: samples,
		SequentialMS: sequentialMedian, FullCodeMS: fullCodeMedian,
		Speedup: sequentialMedian / fullCodeMedian, HostCalls: calls.Load(), Transformed: transformed,
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func medianMilliseconds(values []time.Duration) float64 {
	copyOf := append([]time.Duration(nil), values...)
	sort.Slice(copyOf, func(i, j int) bool { return copyOf[i] < copyOf[j] })
	middle := len(copyOf) / 2
	if len(copyOf)%2 == 1 {
		return float64(copyOf[middle]) / float64(time.Millisecond)
	}
	return float64(copyOf[middle-1]+copyOf[middle]) / (2 * float64(time.Millisecond))
}

func corpusEqual(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}
