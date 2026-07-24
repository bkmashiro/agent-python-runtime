package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

var transactionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type dependencies struct {
	openLedger func(string) (*transaction.SQLiteLedger, error)
	now        func() time.Time
}

func productionDependencies() dependencies {
	return dependencies{openLedger: transaction.OpenSQLiteLedger, now: time.Now}
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdout, os.Stderr, productionDependencies()))
}

func execute(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("apyrun-ledger", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databasePath := flags.String("db", "", "absolute path to the Host transaction ledger")
	transactionID := flags.String("transaction", "", "Host transaction identity")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !filepath.IsAbs(*databasePath) || !transactionIDPattern.MatchString(*transactionID) {
		writeDiagnostic(stderr, "usage: apyrun-ledger -db <absolute.db> -transaction <id>")
		return 2
	}
	info, err := os.Lstat(filepath.Clean(*databasePath))
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		writeDiagnostic(stderr, "open transaction ledger")
		return 2
	}
	if deps.openLedger == nil {
		deps.openLedger = transaction.OpenSQLiteLedger
	}
	ledger, err := deps.openLedger(*databasePath)
	if err != nil {
		writeDiagnostic(stderr, "open transaction ledger")
		return 2
	}
	defer ledger.Close()
	if deps.now == nil {
		deps.now = time.Now
	}
	evidence, err := transaction.BuildTransactionEvidence(ledger, *transactionID, deps.now().UTC())
	if err != nil {
		writeDiagnostic(stderr, "build transaction evidence")
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(evidence); err != nil {
		writeDiagnostic(stderr, "write transaction evidence")
		return 1
	}
	return 0
}

func writeDiagnostic(writer io.Writer, message string) {
	const maxDiagnostic = 256
	if len(message) > maxDiagnostic {
		message = message[:maxDiagnostic]
	}
	_, _ = fmt.Fprintln(writer, message)
}
