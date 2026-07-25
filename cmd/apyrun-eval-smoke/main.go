package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/eval/harness"
)

func run(args []string) error {
	flags := flag.NewFlagSet("apyrun-eval-smoke", flag.ContinueOnError)
	datasetRoot := flags.String("dataset", "", "absolute or relative dataset root")
	prompts := flags.String("prompts", "", "prompt manifest path")
	schemas := flags.String("schemas", "", "evaluation schema directory")
	output := flags.String("out", "", "output directory")
	commit := flags.String("commit", "", "40-hex repository commit")
	host := flags.String("host-digest", "", "Host artifact SHA-256 digest")
	guest := flags.String("guest-digest", "", "Guest artifact SHA-256 digest")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fmt.Errorf("invalid arguments")
	}
	summary, err := harness.RunSmoke(harness.Config{DatasetRoot: *datasetRoot, PromptManifestPath: *prompts, SchemaDir: *schemas, OutputDir: *output, RepositoryCommit: *commit, HostArtifactDigest: *host, GuestArtifactDigest: *guest})
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(encoded))
	return err
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
