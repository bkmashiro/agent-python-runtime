package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func main() {
	guest := flag.String("guest", "", "path to the verified WASI guest artifact")
	datasetRoot := flag.String("dataset", "eval/agentic/routing/v1", "routing diagnostic dataset root")
	repositoryCommit := flag.String("repository-commit", "", "exact lowercase 40-hex repository commit")
	output := flag.String("output", "", "new output report path")
	flag.Parse()
	if *guest == "" || *datasetRoot == "" || *repositoryCommit == "" || *output == "" {
		fatal("all flags are required")
	}
	wasm, err := os.ReadFile(*guest)
	if err != nil {
		fatal(err.Error())
	}
	manifestBytes, err := os.ReadFile(filepath.Join(*datasetRoot, "manifest.json"))
	if err != nil {
		fatal(err.Error())
	}
	planBytes, err := os.ReadFile(filepath.Join(*datasetRoot, "development-plan.json"))
	if err != nil {
		fatal(err.Error())
	}
	data, err := agentic.LoadRoutingDataset(*datasetRoot)
	if err != nil {
		fatal(err.Error())
	}
	report, err := agentic.RunMechanismBaseline(context.Background(), wasm, runtimeconfig.DefaultRunConfig(), data, agentic.MechanismBaselineIdentity{
		RepositoryCommit: *repositoryCommit, GuestArtifactSHA256: digest(wasm), DatasetManifestSHA256: digest(manifestBytes), DatasetPlanSHA256: digest(planBytes),
	})
	if err != nil {
		fatal(err.Error())
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fatal(err.Error())
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(*output)
		}
	}()
	if _, err := file.Write(encoded); err != nil {
		fatal(err.Error())
	}
	if err := file.Sync(); err != nil {
		fatal(err.Error())
	}
	if err := file.Close(); err != nil {
		fatal(err.Error())
	}
	remove = false
	fmt.Printf("%s\n", digest(encoded))
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
