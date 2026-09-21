package main

import (
	"testing"
	"time"
)

func TestSummarizeUsesObservedSamples(t *testing.T) {
	rows := []sample{
		{FetchBatchNS: int64(30 * time.Millisecond), WorkspaceBatchNS: int64(9 * time.Millisecond), TotalNS: int64(40 * time.Millisecond)},
		{FetchBatchNS: int64(10 * time.Millisecond), WorkspaceBatchNS: int64(5 * time.Millisecond), TotalNS: int64(20 * time.Millisecond)},
		{FetchBatchNS: int64(20 * time.Millisecond), WorkspaceBatchNS: int64(7 * time.Millisecond), TotalNS: int64(30 * time.Millisecond)},
	}
	got := summarize("external_io", rows)
	if got.Samples != 3 || got.FetchP50NS != int64(20*time.Millisecond) || got.WorkspaceP50NS != int64(7*time.Millisecond) || got.TotalP50NS != int64(30*time.Millisecond) {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestValidateRecord(t *testing.T) {
	valid := workflowRecord{SKU: "A-1", ItemID: "item-A-1", Price: 123, Currency: "GBP", Note: "approved local catalog record"}
	if err := validateRecord(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Price = 0
	if err := validateRecord(invalid); err == nil {
		t.Fatal("expected invalid record")
	}
}
