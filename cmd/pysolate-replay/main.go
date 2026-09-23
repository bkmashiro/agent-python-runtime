// Command pysolate-replay exports one ended durable Run or replays a local
// bundle against an exact Guest artifact without dispatching any real Tool.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bkmashiro/agent-python-runtime/durable"
)

func main() {
	args := os.Args[1:]
	if err := run(args, os.Stdout, os.Stderr); err != nil {
		// Do not print err: bundle and Guest failures can contain private source,
		// inputs, tool arguments, outcomes or Python exception text.
		fmt.Fprintln(os.Stderr, cliFailureMessage(args, err))
		os.Exit(1)
	}
}

func cliFailureMessage(args []string, err error) string {
	category := cliFailureCategory(args, err)
	detail := fmt.Sprintf("category=%s", category)
	var mismatch *durable.ReplayMismatchError
	if errors.As(err, &mismatch) && mismatch != nil && category != durable.ReplayErrorCategoryUsage {
		// Only fixed labels are printable, even if a caller constructed this type.
		switch mismatch.Reason {
		case durable.ReplayMismatchReasonArtifactIdentity, durable.ReplayMismatchReasonCallExcess,
			durable.ReplayMismatchReasonCallUnconsumed, durable.ReplayMismatchReasonCallSequence,
			durable.ReplayMismatchReasonCallID, durable.ReplayMismatchReasonCallTool,
			durable.ReplayMismatchReasonCallOperationKey, durable.ReplayMismatchReasonCallArguments,
			durable.ReplayMismatchReasonTerminalValue, durable.ReplayMismatchReasonTerminalStdout,
			durable.ReplayMismatchReasonTerminalTransformed, durable.ReplayMismatchReasonTerminalError:
			detail += " reason=" + string(mismatch.Reason)
		}
		if category == durable.ReplayErrorCategoryCall && mismatch.HasSequence {
			detail += fmt.Sprintf(" sequence=%d", mismatch.Sequence)
		}
	}
	return "pysolate-replay: operation failed (" + detail + ")"
}

func cliFailureCategory(args []string, err error) durable.ReplayErrorCategory {
	if err == nil {
		return ""
	}
	if len(args) == 0 || (args[0] != "export" && args[0] != "replay") || isCLIUsageError(err) {
		return durable.ReplayErrorCategoryUsage
	}
	if category := durable.ErrorCategory(err); category != durable.ReplayErrorCategoryRuntime {
		return category
	}
	return durable.ReplayErrorCategoryRuntime
}

type cliUsageError struct{ message string }

func (err *cliUsageError) Error() string { return err.message }

func isCLIUsageError(err error) bool {
	_, ok := err.(*cliUsageError)
	return ok
}

func usageError(message string) error { return &cliUsageError{message: message} }

func run(args []string, stdout, _ io.Writer) error {
	if len(args) == 0 {
		return usageError("subcommand is required")
	}
	switch args[0] {
	case "export":
		return runExport(args[1:], stdout)
	case "replay":
		return runReplay(args[1:], stdout)
	default:
		return usageError("unknown subcommand")
	}
}

func runExport(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", "", "existing durable database")
	runID := flags.String("run", "", "ended Run ID")
	outPath := flags.String("out", "", "new local bundle path")
	if err := flags.Parse(args); err != nil || *dbPath == "" || *runID == "" || *outPath == "" || flags.NArg() != 0 {
		return usageError("export requires -db, -run and -out")
	}
	info, err := os.Stat(*dbPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("durable database must already exist")
	}
	if err := durable.ExportRun(context.Background(), *dbPath, *runID, *outPath); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"ok": true, "operation": "export", "run_id": *runID,
		"privacy_warning": durable.RunBundlePrivacyWarning,
	})
}

func runReplay(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("replay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	bundlePath := flags.String("bundle", "", "local Run bundle")
	guestPath := flags.String("guest", "", "exact Guest artifact")
	timeout := flags.Duration("timeout", 2*time.Minute, "replay deadline")
	if err := flags.Parse(args); err != nil || *bundlePath == "" || *guestPath == "" || *timeout <= 0 || flags.NArg() != 0 {
		return usageError("replay requires -bundle and -guest")
	}
	bundle, err := durable.ReadBundle(*bundlePath)
	if err != nil {
		return err
	}
	guestInfo, err := os.Stat(*guestPath)
	if err != nil || !guestInfo.Mode().IsRegular() || guestInfo.Size() <= 0 {
		return errors.New("Guest artifact must be a non-empty regular file")
	}
	guest, err := os.ReadFile(*guestPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if _, err := durable.ReplayBundle(ctx, bundle, guest); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"ok": true, "operation": "replay", "run_id": bundle.Run.ID,
		"status": bundle.Run.Status,
	})
}
