package main

import (
	"strings"
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
