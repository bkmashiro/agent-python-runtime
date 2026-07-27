package wazero

import (
	"fmt"
	"sort"

	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// ReactorRestoreDecision is a fail-closed conclusion about whether a compiled
// reactor has enough observable state to support reuse after execution.
type ReactorRestoreDecision string

const (
	ReactorRestoreSingleUseOnly ReactorRestoreDecision = "single-use-only"
	ReactorRestoreEligible      ReactorRestoreDecision = "restore-eligible"
	guestMemoryExport                                  = "memory"
)

// ReactorMemoryCensus describes the linear-memory shape visible through
// wazero's public CompiledModule API. Count is the number of distinct observed
// memory indices, not a claim about otherwise unobservable definitions.
type ReactorMemoryCensus struct {
	Count              int    `json:"count"`
	ImportedCount      int    `json:"imported_count"`
	ExportName         string `json:"export_name,omitempty"`
	MinPages           uint32 `json:"min_pages"`
	MaxPages           uint32 `json:"max_pages,omitempty"`
	MaxDeclared        bool   `json:"max_declared"`
	Fixed              bool   `json:"fixed"`
	VisibilityComplete bool   `json:"visibility_complete"`
	COWEligible        bool   `json:"cow_eligible"`
}

// ReactorStateCensus records what is proven and what remains opaque. Unknown
// state classes intentionally block reusable-restore claims.
type ReactorStateCensus struct {
	SchemaVersion       int                    `json:"schema_version"`
	Memory              ReactorMemoryCensus    `json:"memory"`
	UnknownStateClasses []string               `json:"unknown_state_classes"`
	RestoreDecision     ReactorRestoreDecision `json:"restore_decision"`
	Reasons             []string               `json:"reasons"`
}

// Validate checks internal consistency; it does not widen what the public
// wazero API can prove.
func (census ReactorStateCensus) Validate() error {
	if census.SchemaVersion != 1 || len(census.Reasons) == 0 {
		return fmt.Errorf("reactor state census identity is incomplete")
	}
	if census.Memory.COWEligible && (!census.Memory.Fixed || !census.Memory.VisibilityComplete ||
		census.Memory.Count != 1 || census.Memory.ImportedCount != 0 || census.Memory.ExportName != guestMemoryExport) {
		return fmt.Errorf("reactor state census has inconsistent COW memory eligibility")
	}
	switch census.RestoreDecision {
	case ReactorRestoreSingleUseOnly:
		return nil
	case ReactorRestoreEligible:
		if !census.Memory.COWEligible || len(census.UnknownStateClasses) != 0 {
			return fmt.Errorf("reactor state census cannot prove reusable restore")
		}
		return nil
	default:
		return fmt.Errorf("reactor state census has unknown restore decision %q", census.RestoreDecision)
	}
}

func censusCompiledReactor(compiled wazerort.CompiledModule) ReactorStateCensus {
	census := ReactorStateCensus{
		SchemaVersion:       1,
		UnknownStateClasses: []string{"mutable-globals", "tables"},
		RestoreDecision:     ReactorRestoreSingleUseOnly,
	}
	if compiled == nil {
		census.Reasons = []string{"compiled module is nil"}
		return census
	}

	imported := compiled.ImportedMemories()
	exported := compiled.ExportedMemories()
	definitions := make(map[uint32]api.MemoryDefinition, len(imported)+len(exported))
	for _, definition := range imported {
		definitions[definition.Index()] = definition
	}
	for _, definition := range exported {
		definitions[definition.Index()] = definition
	}
	census.Memory.Count = len(definitions)
	census.Memory.ImportedCount = len(imported)

	memory, ok := exported[guestMemoryExport]
	if !ok {
		census.Reasons = append(census.Reasons, fmt.Sprintf("required exported memory %q is not visible", guestMemoryExport))
	} else {
		census.Memory.ExportName = guestMemoryExport
		census.Memory.MinPages = memory.Min()
		census.Memory.MaxPages, census.Memory.MaxDeclared = memory.Max()
		census.Memory.Fixed = census.Memory.MaxDeclared && census.Memory.MinPages == census.Memory.MaxPages
	}

	// WebAssembly Core 2.0 permits at most one memory. Seeing the reactor's
	// required export therefore makes memory visibility complete for this
	// supported artifact class.
	census.Memory.VisibilityComplete = ok && census.Memory.Count == 1
	if len(imported) != 0 {
		census.Reasons = append(census.Reasons, "imported linear memory is not allocator-owned")
	}
	if !census.Memory.VisibilityComplete {
		census.Reasons = append(census.Reasons, "linear-memory ownership or visibility is incomplete")
	}
	if ok && !census.Memory.Fixed {
		census.Reasons = append(census.Reasons, "linear memory is growable")
	}
	census.Memory.COWEligible = ok && len(imported) == 0 && census.Memory.VisibilityComplete && census.Memory.Fixed

	for _, stateClass := range census.UnknownStateClasses {
		census.Reasons = append(census.Reasons, stateClass+" are not exhaustively observable through wazero public APIs")
	}
	sort.Strings(census.Reasons)
	return census
}
