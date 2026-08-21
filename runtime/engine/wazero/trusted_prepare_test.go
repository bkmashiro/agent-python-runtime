package wazero

import (
	"context"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestTrustedCOWPrepareSourceValidation(t *testing.T) {
	source := "import numpy as np\n"
	identity, err := trustedCOWPrepareIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	if identity != "sha256:d34aea06c990aeedd2f8f5ff809c1180b92fccca0381269b7c18654043b9a374" {
		t.Fatalf("identity=%s", identity)
	}
	for _, invalid := range []string{"", "import numpy as np\x00", strings.Repeat("x", maxTrustedCOWPrepareBytes+1)} {
		if _, err := trustedCOWPrepareIdentity(invalid); !errors.Is(err, ErrTrustedCOWPrepareSource) {
			t.Fatalf("invalid source len=%d err=%v", len(invalid), err)
		}
	}
	large := "import base64\n" + strings.Repeat("#", (64<<10)+1) + "\n"
	if _, err := trustedCOWPrepareIdentity(large); err != nil {
		t.Fatalf("bounded derived image source err=%v", err)
	}
}

func TestPrepareSemanticRuntimeWithTrustedSourceRequiresCOW(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.PreparedRuntime = true
	engine := &Engine{config: config}
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), "import numpy as np\n"); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestPrepareSemanticRuntimeWithTrustedSourceRejectsBaselineDrift(t *testing.T) {
	source := "import numpy as np\n"
	identity, err := trustedCOWPrepareIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	engine := &Engine{config: config, preparedInitialized: true, preparedTrustedSHA: identity}
	if _, err := engine.ensurePreparedWithResult(context.Background()); err != nil {
		t.Fatalf("ordinary consumer err=%v", err)
	}
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), source); err != nil {
		t.Fatalf("same source err=%v", err)
	}
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), "import numpy\n"); !errors.Is(err, ErrTrustedCOWPrepareBinding) {
		t.Fatalf("drift err=%v", err)
	}
}

func TestCOWBaselineGrowthPages(t *testing.T) {
	if pages, err := cowBaselineGrowthPages(2*wasmLinearPageSize, 4*wasmLinearPageSize); err != nil || pages != 2 {
		t.Fatalf("pages=%d err=%v", pages, err)
	}
	if pages, err := cowBaselineGrowthPages(4*wasmLinearPageSize, 4*wasmLinearPageSize); err != nil || pages != 0 {
		t.Fatalf("equal pages=%d err=%v", pages, err)
	}
	for _, values := range [][2]uint64{{4 * wasmLinearPageSize, 2 * wasmLinearPageSize}, {wasmLinearPageSize + 1, 2 * wasmLinearPageSize}, {wasmLinearPageSize, 2*wasmLinearPageSize + 1}} {
		if _, err := cowBaselineGrowthPages(values[0], values[1]); err == nil {
			t.Fatalf("accepted current=%d baseline=%d", values[0], values[1])
		}
	}
}
