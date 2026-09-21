package main

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func serveFixture(ctx context.Context, delay time.Duration) error {
	server := mcp.NewServer(&mcp.Implementation{Name: "pysolate-workflow-fixture", Version: "v1"}, nil)
	readOnly, openWorld, destructive := true, false, false
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: readOnly, OpenWorldHint: &openWorld, DestructiveHint: &destructive}
	mcp.AddTool(server, &mcp.Tool{Name: "lookup", Description: "Look up a fixed catalog item", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input lookupInput) (*mcp.CallToolResult, lookupOutput, error) {
		if err := wait(ctx, delay); err != nil {
			return nil, lookupOutput{}, err
		}
		if input.SKU != "A-1" {
			return nil, lookupOutput{}, fmt.Errorf("unknown SKU %q", input.SKU)
		}
		return nil, lookupOutput{SKU: input.SKU, ItemID: "item-A-1", Price: 123, Currency: "GBP"}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "detail", Description: "Read fixed catalog metadata", Annotations: annotations}, func(ctx context.Context, _ *mcp.CallToolRequest, input detailInput) (*mcp.CallToolResult, detailOutput, error) {
		if err := wait(ctx, delay); err != nil {
			return nil, detailOutput{}, err
		}
		if input.ItemID != "item-A-1" {
			return nil, detailOutput{}, fmt.Errorf("unknown item %q", input.ItemID)
		}
		return nil, detailOutput{ItemID: input.ItemID, Note: "approved local catalog record"}, nil
	})
	return server.Run(ctx, &mcp.StdioTransport{})
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
