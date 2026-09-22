package main

import (
	"strings"
	"sync/atomic"
	"testing"
)

func TestQueueCasesKeepLegacyParkAndAddReadFinish(t *testing.T) {
	park, err := selectQueueCase("park-readmit", 8)
	if err != nil {
		t.Fatal(err)
	}
	if !park.wantPark || !strings.Contains(park.code, "approve()") || !strings.Contains(park.code, "bytearray(8388608)") {
		t.Fatalf("park=%+v", park)
	}
	read, err := selectQueueCase("read-finish", 0)
	if err != nil {
		t.Fatal(err)
	}
	if read.wantPark || !strings.Contains(read.code, "probe()") || strings.Contains(read.code, "approve()") {
		t.Fatalf("read=%+v", read)
	}
	if _, err := selectQueueCase("unknown", 0); err == nil {
		t.Fatal("unknown queue case accepted")
	}
}

func TestParseMemoryRollupKeepsRequiredLinuxCounters(t *testing.T) {
	snapshot := parseMemoryRollup([]byte("Rss: 1234 kB\nPss: 987 kB\nPrivate_Dirty: 456 kB\nShared_Clean: 99 kB\n"))
	if snapshot.RSSKB != 1234 || snapshot.PSSKB != 987 || snapshot.PrivateDirtyKB != 456 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestParseMemoryRollupIgnoresMalformedAndUnknownRows(t *testing.T) {
	snapshot := parseMemoryRollup([]byte("Rss: nope kB\nPss: 42 kB\nUnknown: 77 kB\nPrivate_Dirty: 8 kB\n"))
	if snapshot.RSSKB != 0 || snapshot.PSSKB != 42 || snapshot.PrivateDirtyKB != 8 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestMemorySamplerResetStartsANewMeasuredPeak(t *testing.T) {
	var peak atomic.Int64
	updatePeak(&peak, 12)
	updatePeak(&peak, 7)
	if peak.Load() != 12 {
		t.Fatalf("peak=%d", peak.Load())
	}
	peak.Store(0)
	updatePeak(&peak, 7)
	if peak.Load() != 7 {
		t.Fatalf("reset peak=%d", peak.Load())
	}
}
