package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRunWithEarlyReads(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	counts := map[string]int{}
	var secondStarted, waitStarted, waitStopped chan struct{}
	var secondFails bool
	read := func(ctx context.Context, args json.RawMessage) (any, error) {
		var a struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		mu.Lock()
		counts[a.Key]++
		mu.Unlock()
		switch a.Key {
		case "first":
			select {
			case <-secondStarted:
				return 21, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case "second":
			close(secondStarted)
			if secondFails {
				return nil, errors.New("early failure")
			}
			return 5, nil
		case "fail":
			select {
			case <-waitStarted:
				return nil, errors.New("first failed")
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case "wait":
			close(waitStarted)
			<-ctx.Done()
			close(waitStopped)
			return nil, ctx.Err()
		case "book":
			return 21, nil
		case "shipping":
			return 5, nil
		case "choose":
			return "book", nil
		default:
			return nil, errors.New("missing key: " + a.Key)
		}
	}
	// This is the whole-suite budget, including wazero compilation under -race.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := New(ctx, wasm, Manifest{"lookup": {Call: read, AllowEarlyRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	run := func(t *testing.T, source, want string, transformed bool) {
		t.Helper()
		out, err := r.RunWithEarlyReads(ctx, source, map[string]any{"item": "book", "yes": false})
		if err != nil || string(out.Value) != want {
			t.Fatalf("got %+v, %v; want %s", out, err, want)
		}
		if (out.Transformed != "") != transformed {
			t.Fatalf("unexpected transformed source: %q", out.Transformed)
		}
	}
	t.Run("actual transformed Guest", func(t *testing.T) {
		run(t, `a=lookup(key="book")
b=lookup(key="shipping")
result=a+b`, "26", true)
	})
	t.Run("both started before first resolves", func(t *testing.T) {
		secondStarted = make(chan struct{})
		secondFails = false
		run(t, `a=lookup(key="first")
b=lookup(key="second")
result=a+b`, "26", true)
	})
	t.Run("error delivered after first assignment", func(t *testing.T) {
		secondStarted = make(chan struct{})
		secondFails = true
		run(t, `try:
 a=lookup(key="first")
 b=lookup(key="second")
except RuntimeError:
 result=a`, "21", true)
	})
	t.Run("dependency and branch", func(t *testing.T) {
		run(t, `key=lookup(key="choose")
a=lookup(key=key)
if inputs["yes"]:
 b=lookup(key="unselected")
else:
 b=lookup(key="shipping")
result=a+b`, "26", true)
		mu.Lock()
		defer mu.Unlock()
		if counts["unselected"] != 0 {
			t.Fatal("unselected branch executed")
		}
	})
	t.Run("argument error stays at original call", func(t *testing.T) {
		run(t, `try:
 a=lookup(key="book")
 b=lookup(key=inputs["absent"])
except KeyError:
 result=a`, "21", true)
	})
	t.Run("unused future cancelled and joined", func(t *testing.T) {
		waitStarted, waitStopped = make(chan struct{}), make(chan struct{})
		run(t, `try:
 a=lookup(key="fail")
 b=lookup(key="wait")
except RuntimeError:
 result=7`, "7", true)
		select {
		case <-waitStopped:
		default:
			t.Fatal("Run returned with live worker")
		}
	})
	t.Run("unsupported loop executes normally", func(t *testing.T) {
		run(t, `result=0
for i in range(2):
 result += lookup(key="book")`, "42", false)
	})
	t.Run("mismatch never reuses old value", func(t *testing.T) {
		run(t, `import _pysolate, json
h=_pysolate.prepare(json.dumps({"tool":"lookup","args":{"key":"book"}}))
response=json.loads(_pysolate.resolve(h,json.dumps({"tool":"lookup","args":{"key":"shipping"}})))
result=response["value"]`, "5", false)
	})
	t.Run("future does not cross run", func(t *testing.T) {
		run(t, `import _pysolate, json
response=json.loads(_pysolate.resolve(1,json.dumps({"tool":"lookup","args":{"key":"book"}})))
result="error" in response`, "true", false)
	})
	t.Run("handle is consumed only once", func(t *testing.T) {
		run(t, `import _pysolate,json
request=json.dumps({"tool":"lookup","args":{"key":"book"}})
h=_pysolate.prepare(request)
a=json.loads(_pysolate.resolve(h,request))
b=json.loads(_pysolate.resolve(h,request))
result=a["value"] == 21 and "error" in b`, "true", false)
	})
	t.Run("ordinary run does not prepare", func(t *testing.T) {
		out, err := r.Run(ctx, `import _pysolate
result=_pysolate.prepare('{"tool":"lookup","args":{"key":"book"}}')`, nil)
		if err != nil || string(out.Value) != "0" {
			t.Fatalf("%+v %v", out, err)
		}
	})
	t.Run("pending limit falls back explicitly", func(t *testing.T) {
		run(t, `import _pysolate
handles=[_pysolate.prepare('{"tool":"lookup","args":{"key":"book"}}') for _ in range(65)]
result=handles[-1] == 0 and len(set(handles[:-1])) == 64`, "true", false)
	})
	t.Run("not opted in executes normally", func(t *testing.T) {
		s, err := New(ctx, wasm, Manifest{"lookup": {Call: read}})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close(context.Background())
		out, err := s.RunWithEarlyReads(ctx, `result=lookup(key="book")`, nil)
		if err != nil || string(out.Value) != "21" || out.Transformed != "" {
			t.Fatalf("%+v %v", out, err)
		}
	})
}
