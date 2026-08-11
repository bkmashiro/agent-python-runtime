package capability

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"
	"sync"
)

const maxWorkspaceTextBytes = 1 << 20
const workspaceTextHandlerIdentity = "pysolate.workspace-text.v1"

// Workspace is the intentionally small PoC workspace backend. It has no Host
// filesystem path and can only be reached through registered typed tools.
type Workspace struct {
	mu    sync.RWMutex
	files map[string]string
}

func NewWorkspace(seed map[string]string) (*Workspace, error) {
	workspace := &Workspace{files: make(map[string]string, len(seed))}
	for name, content := range seed {
		cleaned, err := workspacePath(name)
		if err != nil || len(content) > maxWorkspaceTextBytes {
			return nil, ErrInvalidTool
		}
		workspace.files[cleaned] = content
	}
	return workspace, nil
}

func (workspace *Workspace) Snapshot() map[string]string {
	workspace.mu.RLock()
	defer workspace.mu.RUnlock()
	result := make(map[string]string, len(workspace.files))
	for name, content := range workspace.files {
		result[name] = content
	}
	return result
}

func RegisterWorkspaceTools(registry *Registry, workspace *Workspace) error {
	if registry == nil || workspace == nil {
		return ErrInvalidTool
	}
	for _, registered := range []struct {
		spec    Spec
		handler Handler
	}{
		{workspaceReadTextSpec(), HandlerFunc(workspace.readText)},
		{workspaceWriteTextSpec(), HandlerFunc(workspace.writeText)},
		{workspaceListFilesSpec(), HandlerFunc(workspace.listFiles)},
	} {
		if err := registry.Register(registered.spec, registered.handler); err != nil {
			return err
		}
	}
	return nil
}

func workspaceReadTextSpec() Spec {
	return Spec{
		Name: "workspace.read_text", Version: "pysolate.workspace.read-text.v1", HandlerIdentity: workspaceTextHandlerIdentity,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":256}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","maxLength":1048576}},"required":["content"],"additionalProperties":false}`),
		Python:       &PythonProjection{Name: "read_text", Arguments: []string{"path"}, ResultField: "content"},
	}
}

func workspaceWriteTextSpec() Spec {
	return Spec{
		Name: "workspace.write_text", Version: "pysolate.workspace.write-text.v1", HandlerIdentity: workspaceTextHandlerIdentity,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","minLength":1,"maxLength":256},"content":{"type":"string","maxLength":1048576}},"required":["path","content"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"written":{"type":"boolean"}},"required":["written"],"additionalProperties":false}`),
		Python:       &PythonProjection{Name: "write_text", Arguments: []string{"path", "content"}, ResultField: "written"},
	}
}

func workspaceListFilesSpec() Spec {
	return Spec{
		Name: "workspace.list_files", Version: "pysolate.workspace.list-files.v1", HandlerIdentity: workspaceTextHandlerIdentity,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"files":{"type":"array","items":{"type":"string","maxLength":256}}},"required":["files"],"additionalProperties":false}`),
		Python:       &PythonProjection{Name: "list_files", Arguments: []string{}, ResultField: "files"},
	}
}

func (workspace *Workspace) readText(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return nil, ErrInvalidTool
	}
	name, err := workspacePath(arguments.Path)
	if err != nil {
		return nil, err
	}
	workspace.mu.RLock()
	content, ok := workspace.files[name]
	workspace.mu.RUnlock()
	if !ok {
		return nil, errors.New("workspace file not found")
	}
	return json.Marshal(map[string]string{"content": content})
}

func (workspace *Workspace) writeText(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if json.Unmarshal(raw, &arguments) != nil || len(arguments.Content) > maxWorkspaceTextBytes {
		return nil, ErrInvalidTool
	}
	name, err := workspacePath(arguments.Path)
	if err != nil {
		return nil, err
	}
	workspace.mu.Lock()
	workspace.files[name] = arguments.Content
	workspace.mu.Unlock()
	return json.Marshal(map[string]bool{"written": true})
}

func (workspace *Workspace) listFiles(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments map[string]json.RawMessage
	if json.Unmarshal(raw, &arguments) != nil || len(arguments) != 0 {
		return nil, ErrInvalidTool
	}
	workspace.mu.RLock()
	names := make([]string, 0, len(workspace.files))
	for name := range workspace.files {
		names = append(names, name)
	}
	workspace.mu.RUnlock()
	sort.Strings(names)
	return json.Marshal(map[string][]string{"files": names})
}

func workspacePath(name string) (string, error) {
	if name == "" || len(name) > 256 || strings.Contains(name, "\\") || strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) {
		return "", ErrInvalidTool
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") || cleaned != name {
		return "", ErrInvalidTool
	}
	return cleaned, nil
}
