package pysolate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"testing"
)

func TestPreparedRecordedMatchesFresh(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	seed := "prepared-recorded-seed"
	manifest := Manifest{"action": {Call: func(context.Context, json.RawMessage) (any, error) { return 7, nil }}}
	journal := journalFunc(func(ctx context.Context, _ string, _ json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
		return next(ctx), nil
	})
	source := `import os, random, time
import numpy as np
time.sleep(0.003)
result = {"bytes":os.urandom(17).hex(), "random":random.Random().getrandbits(64), "set":list({"alpha","beta","gamma","delta"}), "wall":time.time(), "mono":time.monotonic(), "numpy":np.random.default_rng().integers(0,100000,size=4).tolist(), "tool":action()}`
	fresh, err := New(ctx, wasm, manifest)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := fresh.RunRecorded(ctx, source, nil, seed, journal)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Close(ctx)
	modes := []string{"copy"}
	if runtime.GOOS == "linux" {
		modes = append(modes, "cow", "cow-data-image")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			var r *Runner
			var err error
			if mode == "copy" {
				r, err = NewPreparedRecorded(ctx, wasm, manifest, seed)
			} else {
				r, err = NewPreparedRecordedCOW(ctx, wasm, manifest, seed, COWOptions{DataImage: mode == "cow-data-image"})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close(ctx)
			// Concurrent attempts must not share mutable RNG/clock or Python memory.
			var group sync.WaitGroup
			for i := 0; i < 3; i++ {
				group.Add(1)
				go func() {
					defer group.Done()
					out, e := r.RunRecorded(ctx, source, nil, seed, journal)
					if e != nil || !bytes.Equal(out.Value, expected.Value) {
						t.Errorf("fresh=%s prepared=%s error=%v", expected.Value, out.Value, e)
					}
				}()
			}
			group.Wait()
			if _, err := r.RunRecorded(ctx, "result=action()", nil, "different-seed", journal); err == nil {
				t.Fatal("mismatched seed accepted")
			}
			parked := errors.New("park")
			stop := journalFunc(func(context.Context, string, json.RawMessage, func(context.Context) []byte) ([]byte, error) {
				return nil, parked
			})
			if _, err := r.RunRecorded(ctx, "try:\n action()\nexcept BaseException:\n result=99", nil, seed, stop); !errors.Is(err, parked) {
				t.Fatalf("park=%v", err)
			}
			out, e := r.RunRecorded(ctx, source, nil, seed, journal)
			if e != nil || !bytes.Equal(out.Value, expected.Value) {
				t.Fatalf("after park=%s error=%v", out.Value, e)
			}
		})
	}
}
