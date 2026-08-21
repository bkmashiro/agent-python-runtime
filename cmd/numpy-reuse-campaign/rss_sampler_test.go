package main

import (
	"runtime"
	"testing"
	"time"
)

func TestPerTrialRSSSamplerStopsIdempotently(t *testing.T) {
	sampler := startRSSSampler()
	payload := make([]byte, 8<<20)
	for i := range payload {
		payload[i] = byte(i)
	}
	time.Sleep(30 * time.Millisecond)
	first := sampler.Stop()
	second := sampler.Stop()
	if first == 0 || second != first {
		t.Fatalf("invalid sampler result first=%d second=%d", first, second)
	}
	runtime.KeepAlive(payload)
}
