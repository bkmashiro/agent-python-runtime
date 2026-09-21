package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type replayState struct {
	tool      string
	args      string
	value     json.RawMessage
	errorText string
	initial   int
	remaining int
}

// ReplayProvider exposes one Case's tool catalog while serving only frozen,
// exact request fixtures. It is safe for concurrent early reads.
type ReplayProvider struct {
	mu          sync.Mutex
	definitions []pysolate.ToolDefinition
	entries     map[string]*replayState
}

func NewReplayProvider(item Case) (*ReplayProvider, error) {
	if err := item.Validate(); err != nil {
		return nil, err
	}
	provider := &ReplayProvider{entries: make(map[string]*replayState, len(item.Replay))}
	for _, replay := range item.Replay {
		args, err := canonicalJSON(replay.Args)
		if err != nil {
			return nil, err
		}
		provider.entries[replayKey(replay.Tool, args)] = &replayState{
			tool: replay.Tool, args: args, value: append(json.RawMessage(nil), replay.Value...), errorText: replay.Error, initial: replay.Count, remaining: replay.Count,
		}
	}
	provider.definitions = make([]pysolate.ToolDefinition, 0, len(item.Tools))
	for _, source := range item.Tools {
		tool := source
		provider.definitions = append(provider.definitions, pysolate.ToolDefinition{
			Name: tool.Name,
			Spec: pysolate.ToolSpec{
				PythonPath:     tool.PythonPath,
				Description:    tool.Description,
				InputSchema:    append(json.RawMessage(nil), tool.InputSchema...),
				AllowEarlyRead: tool.AllowEarlyRead,
				Annotations: pysolate.ToolAnnotations{
					ReadOnlyHint:   tool.AllowEarlyRead,
					IdempotentHint: tool.AllowEarlyRead,
				},
				Call: func(ctx context.Context, args json.RawMessage) (any, error) {
					return provider.call(ctx, tool.Name, args)
				},
			},
		})
	}
	if _, err := pysolate.ManifestFromProviders(context.Background(), provider); err != nil {
		return nil, err
	}
	return provider, nil
}

func (p *ReplayProvider) Tools(context.Context) ([]pysolate.ToolDefinition, error) {
	definitions := make([]pysolate.ToolDefinition, len(p.definitions))
	copy(definitions, p.definitions)
	return definitions, nil
}

func (p *ReplayProvider) call(ctx context.Context, tool string, raw json.RawMessage) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	args, err := canonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("canonicalize replay args: %w", err)
	}
	key := replayKey(tool, args)
	p.mu.Lock()
	entry := p.entries[key]
	if entry == nil || entry.remaining == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("unexpected or exhausted replay call: %s %s", tool, args)
	}
	entry.remaining--
	value := append(json.RawMessage(nil), entry.value...)
	errorText := entry.errorText
	p.mu.Unlock()
	if errorText != "" {
		return nil, errors.New(errorText)
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode frozen replay value: %w", err)
	}
	return decoded, nil
}

func (p *ReplayProvider) Verify() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var missing []string
	for _, entry := range p.entries {
		if entry.remaining > 0 {
			missing = append(missing, fmt.Sprintf("%s %s x%d", entry.tool, entry.args, entry.remaining))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("unconsumed replay calls: %v", missing)
}

// Reset starts another identical run after the previous replay was fully consumed.
func (p *ReplayProvider) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, entry := range p.entries {
		if entry.remaining != 0 {
			return errors.New("cannot reset replay before all calls are consumed")
		}
	}
	for _, entry := range p.entries {
		entry.remaining = entry.initial
	}
	return nil
}
