package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync/atomic"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/durable"
)

type lookupInput struct {
	SKU string `json:"sku" jsonschema:"catalog SKU"`
}

type lookupOutput struct {
	SKU      string `json:"sku"`
	ItemID   string `json:"item_id"`
	Price    int    `json:"price"`
	Currency string `json:"currency"`
}

type detailInput struct {
	ItemID string `json:"item_id" jsonschema:"catalog item ID"`
}

type detailOutput struct {
	ItemID string `json:"item_id"`
	Note   string `json:"note"`
}

type workflowRecord struct {
	SKU      string `json:"sku"`
	ItemID   string `json:"item_id"`
	Price    int    `json:"price"`
	Currency string `json:"currency"`
	Note     string `json:"note"`
}

type sample struct {
	Mode                 string `json:"mode"`
	Iteration            int    `json:"iteration"`
	CreateNS             int64  `json:"create_ns"`
	FetchBatchNS         int64  `json:"fetch_batch_ns"`
	WorkspaceBatchNS     int64  `json:"workspace_batch_ns"`
	TotalNS              int64  `json:"total_ns"`
	ToolCalls            int64  `json:"tool_calls"`
	PeakMCPCalls         int32  `json:"peak_mcp_calls"`
	ToolQueueNS          int64  `json:"tool_queue_ns"`
	ToolServiceNS        int64  `json:"tool_service_ns"`
	ContinuationResumeNS int64  `json:"continuation_resume_ns"`
	ChangedFiles         int    `json:"changed_files"`
	ConflictFree         bool   `json:"conflict_free"`
	OraclePassed         bool   `json:"oracle_passed"`
}

type summary struct {
	Mode           string `json:"mode"`
	Samples        int    `json:"samples"`
	FetchP50NS     int64  `json:"fetch_p50_ns"`
	WorkspaceP50NS int64  `json:"workspace_p50_ns"`
	TotalP50NS     int64  `json:"total_p50_ns"`
}

type report struct {
	Guest         string    `json:"guest"`
	Tasks         int       `json:"tasks"`
	Iterations    int       `json:"iterations"`
	ToolDelayNS   int64     `json:"tool_delay_ns"`
	RunningLimit  int       `json:"running_limit"`
	ResidentLimit int       `json:"resident_limit"`
	Boundary      string    `json:"boundary"`
	Samples       []sample  `json:"samples"`
	Summaries     []summary `json:"summaries"`
}

type phaseMetrics struct {
	calls   atomic.Int64
	active  atomic.Int32
	peak    atomic.Int32
	queue   atomic.Int64
	service atomic.Int64
	resume  atomic.Int64
}

func (metrics *phaseMetrics) reset() {
	metrics.calls.Store(0)
	metrics.active.Store(0)
	metrics.peak.Store(0)
	metrics.queue.Store(0)
	metrics.service.Store(0)
	metrics.resume.Store(0)
}

func (metrics *phaseMetrics) wrap(call pysolate.Tool) pysolate.Tool {
	return func(ctx context.Context, args json.RawMessage) (any, error) {
		active := metrics.active.Add(1)
		defer metrics.active.Add(-1)
		metrics.calls.Add(1)
		for old := metrics.peak.Load(); active > old && !metrics.peak.CompareAndSwap(old, active); old = metrics.peak.Load() {
		}
		return call(ctx, args)
	}
}

func (metrics *phaseMetrics) observe(observation durable.ToolObservation) {
	metrics.queue.Add(observation.QueueDuration.Nanoseconds())
	metrics.service.Add(observation.ServiceDuration.Nanoseconds())
	metrics.resume.Add(observation.ResumeDuration.Nanoseconds())
}

func validateRecord(record workflowRecord) error {
	if record.SKU != "A-1" || record.ItemID != "item-A-1" || record.Price != 123 || record.Currency != "GBP" || record.Note != "approved local catalog record" {
		return fmt.Errorf("unexpected MCP record: %+v", record)
	}
	return nil
}

func summarize(mode string, rows []sample) summary {
	fetch := make([]int64, len(rows))
	workspace := make([]int64, len(rows))
	total := make([]int64, len(rows))
	for index, row := range rows {
		fetch[index], workspace[index], total[index] = row.FetchBatchNS, row.WorkspaceBatchNS, row.TotalNS
	}
	return summary{Mode: mode, Samples: len(rows), FetchP50NS: median(fetch), WorkspaceP50NS: median(workspace), TotalP50NS: median(total)}
}

func median(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	values = append([]int64(nil), values...)
	sort.Slice(values, func(left, right int) bool { return values[left] < values[right] })
	return values[len(values)/2]
}
