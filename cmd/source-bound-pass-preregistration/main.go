package main

import (
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/sourceboundpasses"
)

func main() {
	raw, err := sourceboundpasses.EncodePreregistration(sourceboundpasses.PreregistrationV1())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(append(raw, '\n'))
}
