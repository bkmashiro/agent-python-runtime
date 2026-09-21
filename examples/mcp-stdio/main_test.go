package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("PYSOLATE_MCP_FIXTURE") == "1" {
		if err := serveFixture(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRealStdioMCPToolRunsInsideGuest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	guest := os.Getenv("PYSOLATE_GUEST")
	if guest == "" {
		guest = filepath.Join("..", "..", "dist", "pysolate.wasm")
	}
	command := exec.Command(os.Args[0])
	command.Env = append(os.Environ(), "PYSOLATE_MCP_FIXTURE=1")
	result, err := runDemo(ctx, guest, command)
	if err != nil {
		t.Fatal(err)
	}
	if result.CanonicalName != "mcp.catalog.lookup" || result.PythonPath != "catalog.lookup" {
		t.Fatalf("unexpected tool identity: %#v", result)
	}
	var value struct {
		SKU         string `json:"sku"`
		Price       int    `json:"price"`
		Currency    string `json:"currency"`
		ContentType string `json:"content_type"`
	}
	if err := json.Unmarshal(result.Value, &value); err != nil {
		t.Fatal(err)
	}
	if value.SKU != "A-1" || value.Price != 123 || value.Currency != "GBP" || value.ContentType != "text" {
		t.Fatalf("unexpected Guest value: %#v", value)
	}
}
