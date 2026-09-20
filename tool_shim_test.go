package pysolate

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDynamicToolNamespaceInRealGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	canonical := "mcp.market/get-price"
	manifest := Manifest{canonical: {
		PythonPath:     "stock.getprice",
		Description:    "Read one approved price",
		InputSchema:    json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"]}`),
		Annotations:    ToolAnnotations{ReadOnlyHint: true},
		AllowEarlyRead: true,
		Call: func(_ context.Context, args json.RawMessage) (any, error) {
			var input map[string]any
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, err
			}
			return map[string]any{"canonical": canonical, "symbol": input["symbol"], "price": 123}, nil
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := New(ctx, wasm, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	out, err := runner.RunPLM(ctx, `quote=stock.getprice(symbol="AAPL")
result=quote`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Canonical string `json:"canonical"`
		Symbol    string `json:"symbol"`
		Price     int    `json:"price"`
	}
	if err := json.Unmarshal(out.Value, &value); err != nil {
		t.Fatal(err)
	}
	if value.Canonical != canonical || value.Symbol != "AAPL" || value.Price != 123 || out.Transformed == "" {
		t.Fatalf("unexpected value=%#v transformed=%q", value, out.Transformed)
	}
}
