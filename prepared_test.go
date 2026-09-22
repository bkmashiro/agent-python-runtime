package pysolate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPreparedGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	calls := make(chan string, 32)
	var second chan struct{}
	read := func(ctx context.Context, raw json.RawMessage) (any, error) {
		var a struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, err
		}
		calls <- a.Key
		switch a.Key {
		case "first":
			select {
			case <-second:
				return 21, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		case "second":
			close(second)
			return 5, nil

		default:
			return 21, nil
		}
	}
	r, err := NewPrepared(ctx, wasm, Manifest{"lookup": {Call: read, AllowEarlyRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(context.Background())
	if len(r.image) == 0 {
		t.Fatal("missing actual initialized image")
	}
	if len(calls) != 0 {
		t.Fatal("capture called a Host tool")
	}
	expect := func(t *testing.T, out Output, err error, want string) {
		t.Helper()
		if err != nil || string(out.Value) != want {
			t.Fatalf("%+v %v; want %s", out, err, want)
		}
	}

	t.Run("ordinary tool", func(t *testing.T) {
		out, err := r.Run(ctx, `result=lookup(key="book")`, nil)
		expect(t, out, err, "21")
	})
	t.Run("private Python state", func(t *testing.T) {
		out, err := r.Run(ctx, `import builtins; builtins.secret=123; result=builtins.secret`, nil)
		expect(t, out, err, "123")
		out, err = r.Run(ctx, `import builtins; result=hasattr(builtins,"secret")`, nil)
		expect(t, out, err, "false")
	})
	t.Run("two live memories and image are independent", func(t *testing.T) {
		a, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close(context.Background())
		b, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
		if err != nil {
			t.Fatal(err)
		}
		defer b.Close(context.Background())
		// Last byte is only a memory-isolation probe. These two instances execute no code afterward.
		offset := uint32(len(r.image) - 1)
		before := r.image[offset]
		if !a.Memory().WriteByte(offset, before^255) {
			t.Fatal("write")
		}
		value, ok := b.Memory().ReadByte(offset)
		if !ok || value != before || r.image[offset] != before {
			t.Fatal("shared writable storage")
		}
	})
	t.Run("early reads still overlap", func(t *testing.T) {
		second = make(chan struct{})
		out, err := r.RunWithEarlyReads(ctx, "a=lookup(key='first')\nb=lookup(key='second')\nresult=a+b", nil)
		expect(t, out, err, "26")
		if !strings.Contains(out.Transformed, "_pysolate_prepare") {
			t.Fatal("early-read preparation not used")
		}
	})
	t.Run("error does not change image", func(t *testing.T) {
		if _, err := r.Run(ctx, `raise ValueError("bad")`, nil); err == nil {
			t.Fatal("missing error")
		}
		out, err := r.Run(ctx, `result=7`, nil)
		expect(t, out, err, "7")
	})
	t.Run("cancel does not change image", func(t *testing.T) {
		deadline, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		if _, err := r.Run(deadline, `while True: pass`, nil); err == nil || deadline.Err() == nil {
			t.Fatalf("expected deadline: %v", err)
		}
		out, err := r.Run(ctx, `result=7`, nil)
		expect(t, out, err, "7")
	})
}
