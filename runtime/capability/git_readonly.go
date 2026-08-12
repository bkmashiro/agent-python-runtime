package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/pmezard/go-difflib/difflib"
)

const gitReadHandlerIdentity = "pysolate.git-read.go-git.v1"

// GitReadPolicy binds read-only Git semantics to one Host-owned local repository.
// RepositoryPath is private handler configuration and never enters a Spec or Grant.
type GitReadPolicy struct {
	RepositoryID   string
	RepositoryPath string
	MaxEntries     uint32
	MaxPatchBytes  uint32
	MaxBlobBytes   uint32
}

type gitReadHandler struct {
	repository *git.Repository
	path       string
	policy     GitReadPolicy
}

func RegisterGitReadTools(registry *Registry, policy GitReadPolicy) error {
	if registry == nil || !validGitReadPolicy(policy) {
		return ErrInvalidTool
	}
	repository, err := git.PlainOpen(policy.RepositoryPath)
	if err != nil {
		return ErrInvalidTool
	}
	canonicalPath, err := filepath.EvalSymlinks(policy.RepositoryPath)
	if err != nil || !filepath.IsAbs(canonicalPath) {
		return ErrInvalidTool
	}
	handler := &gitReadHandler{repository: repository, path: canonicalPath, policy: policy}
	grant, err := NewGrant(json.RawMessage(fmt.Sprintf(`{"scope":"host-selected-local-git-read","repository_id":%q,"network":false,"hooks":false}`, policy.RepositoryID)))
	if err != nil {
		return err
	}
	registrations := []struct {
		spec Spec
		call Handler
	}{
		{gitSpec("status", nil, "", `{"type":"object","properties":{},"additionalProperties":false}`, gitStatusOutputSchema), HandlerFunc(handler.status)},
		{gitSpec("diff", nil, "", `{"type":"object","properties":{},"additionalProperties":false}`, gitDiffOutputSchema), HandlerFunc(handler.diff)},
		{gitSpec("log", []string{"limit"}, "", `{"type":"object","properties":{"limit":{"type":"integer","minimum":1,"maximum":100}},"required":["limit"],"additionalProperties":false}`, gitLogOutputSchema), HandlerFunc(handler.log)},
		{gitSpec("show", []string{"revision", "path"}, "content", gitRevisionPathInputSchema, gitShowOutputSchema), HandlerFunc(handler.show)},
		{gitSpec("list_refs", nil, "", `{"type":"object","properties":{},"additionalProperties":false}`, gitRefsOutputSchema), HandlerFunc(handler.listRefs)},
		{gitSpec("resolve_revision", []string{"revision"}, "commit", gitRevisionInputSchema, gitResolveOutputSchema), HandlerFunc(handler.resolveRevision)},
	}
	for _, item := range registrations {
		if err := registry.Register(item.spec, grant, item.call); err != nil {
			return err
		}
	}
	return nil
}

