package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

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
	configPath := flags.String("config", "", "path to Host-owned operator config")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *artifactPath == "" {
		writeDiagnostic(stderr, "usage: apyrun -artifact <guest.wasm> [-config <host.json>]")
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
	if _, err := runtimeconfig.DecodeRunRequest(request); err != nil {
		writeDiagnostic(stderr, "invalid RunRequest")
		return 2
	}

	wasm, err := deps.readFile(*artifactPath)
	if err != nil {
		writeDiagnostic(stderr, "read guest artifact")
		return 2
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

func newWazeroFactory(config operatorConfig, hostIdentity string, client *http.Client) (engine.Factory, error) {
	_, grant, err := config.resolve()
	if err != nil {
		return nil, err
	}
	factory := wazeroengine.Factory{PreparedCapacity: config.PreparedCapacity}
	if grant == nil {
		return factory, nil
	}
	if client == nil {
		client = &http.Client{}
	}
	fetcher := capability.NewHTTPFetcher(client)
	factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
		return capability.NewBroker(capability.Config{
			RunIdentity: hostIdentity,
			Grants: map[string]capability.Grant{
				capability.FetchManyCapability: *grant,
			},
		}, fetcher)
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
