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
		fatal(errors.New("usage: eager-style-gate-contract <target-python> <output>"))
	}
	value, err := semanticspeculation.NewEagerStyleGateV1(os.Args[1])
	if err != nil {
		fatal(err)
	}
	sealed, err := semanticspeculation.SealEagerComparatorContract(value)
	if err != nil {
		fatal(err)
	}
	raw, err := semanticspeculation.EncodeEagerComparatorContract(sealed)
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
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		fatal(err)
	}
	if err := file.Close(); err != nil {
		fatal(err)
	}
	fmt.Println(sealed.Identity)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
