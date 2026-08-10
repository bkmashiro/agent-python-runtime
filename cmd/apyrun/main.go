package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const exitEscalationRequired = 3

type dependencies struct {
	readFile    func(string) ([]byte, error)
	newIdentity func() (string, error)
	newFactory  func(operatorConfig, string, *http.Client) (engine.Factory, error)
	httpClient  *http.Client
}

func productionDependencies() dependencies {
	return dependencies{
		readFile:    os.ReadFile,
		newIdentity: randomRunIdentity,
		newFactory:  newWazeroFactory,
		httpClient:  capability.NewPublicHTTPClient(),
	}
}

func main() {
	os.Exit(execute(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, productionDependencies()))
}

func execute(args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("apyrun", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	artifactPath := flags.String("artifact", "", "path to the verified WASI guest artifact")
	manifestPath := flags.String("manifest", "", "path to the artifact distribution manifest")
	configPath := flags.String("config", "", "path to Host-owned operator config")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *artifactPath == "" {
		writeDiagnostic(stderr, "usage: apyrun -artifact <guest.wasm> [-manifest <manifest.json> -config <host.json>]")
		return 2
	}
	if deps.readFile == nil {
		deps.readFile = os.ReadFile
	}

	configData := []byte("{}")
	if *configPath != "" {
		var err error
		configData, err = deps.readFile(*configPath)
		if err != nil {
			writeDiagnostic(stderr, "read operator config")
			return 2
		}
	}
	operator, err := decodeOperatorConfig(configData)
	if err != nil {
		writeDiagnostic(stderr, err.Error())
		return 2
	}
	runConfig, _, err := operator.resolve()
	if err != nil {
		writeDiagnostic(stderr, err.Error())
		return 2
	}

	request, err := io.ReadAll(io.LimitReader(stdin, int64(runConfig.MaxRequestBytes)+1))
	if err != nil {
		writeDiagnostic(stderr, "read RunRequest")
		return 2
	}
	if uint64(len(request)) > uint64(runConfig.MaxRequestBytes) {
		writeDiagnostic(stderr, "RunRequest exceeds configured bounds")
		return 2
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		writeDiagnostic(stderr, "invalid RunRequest")
		return 2
	}
	if admissionErr := runtimeconfig.AdmitRunRequirements(decodedRequest); admissionErr != nil {
		return emitUnsupportedOutcome(request, admissionErr, runConfig.MaxResponseBytes, stdout, stderr)
	}
	if admissionErr := runtimeconfig.AdmitRunCompatibility(decodedRequest, runConfig.ExecutionProfile); admissionErr != nil {
		writeDiagnostic(stderr, "execution profile unsupported")
		return 2
	}
	if (runConfig.ExecutionProfile != nil) != (*manifestPath != "") {
		writeDiagnostic(stderr, "execution profile and artifact manifest must be configured together")
		return 2
	}

	wasm, err := deps.readFile(*artifactPath)
	if err != nil {
		writeDiagnostic(stderr, "read guest artifact")
		return 2
	}
	if runConfig.ExecutionProfile != nil {
		manifest, manifestErr := deps.readFile(*manifestPath)
		if manifestErr != nil {
			writeDiagnostic(stderr, "read execution profile manifest")
			return 2
		}
		inventoryFilename, required, inventoryEnvelopeErr := runtimeconfig.DistributionImportInventoryFilename(manifest)
		if inventoryEnvelopeErr != nil {
			writeDiagnostic(stderr, "inspect execution profile manifest")
			return 2
		}
		var inventory []byte
		if required {
			inventory, manifestErr = deps.readFile(filepath.Join(filepath.Dir(*manifestPath), inventoryFilename))
			if manifestErr != nil {
				writeDiagnostic(stderr, "read execution profile import inventory")
				return 2
			}
		}
		identity, verifyErr := runtimeconfig.VerifyDistributionArtifact(filepath.Base(*artifactPath), wasm, manifest, inventory)
		if verifyErr != nil {
			writeDiagnostic(stderr, "verify execution profile artifact")
			return 2
		}
		bound, bindErr := runConfig.ExecutionProfile.BindVerifiedArtifact(identity)
		if bindErr != nil {
			writeDiagnostic(stderr, "execution profile artifact mismatch")
			return 2
		}
		operator.boundExecutionProfile = &bound
	}
	if deps.newIdentity == nil {
		deps.newIdentity = randomRunIdentity
	}
	hostIdentity, err := deps.newIdentity()
	if err != nil {
		writeDiagnostic(stderr, "create Host run identity")
		return 1
	}
	if deps.newFactory == nil {
		deps.newFactory = newWazeroFactory
	}
	factory, err := deps.newFactory(operator, hostIdentity, deps.httpClient)
	if err != nil {
		writeDiagnostic(stderr, "configure execution backend")
		return 2
	}

	ctx := context.Background()
	runner, err := factory.New(ctx, wasm, runConfig)
	if err != nil {
		writeDiagnostic(stderr, "initialize execution backend")
		return 1
	}
	defer runner.Close(context.Background())
	response, err := runner.Run(ctx, request, "")
	if err != nil {
		if _, outcomeErr := runtimeconfig.NewUnsupportedOutcome(request, err); outcomeErr == nil {
			return emitUnsupportedOutcome(request, err, runConfig.MaxResponseBytes, stdout, stderr)
		}
		writeDiagnostic(stderr, "execute guest")
		return 1
	}
	if uint64(len(response)) > uint64(runConfig.MaxResponseBytes) {
		writeDiagnostic(stderr, "response exceeds configured bounds")
		return 1
	}
	if _, err := stdout.Write(append(response, '\n')); err != nil {
		writeDiagnostic(stderr, "write response")
		return 1
	}
	return 0
}

func emitUnsupportedOutcome(request []byte, runErr error, maximum uint32, stdout, stderr io.Writer) int {
	outcome, err := runtimeconfig.NewUnsupportedOutcome(request, runErr)
	if err != nil {
		writeDiagnostic(stderr, "classify unsupported outcome")
		return 1
	}
	encoded, err := json.Marshal(outcome)
	if err != nil || uint64(len(encoded)) > uint64(maximum) {
		writeDiagnostic(stderr, "encode unsupported outcome")
		return 1
	}
	if _, err := stdout.Write(append(encoded, '\n')); err != nil {
		writeDiagnostic(stderr, "write unsupported outcome")
		return 1
	}
	return exitEscalationRequired
}

func newWazeroFactory(config operatorConfig, hostIdentity string, client *http.Client) (engine.Factory, error) {
	_, grant, err := config.resolve()
	if err != nil {
		return nil, err
	}
	factory := wazeroengine.Factory{
		PreparedCapacity: config.PreparedCapacity,
		Strategy:         config.ExecutionStrategy,
		COWSnapshotShell: config.COWSnapshotShell,
	}
	if grant == nil {
		return factory, nil
	}
	if client == nil {
		client = &http.Client{}
	}
	fetcher := capability.NewHTTPFetcher(client)
	registry, toolGrant, catalogDigest, err := capability.BuildBuiltinFetchManyRegistry(*grant, fetcher)
	if err != nil {
		return nil, err
	}
	factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
		var coordinator *transaction.Coordinator
		var closeJournal func() error
		if config.TransactionJournalPath != "" {
			ledger, openErr := transaction.OpenSQLiteLedger(config.TransactionJournalPath)
			if openErr != nil {
				return nil, openErr
			}
			closeJournal = ledger.Close
			coordinator = transaction.NewCoordinator(ledger, transaction.RandomIDSource{}, time.Now, nil)
		} else {
			coordinator = transaction.NewCoordinator(transaction.NewMemoryLedger(), transaction.RandomIDSource{}, time.Now, nil)
		}
		tx, beginErr := coordinator.Begin(transaction.BeginRequest{RunID: hostIdentity, CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
		if beginErr != nil {
			if closeJournal != nil {
				_ = closeJournal()
			}
			return nil, beginErr
		}
		binder, bindErr := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
		if bindErr != nil {
			if closeJournal != nil {
				_ = closeJournal()
			}
			return nil, bindErr
		}
		broker, brokerErr := capability.NewBroker(capability.Config{
			RunIdentity: hostIdentity, Grants: map[string]capability.Grant{capability.FetchManyCapability: *grant},
			CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
			ToolGrants: map[string]capability.ToolGrant{capability.FetchManyCapability: toolGrant}, CloseJournal: closeJournal,
		}, fetcher)
		if brokerErr != nil && closeJournal != nil {
			_ = closeJournal()
		}
		return broker, brokerErr
	}
	return factory, nil
}

func randomRunIdentity() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "host-" + hex.EncodeToString(value[:]), nil
}

func writeDiagnostic(writer io.Writer, message string) {
	const maxDiagnostic = 512
	if len(message) > maxDiagnostic {
		message = message[:maxDiagnostic]
	}
	_, _ = fmt.Fprintln(writer, message)
}
