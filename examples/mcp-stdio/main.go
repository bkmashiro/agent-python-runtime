package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/mcpadapter"
	"github.com/bkmashiro/agent-python-runtime/mcpadapter/gosdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupInput struct {
	SKU string `json:"sku" jsonschema:"catalog SKU to look up"`
}

type lookupOutput struct {
	SKU      string `json:"sku"`
	Price    int    `json:"price"`
	Currency string `json:"currency"`
}

type demoResult struct {
	PythonPath    string          `json:"python_path"`
	CanonicalName string          `json:"canonical_name"`
	Value         json.RawMessage `json:"value"`
}

func main() {
	guest := flag.String("guest", "dist/pysolate.wasm", "Guest artifact")
	serveMCP := flag.Bool("mcp-server", false, "run the local stdio MCP fixture")
	flag.Parse()
	if *serveMCP {
		if err := serveFixture(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result, err := runDemo(ctx, *guest, exec.Command(executable, "-mcp-server"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

func runDemo(ctx context.Context, guestPath string, command *exec.Cmd) (demoResult, error) {
	wasm, err := os.ReadFile(guestPath)
	if err != nil {
		return demoResult{}, err
	}
	client, err := gosdk.ConnectCommand(ctx, command, time.Second)
	if err != nil {
		return demoResult{}, err
	}
	defer client.Close()
	provider := mcpadapter.Provider{
		Client:             client,
		CanonicalNamespace: "mcp.catalog",
		PythonNamespace:    "catalog",
		AllowEarlyRead: func(tool mcpadapter.Tool) bool {
			return tool.Annotations.ReadOnlyHint && !tool.Annotations.OpenWorldHint
		},
	}
	manifest, err := pysolate.ManifestFromProviders(ctx, provider)
	if err != nil {
		return demoResult{}, err
	}
	spec, ok := manifest["mcp.catalog.lookup"]
	if !ok || spec.PythonPath != "catalog.lookup" || !spec.AllowEarlyRead {
		return demoResult{}, errors.New("MCP fixture tool was not normalized as expected")
	}
	runner, err := pysolate.New(ctx, wasm, manifest)
	if err != nil {
		return demoResult{}, err
	}
	defer runner.Close(ctx)
	output, err := runner.Run(ctx, `
reply = catalog.lookup(sku=inputs["sku"])
structured = reply["structuredContent"]
result = {
    "sku": structured["sku"],
    "price": structured["price"],
    "currency": structured["currency"],
    "content_type": reply["content"][0]["type"],
}
`, map[string]any{"sku": "A-1"})
	if err != nil {
		return demoResult{}, err
	}
	return demoResult{
		PythonPath:    spec.PythonPath,
		CanonicalName: "mcp.catalog.lookup",
		Value:         append(json.RawMessage(nil), output.Value...),
	}, nil
}

func serveFixture(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "pysolate-catalog-fixture", Version: "v1"}, nil)
	openWorld := false
	destructive := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lookup",
		Description: "Look up one item in a fixed local catalog",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input lookupInput) (*mcp.CallToolResult, lookupOutput, error) {
		if input.SKU != "A-1" {
			return nil, lookupOutput{}, fmt.Errorf("unknown SKU %q", input.SKU)
		}
		return nil, lookupOutput{SKU: input.SKU, Price: 123, Currency: "GBP"}, nil
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}
