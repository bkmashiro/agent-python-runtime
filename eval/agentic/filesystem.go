package agentic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxFSNodes        = 4096
	maxFSDepth        = 32
	maxFSNameBytes    = 255
	maxFSContentBytes = 1 << 20
)

var ErrFileSystem = errors.New("agentic filesystem rejected input")

type fsNode struct {
	name     string
	dir      bool
	content  string
	children map[string]*fsNode
	order    []string
	parent   *fsNode
}

type GorillaFileSystem struct {
	mu      sync.Mutex
	root    *fsNode
	cwd     *fsNode
	version uint64
}

type FileSystemSnapshot struct {
	owner   *GorillaFileSystem
	root    *fsNode
	cwdPath []string
	version uint64
}

type initialNode struct {
	Type     string                     `json:"type"`
	Content  *string                    `json:"content,omitempty"`
	Contents map[string]json.RawMessage `json:"contents,omitempty"`
}

type fsLimits struct {
	nodes int
	bytes int
}

func NewGorillaFileSystem(raw json.RawMessage) (*GorillaFileSystem, error) {
	if len(raw) == 0 || len(raw) > maxTaskSize {
		return nil, ErrFileSystem
	}
	var outer map[string]json.RawMessage
	if decodeStrict(raw, &outer) != nil || len(outer) != 1 {
		return nil, ErrFileSystem
	}
	if wrapped, ok := outer["GorillaFileSystem"]; ok {
		var nested map[string]json.RawMessage
		if decodeStrict(wrapped, &nested) != nil || len(nested) != 1 {
			return nil, ErrFileSystem
		}
		outer = nested
	}
	rootRaw, ok := outer["root"]
	if !ok {
		return nil, ErrFileSystem
	}
	var roots map[string]json.RawMessage
	if decodeStrict(rootRaw, &roots) != nil || len(roots) != 1 {
		return nil, ErrFileSystem
	}
	var rootName string
	var descriptor json.RawMessage
	for rootName, descriptor = range roots {
	}
	if !safeFSName(rootName) {
		return nil, ErrFileSystem
	}
	limits := &fsLimits{}
	root, err := decodeInitialNode(rootName, descriptor, nil, 1, limits)
	if err != nil || !root.dir {
		return nil, ErrFileSystem
	}
	return &GorillaFileSystem{root: root, cwd: root}, nil
}

func decodeInitialNode(name string, raw json.RawMessage, parent *fsNode, depth int, limits *fsLimits) (*fsNode, error) {
	if depth > maxFSDepth || !safeFSName(name) {
		return nil, ErrFileSystem
	}
	var value initialNode
	if decodeStrict(raw, &value) != nil {
		return nil, ErrFileSystem
	}
	limits.nodes++
	if limits.nodes > maxFSNodes {
		return nil, ErrFileSystem
	}
	node := &fsNode{name: name, parent: parent}
	switch value.Type {
	case "file":
		if value.Content == nil || value.Contents != nil {
			return nil, ErrFileSystem
		}
		node.content = *value.Content
		limits.bytes += len([]byte(node.content))
		if limits.bytes > maxFSContentBytes {
			return nil, ErrFileSystem
		}
	case "directory":
		if value.Content != nil || value.Contents == nil {
			return nil, ErrFileSystem
		}
		node.dir = true
		node.children = make(map[string]*fsNode, len(value.Contents))
		childNames := make([]string, 0, len(value.Contents))
		for childName := range value.Contents {
			childNames = append(childNames, childName)
		}
		sort.Strings(childNames)
		for _, childName := range childNames {
			child, err := decodeInitialNode(childName, value.Contents[childName], node, depth+1, limits)
			if err != nil {
				return nil, err
			}
			addChild(node, child)
		}
	default:
		return nil, ErrFileSystem
	}
	return node, nil
}

func safeFSName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) &&
		len([]byte(name)) <= maxFSNameBytes && !strings.ContainsAny(name, `|/\?%*:><"`) && !strings.ContainsRune(name, 0)
}

func (fs *GorillaFileSystem) Snapshot() FileSystemSnapshot {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return FileSystemSnapshot{owner: fs, root: cloneNode(fs.root, nil), cwdPath: nodePath(fs.cwd), version: fs.version}
}

func (fs *GorillaFileSystem) Restore(snapshot FileSystemSnapshot) error {
	return fs.RestoreAtVersion(snapshot, 0)
}

