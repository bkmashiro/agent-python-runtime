package wazero

import "context"

type cowPreparedRuntime interface {
	prepare(context.Context, *Engine) (*preparedInstance, error)
	close() error
	imageState() PreparedImageState
}

// PreparedImageState is bounded, body-free evidence about the sealed linear-memory baseline.
type PreparedImageState struct {
	Available            bool   `json:"available"`
	VirtualBytes         uint64 `json:"virtual_bytes"`
	AllocatedBytes       uint64 `json:"allocated_bytes"`
	PageSizeBytes        uint64 `json:"page_size_bytes"`
	ZeroPages            uint64 `json:"zero_pages"`
	NonZeroPages         uint64 `json:"non_zero_pages"`
	SparsePotentialBytes uint64 `json:"sparse_potential_bytes"`
}

func (engine *Engine) PreparedImageState() PreparedImageState {
	if engine == nil || engine.cowRuntime == nil {
		return PreparedImageState{}
	}
	return engine.cowRuntime.imageState()
}