func validGitReadPolicy(policy GitReadPolicy) bool {
	if policy.RepositoryID == "" || len(policy.RepositoryID) > 128 || policy.RepositoryPath == "" || !filepath.IsAbs(policy.RepositoryPath) || filepath.Clean(policy.RepositoryPath) != policy.RepositoryPath {
		return false
	}
	if policy.MaxEntries == 0 || policy.MaxEntries > 10_000 || policy.MaxPatchBytes == 0 || policy.MaxPatchBytes > 8<<20 || policy.MaxBlobBytes == 0 || policy.MaxBlobBytes > 8<<20 {
		return false
	}
	for _, character := range policy.RepositoryID {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func gitSpec(method string, arguments []string, resultField, input, output string) Spec {
	return Spec{
		Name: "git." + method, Version: "pysolate.git." + strings.ReplaceAll(method, "_", "-") + ".v1",
		Description: "Read bounded local Git " + strings.ReplaceAll(method, "_", " ") + " data from the Host-selected repository.",
		EffectClass: EffectWorkspaceRead, Playback: PlaybackLiveOnly, HandlerIdentity: gitReadHandlerIdentity,
		InputSchema: json.RawMessage(input), OutputSchema: json.RawMessage(output),
		Python: &PythonProjection{Module: "git", Method: method, Arguments: arguments, ResultField: resultField},
	}
}

const (
	gitRevisionInputSchema     = `{"type":"object","properties":{"revision":{"type":"string","minLength":1,"maxLength":256}},"required":["revision"],"additionalProperties":false}`
	gitRevisionPathInputSchema = `{"type":"object","properties":{"revision":{"type":"string","minLength":1,"maxLength":256},"path":{"type":"string","minLength":1,"maxLength":4096}},"required":["revision","path"],"additionalProperties":false}`
	gitStatusOutputSchema      = `{"type":"object","properties":{"head":{"type":"string","pattern":"^[0-9a-f]{40}$"},"branch":{"type":"string","maxLength":256},"clean":{"type":"boolean"},"changes":{"type":"array","maxItems":10000,"items":{"type":"object","properties":{"path":{"type":"string","maxLength":4096},"staging":{"type":"string","maxLength":1},"worktree":{"type":"string","maxLength":1}},"required":["path","staging","worktree"],"additionalProperties":false}}},"required":["head","branch","clean","changes"],"additionalProperties":false}`
	gitDiffOutputSchema        = `{"type":"object","properties":{"patch":{"type":"string","maxLength":8388608},"files":{"type":"integer","minimum":0,"maximum":10000}},"required":["patch","files"],"additionalProperties":false}`
	gitLogOutputSchema         = `{"type":"object","properties":{"commits":{"type":"array","maxItems":100,"items":{"type":"object","properties":{"commit":{"type":"string","pattern":"^[0-9a-f]{40}$"},"parents":{"type":"array","maxItems":64,"items":{"type":"string","pattern":"^[0-9a-f]{40}$"}},"author_name":{"type":"string","maxLength":512},"author_email":{"type":"string","maxLength":512},"timestamp":{"type":"integer"},"message":{"type":"string","maxLength":4096}},"required":["commit","parents","author_name","author_email","timestamp","message"],"additionalProperties":false}}},"required":["commits"],"additionalProperties":false}`
	gitShowOutputSchema        = `{"type":"object","properties":{"revision":{"type":"string","pattern":"^[0-9a-f]{40}$"},"path":{"type":"string","maxLength":4096},"content":{"type":"string","maxLength":8388608}},"required":["revision","path","content"],"additionalProperties":false}`
	gitRefsOutputSchema        = `{"type":"object","properties":{"refs":{"type":"array","maxItems":10000,"items":{"type":"object","properties":{"name":{"type":"string","maxLength":512},"target":{"type":"string","pattern":"^[0-9a-f]{40}$"}},"required":["name","target"],"additionalProperties":false}}},"required":["refs"],"additionalProperties":false}`
	gitResolveOutputSchema     = `{"type":"object","properties":{"commit":{"type":"string","pattern":"^[0-9a-f]{40}$"}},"required":["commit"],"additionalProperties":false}`
)

func decodeEmpty(raw json.RawMessage) error {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil || len(value) != 0 {
		return ErrInvalidTool
	}
	return nil
}

func (handler *gitReadHandler) status(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeEmpty(raw); err != nil {
		return nil, err
	}
	head, err := handler.repository.Head()
	if err != nil {
		return nil, errors.New("read Git HEAD")
	}
	worktree, err := handler.repository.Worktree()
	if err != nil {
		return nil, errors.New("open Git worktree")
	}
	status, err := worktree.Status()
	if err != nil {
		return nil, errors.New("read Git status")
	}
	paths := make([]string, 0, len(status))
	for name, value := range status {
		if value.Staging != git.Unmodified || value.Worktree != git.Unmodified {
			paths = append(paths, filepath.ToSlash(name))
		}
	}
	sort.Strings(paths)
	if uint32(len(paths)) > handler.policy.MaxEntries {
		return nil, errors.New("Git status exceeds entry bound")
	}
	changes := make([]map[string]string, 0, len(paths))
	for _, name := range paths {
		value := status[name]
		changes = append(changes, map[string]string{"path": name, "staging": string([]byte{byte(value.Staging)}), "worktree": string([]byte{byte(value.Worktree)})})
	}
	branch := ""
	if head.Name().IsBranch() {
		branch = head.Name().Short()
	}
	return json.Marshal(map[string]any{"head": head.Hash().String(), "branch": branch, "clean": len(changes) == 0, "changes": changes})
}

func (handler *gitReadHandler) diff(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeEmpty(raw); err != nil {
		return nil, err
	}
	head, err := handler.repository.Head()
	if err != nil {
		return nil, errors.New("read Git HEAD")
	}
	commit, err := handler.repository.CommitObject(head.Hash())
	if err != nil {
		return nil, errors.New("read Git commit")
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, errors.New("read Git tree")
	}
	worktree, err := handler.repository.Worktree()
	if err != nil {
		return nil, errors.New("open Git worktree")
	}
	status, err := worktree.Status()
	if err != nil {
		return nil, errors.New("read Git status")
	}
	names := make([]string, 0, len(status))
	for name, value := range status {
		if value.Staging != git.Unmodified || value.Worktree != git.Unmodified {
			names = append(names, filepath.ToSlash(name))
		}
	}
	sort.Strings(names)
	if uint32(len(names)) > handler.policy.MaxEntries {
		return nil, errors.New("Git diff exceeds entry bound")
	}
	var builder strings.Builder
	for _, name := range names {
		cleaned, cleanErr := cleanGitPath(name)
		if cleanErr != nil {
			return nil, errors.New("Git status returned an invalid path")
		}
		name = cleaned
		old := ""
		if file, fileErr := tree.File(name); fileErr == nil {
			if file.Size > int64(handler.policy.MaxPatchBytes) {
				return nil, errors.New("Git diff input exceeds byte bound")
			}
			old, _ = file.Contents()
		}
		worktreePath, fileInfo, pathErr := handler.safeWorktreeFile(name)
		if pathErr != nil && !errors.Is(pathErr, os.ErrNotExist) {
			return nil, errors.New("Git worktree path is unavailable")
		}
		if pathErr == nil && fileInfo.Size() > int64(handler.policy.MaxPatchBytes) {
			return nil, errors.New("Git diff input exceeds byte bound")
		}
		newBytes, readErr := os.ReadFile(worktreePath)
		newValue := ""
		if readErr == nil {
			newValue = string(newBytes)
		} else if !os.IsNotExist(readErr) {
			return nil, errors.New("read Git worktree file")
		}
		if !utf8.ValidString(old) || !utf8.ValidString(newValue) {
			return nil, errors.New("Git diff supports bounded UTF-8 files")
		}
		patch, diffErr := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: difflib.SplitLines(old), B: difflib.SplitLines(newValue),
			FromFile: "a/" + name, ToFile: "b/" + name, Context: 3,
		})
		if diffErr != nil {
			return nil, errors.New("compute Git diff")
		}
		builder.WriteString("diff --git a/" + name + " b/" + name + "\n" + patch)
		if builder.Len() > int(handler.policy.MaxPatchBytes) {
			return nil, errors.New("Git diff exceeds byte bound")
		}
	}
	return json.Marshal(map[string]any{"patch": builder.String(), "files": len(names)})
}

