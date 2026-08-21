package wazero

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxTrustedCOWPrepareBytes = 16 << 20
	wasmLinearPageSize        = 64 * 1024
)

var (
	ErrTrustedCOWPrepareSource  = errors.New("trusted COW prepare source is invalid")
	ErrTrustedCOWPrepareBinding = errors.New("trusted COW prepare source does not match the initialized baseline")
	errCOWAllocationShape       = errors.New("COW allocation shape does not match image")
)

func trustedCOWPrepareIdentity(source string) (string, error) {
	if source == "" || len([]byte(source)) > maxTrustedCOWPrepareBytes || !utf8.ValidString(source) || strings.ContainsRune(source, '\x00') {
		return "", ErrTrustedCOWPrepareSource
	}
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

func cowBaselineGrowthPages(currentBytes, baselineBytes uint64) (uint32, error) {
	if currentBytes == 0 || baselineBytes == 0 || currentBytes%wasmLinearPageSize != 0 || baselineBytes%wasmLinearPageSize != 0 || currentBytes > baselineBytes {
		return 0, errCOWAllocationShape
	}
	pages := (baselineBytes - currentBytes) / wasmLinearPageSize
	if pages > uint64(^uint32(0)) {
		return 0, errCOWAllocationShape
	}
	return uint32(pages), nil
}

type cowPreparedRuntime interface {
	prepare(context.Context, *Engine) (*preparedInstance, cowCloneLifecycle, error)
	close() error
	imageState() PreparedImageState
}

type derivableCOWPreparedRuntime interface {
	cowPreparedRuntime
	derive(context.Context, *Engine, string, string) (cowPreparedRuntime, error)
}

type cowCloneLifecycle struct {
	ModuleInstantiations uint32
	InitializeCalls      uint32
}

// PreparedImageState is bounded, body-free evidence about the sealed linear-memory baseline.
type PreparedImageState struct {
	Available                  bool   `json:"available"`
	BaselineBytes              uint64 `json:"baseline_bytes"`
	VirtualBytes               uint64 `json:"virtual_bytes"`
	AllocatedBytes             uint64 `json:"allocated_bytes"`
	PageSizeBytes              uint64 `json:"page_size_bytes"`
	ZeroPages                  uint64 `json:"zero_pages"`
	NonZeroPages               uint64 `json:"non_zero_pages"`
	SparsePotentialBytes       uint64 `json:"sparse_potential_bytes"`
	TrustedPrepareSHA256       string `json:"trusted_prepare_sha256,omitempty"`
	ParentTrustedPrepareSHA256 string `json:"parent_trusted_prepare_sha256,omitempty"`
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