func (fs *GorillaFileSystem) RestoreAtVersion(snapshot FileSystemSnapshot, expectedCurrent uint64) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if snapshot.owner != fs || snapshot.root == nil || (expectedCurrent != 0 && fs.version != expectedCurrent) {
		return ErrFileSystem
	}
	root := cloneNode(snapshot.root, nil)
	cwd := findNode(root, snapshot.cwdPath)
	if cwd == nil || !cwd.dir {
		return ErrFileSystem
	}
	fs.root, fs.cwd, fs.version = root, cwd, snapshot.version
	return nil
}

func (fs *GorillaFileSystem) Version() uint64 {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.version
}

func (fs *GorillaFileSystem) Digest() string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"cwd": absolutePath(fs.cwd), "root": map[string]any{fs.root.name: projectNode(fs.root)}})
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (fs *GorillaFileSystem) Call(name string, arguments json.RawMessage) (json.RawMessage, error) {
	if len(arguments) == 0 || len(arguments) > maxArgumentsBytes || !json.Valid(arguments) || bytes.TrimSpace(arguments)[0] != '{' {
		return nil, ErrFileSystem
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	beforeRoot := cloneNode(fs.root, nil)
	beforeCWD := nodePath(fs.cwd)
	output, mutated, err := fs.callLocked(name, arguments)
	if err != nil {
		return nil, err
	}
	if mutated {
		limits := &fsLimits{}
		if validateTree(fs.root, 1, limits) != nil {
			fs.root = beforeRoot
			fs.cwd = findNode(fs.root, beforeCWD)
			return marshalOutput(map[string]any{"error": "operation exceeds filesystem resource limits"})
		}
		fs.version++
	}
	return marshalOutput(output)
}

func (fs *GorillaFileSystem) CurrentWorkingDirectory() (string, error) {
	if fs == nil {
		return "", ErrFileSystem
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.cwd == nil {
		return "", ErrFileSystem
	}
	return absolutePath(fs.cwd), nil
}

func (fs *GorillaFileSystem) callLocked(name string, raw json.RawMessage) (any, bool, error) {
	switch name {
	case "pwd":
		var args struct{}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		return map[string]any{"current_working_directory": absolutePath(fs.cwd)}, false, nil
	case "ls":
		var args struct {
			All bool `json:"a"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		names := orderedNames(fs.cwd)
		if !args.All {
			visible := names[:0]
			for _, item := range names {
				if !strings.HasPrefix(item, ".") {
					visible = append(visible, item)
				}
			}
			names = visible
		}
		return map[string]any{"current_directory_content": names}, false, nil
	case "cd":
		var args struct {
			Folder string `json:"folder"`
		}
		if decodeStrict(raw, &args) != nil || len(args.Folder) > maxFSNameBytes+1 {
			return nil, false, ErrFileSystem
		}
		folder := strings.TrimRight(args.Folder, "/")
		if folder == "" {
			folder = "/"
		}
		if folder != "." && folder != ".." && folder != "/" && strings.Contains(folder, "/") {
			return map[string]any{"error": fmt.Sprintf("cd: %s: Unsupported path. Only one folder level at a time is supported.", folder)}, false, nil
		}
		if folder == ".." {
			if fs.cwd.parent == nil {
				return map[string]any{"error": "Current directory is already the root. Cannot go back."}, false, nil
			}
			fs.cwd = fs.cwd.parent
			return map[string]any{}, true, nil
		}
		target, message := fs.navigate(folder)
		if target == nil {
			return map[string]any{"error": message}, false, nil
		}
		changed := target != fs.cwd
		fs.cwd = target
		return map[string]any{"current_working_directory": target.name}, changed, nil
	case "mkdir":
		var args struct {
			Name string `json:"dir_name"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		if !safeFSName(args.Name) {
			return map[string]any{"error": fmt.Sprintf("mkdir: cannot create directory '%s': Invalid character", args.Name)}, false, nil
		}
		if fs.cwd.children[args.Name] != nil {
			return map[string]any{"error": fmt.Sprintf("mkdir: cannot create directory '%s': File exists", args.Name)}, false, nil
		}
		addChild(fs.cwd, &fsNode{name: args.Name, dir: true, children: map[string]*fsNode{}})
		return nil, true, nil
	case "touch":
		var args struct {
			Name string `json:"file_name"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		if !safeFSName(args.Name) {
			return map[string]any{"error": fmt.Sprintf("touch: cannot touch '%s': Invalid character", args.Name)}, false, nil
		}
		if fs.cwd.children[args.Name] != nil {
			return map[string]any{"error": fmt.Sprintf("touch: cannot touch '%s': File exists", args.Name)}, false, nil
		}
		addChild(fs.cwd, &fsNode{name: args.Name})
		return nil, true, nil
	case "echo":
		var args struct {
			Content string  `json:"content"`
			Name    *string `json:"file_name"`
		}
		if decodeStrict(raw, &args) != nil || len([]byte(args.Content)) > maxFSContentBytes {
			return nil, false, ErrFileSystem
		}
		if args.Name == nil || *args.Name == "" {
			return map[string]any{"terminal_output": args.Content}, false, nil
		}
		if !safeFSName(*args.Name) {
			return map[string]any{"error": fmt.Sprintf("echo: cannot write to '%s': Invalid character", *args.Name)}, false, nil
		}
		node := fs.cwd.children[*args.Name]
		if node == nil {
			return map[string]any{"error": fmt.Sprintf("echo: cannot write to '%s': No such file", *args.Name)}, false, nil
		}
		if node.dir {
			return map[string]any{"error": fmt.Sprintf("echo: cannot write to '%s': Is a directory", *args.Name)}, false, nil
		}
		changed := node.content != args.Content
		node.content = args.Content
		return nil, changed, nil
	case "cat":
		name, err := oneFileName(raw)
		if err != nil {
			return nil, false, err
		}
		if !safeFSName(name) {
			return map[string]any{"error": fmt.Sprintf("cat: '%s': Invalid character", name)}, false, nil
		}
		node := fs.cwd.children[name]
		if node == nil {
			return map[string]any{"error": fmt.Sprintf("cat: '%s': No such file or directory", name)}, false, nil
		}
		if node.dir {
			return map[string]any{"error": fmt.Sprintf("cat: '%s': Is a directory", name)}, false, nil
		}
		return map[string]any{"file_content": node.content}, false, nil
	case "find":
		var args struct {
			Path string  `json:"path"`
			Name *string `json:"name"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		if args.Path == "" {
			args.Path = "."
		}
		target, message := fs.navigate(args.Path)
		if target == nil {
			return map[string]any{"error": strings.Replace(message, "cd:", "find:", 1)}, false, nil
		}
		matches := []string{}
		base := strings.TrimRight(args.Path, "/")
		findMatches(target, base, args.Name, &matches)
		return map[string]any{"matches": matches}, false, nil
	case "wc":
		var args struct {
			Name string `json:"file_name"`
			Mode string `json:"mode"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		if args.Mode == "" {
			args.Mode = "l"
		}
		if args.Mode != "l" && args.Mode != "w" && args.Mode != "c" {
			return map[string]any{"error": fmt.Sprintf("wc: invalid mode '%s'", args.Mode)}, false, nil
		}
		node := fs.cwd.children[args.Name]
		if node == nil || node.dir {
			return map[string]any{"error": fmt.Sprintf("wc: %s: No such file or directory", args.Name)}, false, nil
		}
		if args.Mode == "l" {
			return map[string]any{"count": len(splitLines(node.content)), "type": "lines"}, false, nil
		}
		if args.Mode == "w" {
			return map[string]any{"count": len(strings.Fields(node.content)), "type": "words"}, false, nil
		}
		return map[string]any{"count": utf8.RuneCountInString(node.content), "type": "characters"}, false, nil
	case "sort":
		name, err := oneFileName(raw)
		if err != nil {
			return nil, false, err
		}
		node := fs.cwd.children[name]
		if node == nil || node.dir {
			return map[string]any{"error": fmt.Sprintf("sort: %s: No such file or directory", name)}, false, nil
		}
		lines := splitLines(node.content)
		sort.Strings(lines)
		return map[string]any{"sorted_content": strings.Join(lines, "\n")}, false, nil
	case "grep":
		var args struct {
			Name    string `json:"file_name"`
			Pattern string `json:"pattern"`
		}
		if decodeStrict(raw, &args) != nil || len(args.Pattern) > 16_384 {
			return nil, false, ErrFileSystem
		}
		node := fs.cwd.children[args.Name]
		if node == nil || node.dir {
			return map[string]any{"error": fmt.Sprintf("grep: %s: No such file or directory", args.Name)}, false, nil
		}
		matches := []string{}
		for _, line := range splitLines(node.content) {
			if strings.Contains(line, args.Pattern) {
				matches = append(matches, line)
			}
		}
		return map[string]any{"matching_lines": matches}, false, nil
	case "du":
		var args struct {
			Human bool `json:"human_readable"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		size := contentSize(fs.cwd)
		if !args.Human {
			return map[string]any{"disk_usage": fmt.Sprintf("%d bytes", size)}, false, nil
		}
		value := float64(size)
		unit := "B"
		for _, candidate := range []string{"B", "KB", "MB", "GB", "TB"} {
			unit = candidate
			if value < 1024 {
				break
			}
			value /= 1024
			unit = "PB"
		}
		return map[string]any{"disk_usage": fmt.Sprintf("%.2f %s", value, unit)}, false, nil
	case "tail":
		var args struct {
			Name  string `json:"file_name"`
			Lines *int   `json:"lines"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		count := 10
		if args.Lines != nil {
			count = *args.Lines
		}
		if count < 0 || count > 100_000 {
			return nil, false, ErrFileSystem
		}
		node := fs.cwd.children[args.Name]
		if node == nil || node.dir {
			return map[string]any{"error": fmt.Sprintf("tail: %s: No such file or directory", args.Name)}, false, nil
		}
		lines := splitLines(node.content)
		if count > len(lines) {
			count = len(lines)
		}
		if count == 0 {
			return map[string]any{"last_lines": strings.Join(lines, "\n")}, false, nil
		}
		return map[string]any{"last_lines": strings.Join(lines[len(lines)-count:], "\n")}, false, nil
	case "diff":
		var args struct {
			One string `json:"file_name1"`
			Two string `json:"file_name2"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		one, two := fs.cwd.children[args.One], fs.cwd.children[args.Two]
		if one == nil || two == nil || one.dir || two.dir {
			return map[string]any{"error": fmt.Sprintf("diff: %s or %s: No such file or directory", args.One, args.Two)}, false, nil
		}
		left, right := splitLines(one.content), splitLines(two.content)
		limit := len(left)
		if len(right) < limit {
			limit = len(right)
		}
		differences := []string{}
		for index := 0; index < limit; index++ {
			if left[index] != right[index] {
				differences = append(differences, "- "+left[index]+"\n+ "+right[index])
			}
		}
		return map[string]any{"diff_lines": strings.Join(differences, "\n")}, false, nil
	case "mv", "cp":
		var args struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if decodeStrict(raw, &args) != nil {
			return nil, false, ErrFileSystem
		}
		return fs.copyOrMove(name, args.Source, args.Destination)
	default:
		return nil, false, ErrFileSystem
	}
}

func oneFileName(raw json.RawMessage) (string, error) {
	var args struct {
		Name string `json:"file_name"`
	}
	if decodeStrict(raw, &args) != nil {
		return "", ErrFileSystem
	}
	return args.Name, nil
}

func (fs *GorillaFileSystem) copyOrMove(operation, source, destination string) (any, bool, error) {
	item := fs.cwd.children[source]
	if item == nil {
		return map[string]any{"error": fmt.Sprintf("%s: cannot %s '%s': No such file or directory", operation, map[bool]string{true: "move", false: "copy"}[operation == "mv"], source)}, false, nil
	}
	if !safeFSName(source) || !safeFSName(destination) {
		return map[string]any{"error": fmt.Sprintf("%s: path not allowed in destination. Provide only a file or directory name.", operation)}, false, nil
	}
	destinationNode := fs.cwd.children[destination]
	if destinationNode != nil {
		if !destinationNode.dir {
			return map[string]any{"error": fmt.Sprintf("%s: cannot %s '%s' to '%s': Not a directory", operation, verb(operation), source, destination)}, false, nil
		}
		if destinationNode.children[source] != nil {
			return map[string]any{"error": fmt.Sprintf("%s: cannot %s '%s' to '%s/%s': File exists", operation, verb(operation), source, destination, source)}, false, nil
		}
		if operation == "mv" {
			removeChild(fs.cwd, source)
			addChild(destinationNode, item)
		} else {
			addChild(destinationNode, cloneNode(item, nil))
		}
		return map[string]any{"result": fmt.Sprintf("'%s' %s to '%s/%s'", source, past(operation), destination, source)}, true, nil
	}
	if operation == "mv" {
		removeChild(fs.cwd, source)
		item.name = destination
		addChild(fs.cwd, item)
	} else {
		copy := cloneNode(item, nil)
		copy.name = destination
		addChild(fs.cwd, copy)
	}
	return map[string]any{"result": fmt.Sprintf("'%s' %s to '%s'", source, past(operation), destination)}, true, nil
}

func verb(operation string) string {
	if operation == "mv" {
		return "move"
	}
	return "copy"
}

func past(operation string) string {
	if operation == "mv" {
		return "moved"
	}
	return "copied"
}

func (fs *GorillaFileSystem) navigate(path string) (*fsNode, string) {
	if path == "" || path == "." {
		return fs.cwd, ""
	}
	if path == "/" {
		return fs.root, ""
	}
	current := fs.cwd
	if strings.HasPrefix(path, "/") {
		current = fs.root
	}
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return current, ""
	}
	for _, part := range strings.Split(trimmed, "/") {
		next := current.children[part]
		if next == nil || !next.dir {
			return nil, fmt.Sprintf("cd: '%s': No such file or directory", path)
		}
		current = next
	}
	return current, ""
}

func findMatches(directory *fsNode, base string, name *string, matches *[]string) {
	for _, childName := range orderedNames(directory) {
		child := directory.children[childName]
		path := base + "/" + childName
		if name == nil || strings.Contains(childName, *name) {
			*matches = append(*matches, path)
		}
		if child.dir {
			findMatches(child, path, name, matches)
		}
	}
}

func addChild(parent, child *fsNode) {
	if parent.children == nil {
		parent.children = map[string]*fsNode{}
	}
	child.parent = parent
	parent.children[child.name] = child
	parent.order = append(parent.order, child.name)
}

func removeChild(parent *fsNode, name string) *fsNode {
	child := parent.children[name]
	if child == nil {
		return nil
	}
	delete(parent.children, name)
	for index, ordered := range parent.order {
		if ordered == name {
			parent.order = append(parent.order[:index], parent.order[index+1:]...)
			break
		}
	}
	child.parent = nil
	return child
}

func orderedNames(node *fsNode) []string {
	return append([]string(nil), node.order...)
}

func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func contentSize(node *fsNode) int {
	if !node.dir {
		return len([]byte(node.content))
	}
	total := 0
	for _, child := range node.children {
		total += contentSize(child)
	}
	return total
}

func validateTree(node *fsNode, depth int, limits *fsLimits) error {
	if node == nil || depth > maxFSDepth || !safeFSName(node.name) {
		return ErrFileSystem
	}
	limits.nodes++
	if limits.nodes > maxFSNodes {
		return ErrFileSystem
	}
	if !node.dir {
		limits.bytes += len([]byte(node.content))
		if limits.bytes > maxFSContentBytes {
			return ErrFileSystem
		}
		return nil
	}
	if len(node.order) != len(node.children) {
		return ErrFileSystem
	}
	seen := make(map[string]bool, len(node.order))
	for _, name := range node.order {
		child := node.children[name]
		if child == nil || seen[name] || child.parent != node || validateTree(child, depth+1, limits) != nil {
			return ErrFileSystem
		}
		seen[name] = true
	}
	return nil
}

func cloneNode(node, parent *fsNode) *fsNode {
	copy := &fsNode{name: node.name, dir: node.dir, content: node.content, parent: parent}
	if node.dir {
		copy.children = make(map[string]*fsNode, len(node.children))
		for _, name := range node.order {
			addChild(copy, cloneNode(node.children[name], nil))
		}
	}
	return copy
}

func nodePath(node *fsNode) []string {
	path := []string{}
	for current := node; current != nil; current = current.parent {
		path = append(path, current.name)
	}
	for left, right := 0, len(path)-1; left < right; left, right = left+1, right-1 {
		path[left], path[right] = path[right], path[left]
	}
	return path
}

func findNode(root *fsNode, path []string) *fsNode {
	if len(path) == 0 || path[0] != root.name {
		return nil
	}
	current := root
	for _, name := range path[1:] {
		current = current.children[name]
		if current == nil {
			return nil
		}
	}
	return current
}

func absolutePath(node *fsNode) string {
	return "/" + strings.Join(nodePath(node), "/")
}

func projectNode(node *fsNode) map[string]any {
	if !node.dir {
		return map[string]any{"content": node.content, "type": "file"}
	}
	children := make(map[string]any, len(node.children))
	for name, child := range node.children {
		children[name] = projectNode(child)
	}
	return map[string]any{"contents": children, "type": "directory"}
}

func marshalOutput(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, ErrFileSystem
	}
	return data, nil
}
