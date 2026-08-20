package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
)

func main() {
	if len(os.Args) != 3 {
		fatal(errors.New("usage: semantic-speculation-preregister <40-hex-parent-commit> <output>"))
	}
	value, err := semanticspeculation.NewV1Preregistration(os.Args[1])
	if err != nil {
		fatal(err)
	}
	sealed, err := semanticspeculation.SealPreregistration(value)
	if err != nil {
		fatal(err)
	}
	encoded, err := semanticspeculation.EncodePreregistration(sealed)
	if err != nil {
		fatal(err)
	}
	output := os.Args[2]
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fatal(err)
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fatal(err)
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		os.Remove(output)
		fatal(err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(output)
		fatal(err)
	}
	if err := file.Close(); err != nil {
		os.Remove(output)
		fatal(err)
	}
	fmt.Println(sealed.Identity)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
