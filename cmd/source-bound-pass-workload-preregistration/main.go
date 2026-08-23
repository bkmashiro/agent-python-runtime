package main

import (
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/sourceboundpasses"
)

func main() {
	raw, err := sourceboundpasses.EncodeAuthoredWorkloadPreregistration(sourceboundpasses.AuthoredWorkloadPreregistrationV1())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(append(raw, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
