package wazero

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
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
	for _, invalid := range []string{"", "import numpy as np\x00", "import numpy\n", strings.Repeat("x", maxTrustedCOWPrepareBytes+1)} {
		if _, err := trustedCOWPrepareIdentity(invalid); !errors.Is(err, ErrTrustedCOWPrepareSource) {
			t.Fatalf("invalid source len=%d err=%v", len(invalid), err)
		}
	}
	derived := testTrustedDerivedSource()
	if _, err := trustedCOWDerivedIdentity(derived); err != nil {
		t.Fatalf("derived source err=%v", err)
	}
	for _, invalid := range []string{"dataset = 1\n", derived + "result = 1\n", strings.Replace(derived, "<i8", "<f8", 1)} {
		if _, err := trustedCOWDerivedIdentity(invalid); !errors.Is(err, ErrTrustedCOWPrepareSource) {
			t.Fatalf("arbitrary derived source accepted err=%v", err)
		}
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
	if err := engine.PrepareSemanticRuntimeWithTrustedSource(context.Background(), "import numpy\n"); !errors.Is(err, ErrTrustedCOWPrepareSource) {
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

type fakeDerivableCOWRuntime struct {
	identity string
	parent   string
	closed   int
	derived  int
}

func (runtime *fakeDerivableCOWRuntime) prepare(context.Context, *Engine) (*preparedInstance, cowCloneLifecycle, error) {
	return nil, cowCloneLifecycle{}, errors.New("not used")
}
func (runtime *fakeDerivableCOWRuntime) close() error { runtime.closed++; return nil }
func (runtime *fakeDerivableCOWRuntime) imageState() PreparedImageState {
	return PreparedImageState{Available: true, TrustedPrepareSHA256: runtime.identity, ParentTrustedPrepareSHA256: runtime.parent}
}
func (runtime *fakeDerivableCOWRuntime) derive(_ context.Context, _ *Engine, _ string, identity string) (cowPreparedRuntime, error) {
	runtime.derived++
	return &fakeDerivableCOWRuntime{identity: identity, parent: runtime.identity}, nil
}

func TestDeriveSemanticRuntimeRetainsPackageParent(t *testing.T) {
	packageIdentity, _ := trustedCOWPrepareIdentity("import numpy as np\n")
	firstSource := testTrustedDerivedSource()
	secondSource := strings.Replace(firstSource, trustedCOWDerivedPrefix+"A", trustedCOWDerivedPrefix+"Q", 1)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = true
	parent := &fakeDerivableCOWRuntime{identity: packageIdentity}
	engine := &Engine{config: config, preparedInitialized: true, preparedTrustedSHA: packageIdentity, cowRuntime: parent}
	if err := engine.DeriveSemanticRuntimeWithTrustedSource(context.Background(), firstSource); err != nil {
		t.Fatal(err)
	}
	first := engine.cowRuntime.(*fakeDerivableCOWRuntime)
	if engine.cowParentRuntime != parent || first.parent != packageIdentity || parent.derived != 1 {
		t.Fatalf("parent=%p first=%+v parent_state=%+v", engine.cowParentRuntime, first, parent)
	}
	if err := engine.DeriveSemanticRuntimeWithTrustedSource(context.Background(), secondSource); err != nil {
		t.Fatal(err)
	}
	if parent.derived != 2 || first.closed != 1 || engine.cowParentRuntime != parent {
		t.Fatalf("parent=%+v first=%+v", parent, first)
	}
	if err := engine.closeCOWRuntime(); err != nil {
		t.Fatal(err)
	}
	if parent.closed != 1 {
		t.Fatalf("parent closed=%d", parent.closed)
	}
}

func testTrustedDerivedSource() string {
	encoded := base64.StdEncoding.EncodeToString(make([]byte, trustedCOWDerivedBodyBytes))
	return fmt.Sprintf("%s%s%s", trustedCOWDerivedPrefix, encoded, trustedCOWDerivedSuffix)
}
