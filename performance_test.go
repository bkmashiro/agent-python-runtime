package pysolate

import (
	"context"
	"testing"
	"time"
)

// Diagnostic only: split the same fresh lifecycle used by Run, without production observers.
func BenchmarkFreshLifecycle(b *testing.B) {
	wasm, err := readGuestArtifact()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now()
	r, err := New(ctx, wasm, nil)
	if err != nil {
		b.Fatal(err)
	}
	construction := time.Since(start)
	defer r.Close(ctx)
	var instantiate, initialize, python, closeTime time.Duration
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stdout, stderr := &boundedText{}, &boundedText{}
		t := time.Now()
		m, e := r.runtime.InstantiateModule(ctx, r.code, r.moduleConfig(ctx, stdout, stderr))
		instantiate += time.Since(t)
		if e != nil {
			b.Fatal(e)
		}
		t = time.Now()
		_, e = m.ExportedFunction("_initialize").Call(ctx)
		initialize += time.Since(t)
		if e != nil {
			b.Fatal(e)
		}
		t = time.Now()
		v, e := m.ExportedFunction("init").Call(ctx)
		python += time.Since(t)
		if e != nil || len(v) != 1 || v[0] != 0 {
			b.Fatalf("init=%v error=%v stderr=%s", v, e, stderr.String())
		}
		t = time.Now()
		if e = m.Close(ctx); e != nil {
			b.Fatal(e)
		}
		closeTime += time.Since(t)
	}
	b.StopTimer()
	b.ReportMetric(float64(construction.Nanoseconds()), "construction-ns")
	b.ReportMetric(float64(instantiate.Nanoseconds())/float64(b.N), "instantiate-ns/op")
	b.ReportMetric(float64(initialize.Nanoseconds())/float64(b.N), "initialize-ns/op")
	b.ReportMetric(float64(python.Nanoseconds())/float64(b.N), "python-init-ns/op")
	b.ReportMetric(float64(closeTime.Nanoseconds())/float64(b.N), "close-ns/op")
}
