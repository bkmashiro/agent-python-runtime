package wazero

import "context"

type cowPreparedRuntime interface {
	prepare(context.Context, *Engine) (*preparedInstance, cowCloneLifecycle, error)
	close() error
	imageState() PreparedImageState
}

type cowCloneLifecycle struct {
	ModuleInstantiations uint32
	InitializeCalls      uint32
}

// PreparedImageState is bounded, body-free evidence about the sealed linear-memory baseline.
type PreparedImageState struct {
	Available            bool   `json:"available"`
	BaselineBytes        uint64 `json:"baseline_bytes"`
	VirtualBytes         uint64 `json:"virtual_bytes"`
	AllocatedBytes       uint64 `json:"allocated_bytes"`
	PageSizeBytes        uint64 `json:"page_size_bytes"`
	ZeroPages            uint64 `json:"zero_pages"`
	NonZeroPages         uint64 `json:"non_zero_pages"`
	SparsePotentialBytes uint64 `json:"sparse_potential_bytes"`
}

func (engine *Engine) PreparedImageState() PreparedImageState {
	if engine == nil {
		return PreparedImageState{}
	}
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	if engine.cowRuntime == nil {
		return PreparedImageState{}
	}
	return engine.cowRuntime.imageState()
}
