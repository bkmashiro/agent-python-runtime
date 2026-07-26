package agentic

import (
	"encoding/json"
	"testing"
)

func fixtureFS(t *testing.T) *GorillaFileSystem {
	t.Helper()
	initial := json.RawMessage(`{
		"root":{"Home":{"type":"directory","contents":{
			".secret":{"type":"file","content":"hidden"},
			"a.txt":{"type":"file","content":"pear\napple\npear"},
			"b.txt":{"type":"file","content":"pear\nberry\npear"},
			"docs":{"type":"directory","contents":{}}
		}}}
	}`)
	fs, err := NewGorillaFileSystem(initial)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func callFS(t *testing.T, fs *GorillaFileSystem, name, arguments string) map[string]any {
	t.Helper()
	output, err := fs.Call(name, json.RawMessage(arguments))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if string(output) == "null" {
		return nil
	}
	var value map[string]any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("decode %s output %s: %v", name, output, err)
	}
	return value
}

func TestGorillaFileSystemReadToolsMatchPinnedUpstreamSemantics(t *testing.T) {
	fs := fixtureFS(t)
	if got := callFS(t, fs, "pwd", `{}`)["current_working_directory"]; got != "/Home" {
		t.Fatalf("pwd=%v", got)
	}
	listed := callFS(t, fs, "ls", `{}`)["current_directory_content"].([]any)
	if len(listed) != 3 || listed[0] != "a.txt" || listed[2] != "docs" {
		t.Fatalf("ls=%v", listed)
	}
	all := callFS(t, fs, "ls", `{"a":true}`)["current_directory_content"].([]any)
	if len(all) != 4 || all[0] != ".secret" {
		t.Fatalf("ls -a=%v", all)
	}
	if got := callFS(t, fs, "cat", `{"file_name":"a.txt"}`)["file_content"]; got != "pear\napple\npear" {
		t.Fatalf("cat=%v", got)
	}
	if got := callFS(t, fs, "wc", `{"file_name":"a.txt","mode":"l"}`)["count"]; got != float64(3) {
		t.Fatalf("wc=%v", got)
	}
	if got := callFS(t, fs, "sort", `{"file_name":"a.txt"}`)["sorted_content"]; got != "apple\npear\npear" {
		t.Fatalf("sort=%v", got)
	}
	matches := callFS(t, fs, "grep", `{"file_name":"a.txt","pattern":"pear"}`)["matching_lines"].([]any)
	if len(matches) != 2 {
		t.Fatalf("grep=%v", matches)
	}
	if got := callFS(t, fs, "tail", `{"file_name":"a.txt","lines":2}`)["last_lines"]; got != "apple\npear" {
		t.Fatalf("tail=%v", got)
	}
	if got := callFS(t, fs, "diff", `{"file_name1":"a.txt","file_name2":"b.txt"}`)["diff_lines"]; got != "- apple\n+ berry" {
		t.Fatalf("diff=%v", got)
	}
	if got := callFS(t, fs, "du", `{"human_readable":true}`)["disk_usage"]; got != "36.00 B" {
		t.Fatalf("du=%v", got)
	}
	matches = callFS(t, fs, "find", `{"path":".","name":".txt"}`)["matches"].([]any)
	if len(matches) != 2 || matches[0] != "./a.txt" || matches[1] != "./b.txt" {
		t.Fatalf("find=%v", matches)
	}
}

func TestGorillaFileSystemMutationSnapshotAndReset(t *testing.T) {
	fs := fixtureFS(t)
	before := fs.Snapshot()
	beforeDigest := fs.Digest()
	if out := callFS(t, fs, "touch", `{"file_name":"new.txt"}`); out != nil {
		t.Fatalf("touch=%v", out)
	}
	if out := callFS(t, fs, "echo", `{"content":"hello","file_name":"new.txt"}`); out != nil {
		t.Fatalf("echo=%v", out)
	}
	if out := callFS(t, fs, "mkdir", `{"dir_name":"archive"}`); out != nil {
		t.Fatalf("mkdir=%v", out)
	}
	if got := callFS(t, fs, "cp", `{"source":"new.txt","destination":"archive"}`)["result"]; got != "'new.txt' copied to 'archive/new.txt'" {
		t.Fatalf("cp=%v", got)
	}
	if got := callFS(t, fs, "mv", `{"source":"new.txt","destination":"renamed.txt"}`)["result"]; got != "'new.txt' moved to 'renamed.txt'" {
		t.Fatalf("mv=%v", got)
	}
	if got := callFS(t, fs, "cd", `{"folder":"archive"}`)["current_working_directory"]; got != "archive" {
		t.Fatalf("cd=%v", got)
	}
	if fs.Digest() == beforeDigest {
		t.Fatal("mutations did not change digest")
	}
	postVersion := fs.Version()
	if err := fs.RestoreAtVersion(before, postVersion+1); err == nil {
		t.Fatal("version-drift restore succeeded")
	}
	if err := fs.RestoreAtVersion(before, postVersion); err != nil {
		t.Fatal(err)
	}
	if fs.Digest() != beforeDigest {
		t.Fatalf("restore digest=%s want=%s", fs.Digest(), beforeDigest)
	}
	if got := callFS(t, fs, "cat", `{"file_name":"renamed.txt"}`)["error"]; got == nil {
		t.Fatal("restored state retained renamed file")
	}
}

