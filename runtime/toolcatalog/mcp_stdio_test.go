package toolcatalog

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMCPFixtureProcess(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "mcp-fixture" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"tools/list"`) {
		os.Exit(3)
	}
	switch mode {
	case "valid":
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"fixture","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}},"outputSchema":{"type":"object"}}]}}`)
	case "oversized":
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"`+strings.Repeat("x", 4096)+`","inputSchema":{"type":"object"}}]}}`)
	case "slow":
		time.Sleep(2 * time.Second)
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	case "orphan":
		child := exec.Command("/bin/sh", "-c", "sleep 5")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(5)
		}
		fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	default:
		os.Exit(4)
	}
	os.Exit(0)
}

func TestStdioDiscoveryReadsBoundedMCPToolsList(t *testing.T) {
	discovery, err := NewStdioDiscovery(StdioConfig{
		Command:  []string{os.Args[0], "-test.run=TestMCPFixtureProcess", "--", "mcp-fixture", "valid"},
		ServerID: "fixture", HandlerVersion: "v1", Timeout: 5 * time.Second, MaxResponseBytes: 16 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := discovery.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].ToolID != "fixture.echo" || tools[0].ServerID != "fixture" || tools[0].HandlerVersion != "v1" || string(tools[0].OutputSchema) != `{"type":"object"}` {
		t.Fatalf("tools=%+v", tools)
	}
}

func TestStdioDiscoveryRejectsOversizedOrTimedOutFixture(t *testing.T) {
	discovery, err := NewStdioDiscovery(StdioConfig{
		Command:  []string{os.Args[0], "-test.run=TestMCPFixtureProcess", "--", "mcp-fixture", "oversized"},
		ServerID: "fixture", HandlerVersion: "v1", Timeout: 5 * time.Second, MaxResponseBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.Discover(context.Background()); err == nil {
		t.Fatal("oversized MCP response accepted")
	}
	timed, err := NewStdioDiscovery(StdioConfig{
		Command:  []string{os.Args[0], "-test.run=TestMCPFixtureProcess", "--", "mcp-fixture", "slow"},
		ServerID: "fixture", HandlerVersion: "v1", Timeout: 50 * time.Millisecond, MaxResponseBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := timed.Discover(context.Background()); err == nil {
		t.Fatal("timed-out MCP process accepted")
	}
}

func TestStdioDiscoveryRejectsMissingResultAndBoundsOrphanedPipes(t *testing.T) {
	config := StdioConfig{ServerID: "fixture", HandlerVersion: "v1"}
	if _, err := decodeToolsList([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`), config); err == nil {
		t.Fatal("missing tools field accepted")
	}
	discovery, err := NewStdioDiscovery(StdioConfig{
		Command:  []string{os.Args[0], "-test.run=TestMCPFixtureProcess", "--", "mcp-fixture", "orphan"},
		ServerID: "fixture", HandlerVersion: "v1", Timeout: 100 * time.Millisecond, MaxResponseBytes: 512,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := discovery.Discover(context.Background()); err == nil {
		t.Fatal("orphaned output pipe accepted")
	}
	if time.Since(started) > time.Second {
		t.Fatal("orphaned output pipe exceeded bounded shutdown")
	}
}
