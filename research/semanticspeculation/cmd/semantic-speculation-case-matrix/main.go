package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
)

func main() {
	if len(os.Args) != 2 {
		fatal(errors.New("usage: semantic-speculation-case-matrix OUTPUT"))
	}
	matrix, err := semanticspeculation.NewSyntheticCaseMatrix()
	if err != nil {
		fatal(err)
	}
	sealed, err := semanticspeculation.SealSyntheticCaseMatrix(matrix)
	if err != nil {
		fatal(err)
	}
	raw, err := semanticspeculation.EncodeSyntheticCaseMatrix(sealed)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(os.Args[1]), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(os.Args[1], raw, 0o644); err != nil {
		fatal(err)
	}
	fmt.Println(sealed.Identity)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