func (handler *gitReadHandler) safeWorktreeFile(name string) (string, os.FileInfo, error) {
	current := handler.path
	var info os.FileInfo
	for _, component := range strings.Split(name, "/") {
		current = filepath.Join(current, component)
		var err error
		info, err = os.Lstat(current)
		if err != nil {
			return current, nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, nil, errors.New("Git worktree symlinks are unavailable")
		}
	}
	if info == nil || !info.Mode().IsRegular() {
		return current, nil, errors.New("Git worktree entry is not an ordinary file")
	}
	return current, info, nil
}

func (handler *gitReadHandler) log(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments struct {
		Limit int `json:"limit"`
	}
	if json.Unmarshal(raw, &arguments) != nil || arguments.Limit < 1 || arguments.Limit > 100 || uint32(arguments.Limit) > handler.policy.MaxEntries {
		return nil, ErrInvalidTool
	}
	iterator, err := handler.repository.Log(&git.LogOptions{})
	if err != nil {
		return nil, errors.New("read Git log")
	}
	defer iterator.Close()
	commits := make([]map[string]any, 0, arguments.Limit)
	for len(commits) < arguments.Limit {
		commit, nextErr := iterator.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, errors.New("iterate Git log")
		}
		parents := make([]string, len(commit.ParentHashes))
		for index, parent := range commit.ParentHashes {
			parents[index] = parent.String()
		}
		message := commit.Message
		if len(message) > 4096 {
			message = message[:4096]
		}
		commits = append(commits, map[string]any{"commit": commit.Hash.String(), "parents": parents, "author_name": commit.Author.Name, "author_email": commit.Author.Email, "timestamp": commit.Author.When.Unix(), "message": message})
	}
	return json.Marshal(map[string]any{"commits": commits})
}

