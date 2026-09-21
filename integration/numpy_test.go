package integration_test

import (
	"context"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

func TestNumPyLegacyAndGeneratorABIs(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	runner, err := pysolate.New(context.Background(), wasm, pysolate.Manifest{})
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	source := `import numpy as np
modern = np.random.default_rng(17)
legacy = np.random.RandomState(17)
a = modern.multinomial(1 << 33, [0.2, 0.3, 0.5])
b = legacy.multinomial(10, [0.2, 0.3, 0.5])
assert a.dtype.itemsize == 8 and int(a.sum()) == (1 << 33), "Generator int64 ABI"
assert b.dtype.itemsize == 4 and int(b.sum()) == 10, "RandomState long ABI"
assert 20000 < int(modern.binomial(100, 0.3, size=1000).sum()) < 40000, "Generator binomial cache layout"
assert 20000 < int(legacy.binomial(100, 0.3, size=1000).sum()) < 40000, "RandomState binomial cache layout"
assert modern.poisson(5, size=4).dtype.itemsize == 8
assert legacy.poisson(5, size=4).dtype.itemsize == 4
result = {"modern": a.tolist(), "legacy": b.tolist()}
`
	out, err := runner.Run(context.Background(), source, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("NumPy static ABI: %s", out.Value)
}
