// Package mcpadapter converts an already connected MCP client into Pysolate
// ToolProvider definitions. Transport, authentication, session lifecycle and
// MCP protocol negotiation remain owned by the embedding Host.
package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

const maxDiscoveredTools = 1024

// Annotations mirrors MCP tool hints. Hints are descriptive and never grant
// Pysolate early-execution authority by themselves.
type Annotations struct {
	ReadOnlyHint    bool
	DestructiveHint bool
	IdempotentHint  bool
	OpenWorldHint   bool
}

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Annotations Annotations
}

type ToolPage struct {
	Tools      []Tool
	NextCursor string
}

// Client is the narrow MCP client surface needed by the adapter. Official or
// application-specific MCP SDK clients can implement this without exposing
// their transport to Guest Python.
type Client interface {
	ListTools(ctx context.Context, cursor string) (ToolPage, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (any, error)
}

type Provider struct {
	Client         Client
	Namespace      string
	AllowEarlyRead func(Tool) bool
}

func (p Provider) Tools(ctx context.Context) ([]pysolate.ToolDefinition, error) {
	if p.Client == nil {
		return nil, errors.New("MCP adapter requires a client")
	}
	namespace := strings.TrimSuffix(p.Namespace, ".")
	if namespace == "" {
		return nil, errors.New("MCP adapter requires a namespace")
	}
	var definitions []pysolate.ToolDefinition
	cursor := ""
	seenCursors := map[string]bool{}
	seenNames := map[string]bool{}
	for {
		if seenCursors[cursor] {
			return nil, fmt.Errorf("MCP tool pagination repeated cursor %q", cursor)
		}
		seenCursors[cursor] = true
		page, err := p.Client.ListTools(ctx, cursor)
		if err != nil {
			return nil, fmt.Errorf("list MCP tools: %w", err)
		}
		for _, tool := range page.Tools {
			if tool.Name == "" || seenNames[tool.Name] {
				return nil, fmt.Errorf("invalid or duplicate MCP tool name %q", tool.Name)
			}
			seenNames[tool.Name] = true
			if len(definitions) >= maxDiscoveredTools {
				return nil, fmt.Errorf("MCP tool discovery exceeds %d tools", maxDiscoveredTools)
			}
			remoteName := tool.Name
			allowEarly := p.AllowEarlyRead != nil && p.AllowEarlyRead(tool)
			definitions = append(definitions, pysolate.ToolDefinition{
				Name: namespace + "." + remoteName,
				Spec: pysolate.ToolSpec{
					Description:    tool.Description,
					InputSchema:    append(json.RawMessage(nil), tool.InputSchema...),
					AllowEarlyRead: allowEarly,
					Annotations: pysolate.ToolAnnotations{
						ReadOnlyHint:    tool.Annotations.ReadOnlyHint,
						DestructiveHint: tool.Annotations.DestructiveHint,
						IdempotentHint:  tool.Annotations.IdempotentHint,
						OpenWorldHint:   tool.Annotations.OpenWorldHint,
					},
					Call: func(callCtx context.Context, args json.RawMessage) (any, error) {
						return p.Client.CallTool(callCtx, remoteName, args)
					},
				},
			})
		}
		if page.NextCursor == "" {
			return definitions, nil
		}
		cursor = page.NextCursor
	}
}