func cleanGitPath(name string) (string, error) {
	if name == "" || len(name) > 4096 || strings.Contains(name, "\\") || strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) {
		return "", ErrInvalidTool
	}
	cleaned := path.Clean(name)
	if cleaned != name || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", ErrInvalidTool
	}
	for _, component := range strings.Split(cleaned, "/") {
		if component == ".git" || strings.HasPrefix(component, "..") {
			return "", ErrInvalidTool
		}
	}
	return cleaned, nil
}

func (handler *gitReadHandler) resolve(value string) (plumbing.Hash, error) {
	if value == "" || len(value) > 256 || strings.IndexByte(value, 0) >= 0 {
		return plumbing.ZeroHash, ErrInvalidTool
	}
	revision := plumbing.Revision(value)
	hash, err := handler.repository.ResolveRevision(revision)
	if err != nil || hash == nil {
		return plumbing.ZeroHash, errors.New("resolve Git revision")
	}
	commit, err := handler.repository.CommitObject(*hash)
	if err != nil {
		return plumbing.ZeroHash, errors.New("revision is not a Git commit")
	}
	return commit.Hash, nil
}

func (handler *gitReadHandler) show(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments struct {
		Revision string `json:"revision"`
		Path     string `json:"path"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return nil, ErrInvalidTool
	}
	name, err := cleanGitPath(arguments.Path)
	if err != nil {
		return nil, err
	}
	hash, err := handler.resolve(arguments.Revision)
	if err != nil {
		return nil, err
	}
	commit, err := handler.repository.CommitObject(hash)
	if err != nil {
		return nil, errors.New("read Git commit")
	}
	file, err := commit.File(name)
	if err != nil {
		return nil, errors.New("read Git file")
	}
	if file.Size > int64(handler.policy.MaxBlobBytes) {
		return nil, errors.New("Git blob exceeds byte bound")
	}
	content, err := file.Contents()
	if err != nil || !utf8.ValidString(content) {
		return nil, errors.New("read bounded UTF-8 Git blob")
	}
	return json.Marshal(map[string]any{"revision": hash.String(), "path": name, "content": content})
}

func (handler *gitReadHandler) listRefs(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if err := decodeEmpty(raw); err != nil {
		return nil, err
	}
	iterator, err := handler.repository.References()
	if err != nil {
		return nil, errors.New("read Git refs")
	}
	defer iterator.Close()
	rows := make([]map[string]string, 0)
	err = iterator.ForEach(func(reference *plumbing.Reference) error {
		if reference.Type() != plumbing.HashReference {
			return nil
		}
		rows = append(rows, map[string]string{"name": reference.Name().String(), "target": reference.Hash().String()})
		if uint32(len(rows)) > handler.policy.MaxEntries {
			return errors.New("Git refs exceed entry bound")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["name"] < rows[j]["name"] })
	return json.Marshal(map[string]any{"refs": rows})
}

func (handler *gitReadHandler) resolveRevision(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var arguments struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(raw, &arguments) != nil {
		return nil, ErrInvalidTool
	}
	hash, err := handler.resolve(arguments.Revision)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"commit": hash.String()})
}
