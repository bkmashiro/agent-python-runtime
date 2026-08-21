package wazero

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxTrustedCOWPrepareBytes  = 16 << 20
	wasmLinearPageSize         = 64 * 1024
	trustedCOWPackageSource    = "import numpy as np\n"
	trustedCOWDerivedPrefix    = "import base64 as _pd_base64, numpy as np\n_pd_body = bytearray(_pd_base64.b64decode(\""
	trustedCOWDerivedSuffix    = "\"))\ndataset = np.frombuffer(_pd_body, dtype=np.dtype('<i8')).reshape((1024, 1024))\n"
	trustedCOWDerivedBodyBytes = 8 << 20
)

var (
	ErrTrustedCOWPrepareSource  = errors.New("trusted COW prepare source is invalid")
	ErrTrustedCOWPrepareBinding = errors.New("trusted COW prepare source does not match the initialized baseline")
	errCOWAllocationShape       = errors.New("COW allocation shape does not match image")
)

func trustedCOWPrepareIdentity(source string) (string, error) {
	if source != trustedCOWPackageSource {
		return "", ErrTrustedCOWPrepareSource
	}
	return trustedCOWSourceIdentity(source), nil
}

func trustedCOWDerivedIdentity(source string) (string, error) {
	if source == "" || len(source) > maxTrustedCOWPrepareBytes || !utf8.ValidString(source) || strings.ContainsRune(source, '\x00') ||
		!strings.HasPrefix(source, trustedCOWDerivedPrefix) || !strings.HasSuffix(source, trustedCOWDerivedSuffix) {
		return "", ErrTrustedCOWPrepareSource
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(source, trustedCOWDerivedPrefix), trustedCOWDerivedSuffix)
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(body) != trustedCOWDerivedBodyBytes {
		return "", ErrTrustedCOWPrepareSource
	}
	return trustedCOWSourceIdentity(source), nil
}

func trustedCOWDerivedSource(body []byte) (string, string, error) {
	if len(body) != trustedCOWDerivedBodyBytes {
		return "", "", ErrTrustedCOWPrepareSource
	}
	source := trustedCOWDerivedPrefix + base64.StdEncoding.EncodeToString(body) + trustedCOWDerivedSuffix
	identity, err := trustedCOWDerivedIdentity(source)
	return source, identity, err
}

func trustedCOWSourceIdentity(source string) string {
	digest := sha256.Sum256([]byte(source))
	return fmt.Sprintf("sha256:%x", digest[:])
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
