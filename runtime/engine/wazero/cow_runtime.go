package wazero

import "context"

type cowPreparedRuntime interface {
	prepare(context.Context, *Engine, string) (*preparedInstance, error)
	close() error
}

// PreparedImageState reports immutable baseline shape plus a point-in-time
// allocated-block census. ZeroPages is a measurement, not a promise that sparse
// storage will produce equal savings. AllocatedBytes is not an identity field.
type PreparedImageState struct {
	Available              bool   `json:"available"`
	VirtualBytes           uint64 `json:"virtual_bytes"`
	AllocatedBytes         uint64 `json:"allocated_bytes"`
	PageSizeBytes          uint64 `json:"page_size_bytes"`
	ZeroPages              uint64 `json:"zero_pages"`
	NonZeroPages           uint64 `json:"non_zero_pages"`
	SparsePotentialBytes   uint64 `json:"sparse_potential_bytes"`
	WarmupProfile          string `json:"warmup_profile,omitempty"`
	WarmupGenerationSHA256 string `json:"warmup_generation_sha256,omitempty"`
}

type PreparedWarmupState struct {
	Profile          string `json:"profile"`
	GenerationSHA256 string `json:"generation_sha256"`
}

func (engine *Engine) PreparedWarmupState() PreparedWarmupState {
	if engine == nil {
		return PreparedWarmupState{}
	}
	return PreparedWarmupState{
		Profile: engine.preparedWarmupProfile, GenerationSHA256: engine.preparedWarmupGeneration,
	}
}

type cowPreparedImageReporter interface {
	preparedImageState() PreparedImageState
}

func (engine *Engine) PreparedImageState() PreparedImageState {
	if engine == nil || engine.cowRuntime == nil {
		return PreparedImageState{}
	}
	reporter, ok := engine.cowRuntime.(cowPreparedImageReporter)
	if !ok {
		return PreparedImageState{}
	}
	return reporter.preparedImageState()
}
