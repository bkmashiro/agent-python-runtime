package integration_test

import (
	"os"
	"path/filepath"
)

func readGuestArtifact() ([]byte, error) {
	path := os.Getenv("PYSOLATE_GUEST")
	if path == "" {
		path = filepath.Join("..", "dist", "pysolate.wasm")
	}
	return os.ReadFile(path)
}
