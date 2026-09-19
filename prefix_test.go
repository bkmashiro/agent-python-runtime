package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPrefixGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := map[string]int{}
	var started, stopped chan struct{}
	read := func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		mu.Lock()
		calls[a.Key]++
		mu.Unlock()
		if started != nil {
			select {
			case started <- struct{}{}:
			default:
			}
		}
		switch a.Key {
		case "book":
			return 21, nil
		case "shipping":
			return 5, nil
		case "choose":
			return "book", nil
		case "wait":
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		default:
			return nil, errors.New("missing key")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := New(ctx, wasm, Manifest{"lookup": {Call: read, AllowEarlyRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	count := func(key string) int { mu.Lock(); defer mu.Unlock(); return calls[key] }
	stream := func(parts ...string) <-chan string {
		ch := make(chan string, len(parts))
		for _, p := range parts {
			ch <- p
		}
		close(ch)
		return ch
	}
	t.Run("starts before source closes and is claimed once", func(t *testing.T) {
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		started = make(chan struct{}, 2)
		chunks := make(chan string)
		// Producer refuses to send the rest until the real Host tool has started.
		go func() {
			defer close(chunks)
			select {
			case chunks <- "price=lookup(key=inputs['item'])\n":
			case <-runCtx.Done():
				return
			}
			select {
			case <-started:
			case <-runCtx.Done():
				return
			}
			select {
			case chunks <- "shipping=lookup(key='shipping')\nresult=price+shipping":
			case <-runCtx.Done():
			}
		}()
		before := count("book")
		out, err := r.RunPrefix(runCtx, chunks, map[string]any{"item": "book"})
		if err != nil || string(out.Value) != "26" || count("book") != before+1 {
			t.Fatalf("%+v %v", out, err)
		}
		started = nil
	})
	t.Run("unsupported final syntax reuses the leading read", func(t *testing.T) {
		before := count("book")
		out, err := r.RunPrefix(ctx, stream("a=lookup(key='book')\n", "import math\nresult=a"), nil)
		if err != nil || string(out.Value) != "21" || out.Transformed != "" || count("book") != before+1 {
			t.Fatalf("%+v %v", out, err)
		}
	})
	t.Run("partial statement is not executed", func(t *testing.T) {
		before := count("book")
		_, err := r.RunPrefix(ctx, stream("a=lookup(key='book'"), nil)
		if err == nil || !strings.Contains(err.Error(), "SyntaxError") || count("book") != before {
			t.Fatalf("%v", err)
		}
	})
	t.Run("duplicate requests have distinct handles", func(t *testing.T) {
		before := count("book")
		out, err := r.RunPrefix(ctx, stream("a=lookup(key='book')\n", "b=lookup(key='book')\nresult=a+b"), nil)
		if err != nil || string(out.Value) != "42" || count("book") != before+2 {
			t.Fatalf("%+v %v", out, err)
		}
	})
	t.Run("dependency waits for final execution", func(t *testing.T) {
		out, err := r.RunPrefix(ctx, stream("key=lookup(key='choose')\n", "a=lookup(key=key)\nresult=a"), nil)
		if err != nil || string(out.Value) != "21" {
			t.Fatalf("%+v %v", out, err)
		}
	})
	t.Run("unselected branch is not prepared", func(t *testing.T) {
		before := count("book")
		out, err := r.RunPrefix(ctx, stream("if False:\n a=lookup(key='book')\n", "result=7"), nil)
		if err != nil || string(out.Value) != "7" || count("book") != before {
			t.Fatalf("%+v %v", out, err)
		}
	})
	t.Run("cancellation during source intake joins workers", func(t *testing.T) {
		runCtx, stop := context.WithCancel(ctx)
		defer stop()
		started, stopped = make(chan struct{}, 1), make(chan struct{})
		chunks := make(chan string, 1)
		chunks <- "a=lookup(key='wait')\n"
		go func() {
			select {
			case <-started:
				stop()
			case <-runCtx.Done():
			}
		}()
		_, err := r.RunPrefix(runCtx, chunks, nil)
		if err == nil {
			t.Fatal("expected cancellation")
		}
		select {
		case <-stopped:
		default:
			t.Fatal("worker still alive")
		}
		started = nil
	})
	t.Run("final syntax error cancels prepared reads", func(t *testing.T) {
		stopped = make(chan struct{})
		_, err := r.RunPrefix(ctx, stream("a=lookup(key='wait')\n", "result = ("), nil)
		if err == nil || !strings.Contains(err.Error(), "SyntaxError") {
			t.Fatalf("%v", err)
		}
		select {
		case <-stopped:
		default:
			t.Fatal("worker still alive")
		}
	})
	t.Run("source bound", func(t *testing.T) {
		_, err := r.RunPrefix(ctx, stream(strings.Repeat("#", maxMessage+1)), nil)
		if err == nil || !strings.Contains(err.Error(), "1 MiB") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("empty stream executes empty program", func(t *testing.T) {
		out, err := r.RunPrefix(ctx, stream(), nil)
		if err != nil || string(out.Value) != "null" {
			t.Fatalf("%+v %v", out, err)
		}
	})
}
