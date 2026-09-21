// Package gosdk adapts the official MCP Go SDK to Pysolate's narrow MCP
// client interface. Protocol negotiation, transport, process lifetime and
// result envelopes remain Host-side.
package gosdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/bkmashiro/agent-python-runtime/mcpadapter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client owns one initialized official-SDK client session.
type Client struct {
	session *mcp.ClientSession
}

// New wraps an already connected official-SDK session.
func New(session *mcp.ClientSession) (*Client, error) {
	if session == nil {
		return nil, errors.New("MCP Go SDK adapter requires a client session")
	}
	return &Client{session: session}, nil
}

// ConnectCommand starts one trusted stdio MCP command and completes protocol
// initialization. Closing the returned Client closes the session and the SDK's
// command transport.
func ConnectCommand(ctx context.Context, command *exec.Cmd, terminateAfter time.Duration) (*Client, error) {
	if command == nil {
		return nil, errors.New("MCP Go SDK adapter requires a command")
	}
	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "pysolate", Version: "v1"}, nil)
	session, err := sdkClient.Connect(ctx, &mcp.CommandTransport{
		Command:           command,
		TerminateDuration: terminateAfter,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect MCP stdio command: %w", err)
	}
	return &Client{session: session}, nil
}

// Close shuts down the MCP session. The official SDK makes Close idempotent and
// concurrency-safe.
func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	return c.session.Close()
}

func (c *Client) ListTools(ctx context.Context, cursor string) (mcpadapter.ToolPage, error) {
	if c == nil || c.session == nil {
		return mcpadapter.ToolPage{}, errors.New("MCP Go SDK adapter is closed")
	}
	result, err := c.session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
	if err != nil {
		return mcpadapter.ToolPage{}, err
	}
	page := mcpadapter.ToolPage{
		Tools:      make([]mcpadapter.Tool, 0, len(result.Tools)),
		NextCursor: result.NextCursor,
	}
	for _, source := range result.Tools {
		if source == nil {
			return mcpadapter.ToolPage{}, errors.New("MCP tools/list returned a null tool")
		}
		schema, err := json.Marshal(source.InputSchema)
		if err != nil {
			return mcpadapter.ToolPage{}, fmt.Errorf("encode input schema for MCP tool %q: %w", source.Name, err)
		}
		if string(schema) == "null" {
			schema = json.RawMessage(`{}`)
		}
		annotations := mcpadapter.Annotations{DestructiveHint: true, OpenWorldHint: true}
		if source.Annotations != nil {
			annotations.ReadOnlyHint = source.Annotations.ReadOnlyHint
			annotations.IdempotentHint = source.Annotations.IdempotentHint
			if source.Annotations.DestructiveHint != nil {
				annotations.DestructiveHint = *source.Annotations.DestructiveHint
			}
			if source.Annotations.OpenWorldHint != nil {
				annotations.OpenWorldHint = *source.Annotations.OpenWorldHint
			}
		}
		page.Tools = append(page.Tools, mcpadapter.Tool{
			Name:        source.Name,
			Description: source.Description,
			InputSchema: schema,
			Annotations: annotations,
		})
	}
	return page, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if c == nil || c.session == nil {
		return nil, errors.New("MCP Go SDK adapter is closed")
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil || object == nil {
		return nil, fmt.Errorf("MCP tool arguments must be a JSON object")
	}
	result, err := c.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("encode MCP tool result: %w", err)
	}
	return json.RawMessage(encoded), nil
}

var _ mcpadapter.Client = (*Client)(nil)