func TestGorillaFileSystemRejectsUnboundedOrHostPaths(t *testing.T) {
	fs := fixtureFS(t)
	for _, test := range []struct{ name, arguments string }{
		{"touch", `{"file_name":"../escape"}`},
		{"mkdir", `{"dir_name":"/tmp"}`},
		{"echo", `{"content":"x","file_name":"a/b"}`},
	} {
		output := callFS(t, fs, test.name, test.arguments)
		if output["error"] == nil {
			t.Fatalf("%s accepted unsafe name: %v", test.name, output)
		}
	}
	if _, err := fs.Call("cat", json.RawMessage(`{"file_name":"a.txt","extra":true}`)); err == nil {
		t.Fatal("accepted unknown argument")
	}
	if _, err := fs.Call("rm", json.RawMessage(`{"file_name":"a.txt"}`)); err == nil {
		t.Fatal("accepted denied tool")
	}
}

func TestStatefulScoreSeparatesTraceFromFinalState(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "bfcl-v4-stateful-local-tools-multi_turn_base_12" {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("stateful fixture not found")
	}
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil {
		t.Fatal("decode oracle")
	}
	completed, err := NewGorillaFileSystem(task.Environment.InitialState)
	if err != nil {
		t.Fatal(err)
	}
	for _, turn := range oracle.Turns {
		for _, call := range turn {
			if _, err := completed.Call(call.Name, call.Arguments); err != nil {
				t.Fatal(err)
			}
		}
	}
	wrongTrace := make([][]StatefulCall, len(oracle.Turns))
	for index := range oracle.Turns {
		wrongTrace[index] = append([]StatefulCall(nil), oracle.Turns[index]...)
	}
	wrongTrace[0][0].Name = "pwd"
	score, err := ScoreStateful(task, wrongTrace, completed)
	if err != nil || score.Passed || score.TracePassed || !score.FinalStatePassed {
		t.Fatalf("wrong trace score=%+v err=%v", score, err)
	}
	fresh, err := NewGorillaFileSystem(task.Environment.InitialState)
	if err != nil {
		t.Fatal(err)
	}
	score, err = ScoreStateful(task, oracle.Turns, fresh)
	if err != nil || score.Passed || !score.TracePassed || score.FinalStatePassed {
		t.Fatalf("wrong state score=%+v err=%v", score, err)
	}
}

func TestStatefulDevelopmentOraclesReplayAndScore(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, task := range dataset.Tasks {
		if task.Split != "dev" || task.Track != "stateful_local_tools" {
			continue
		}
		count++
		fs, err := NewGorillaFileSystem(task.Environment.InitialState)
		if err != nil {
			t.Fatalf("%s: %v", task.ID, err)
		}
		var oracle StatefulOracle
		if err := decodeStrict(task.Oracle, &oracle); err != nil {
			t.Fatal(err)
		}
		for _, turn := range oracle.Turns {
			for _, call := range turn {
				if _, err := fs.Call(call.Name, call.Arguments); err != nil {
					t.Fatalf("%s %s: %v", task.ID, call.Name, err)
				}
			}
		}
		score, err := ScoreStateful(task, oracle.Turns, fs)
		if err != nil || !score.Passed || !score.TracePassed || !score.FinalStatePassed {
			t.Fatalf("%s score=%+v err=%v", task.ID, score, err)
		}
	}
	if count != 5 {
		t.Fatalf("dev stateful tasks=%d want=5", count)
	}
}
