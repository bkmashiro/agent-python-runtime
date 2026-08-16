package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

const (
	protocolVersion  = "pysolate.remote-execution.v1"
	maxHTTPBodyBytes = 1 << 20
)

type executeRequest struct {
	ProtocolVersion string                   `json:"protocol_version"`
	RequestID       string                   `json:"request_id"`
	RunRequest      runtimeconfig.RunRequest `json:"run_request"`
}

type executeResponse struct {
	ProtocolVersion        string          `json:"protocol_version"`
	RequestID              string          `json:"request_id"`
	InvocationIdentity     string          `json:"invocation_identity"`
	PhysicalExecutionID    string          `json:"physical_execution_id"`
	Disposition            string          `json:"disposition"`
	Shared                 bool            `json:"shared"`
	Result                 json.RawMessage `json:"result"`
	ResultSHA256           string          `json:"result_sha256"`
	ArtifactSHA256         string          `json:"artifact_sha256"`
	ExecutionProfileSHA256 string          `json:"execution_profile_sha256"`
	DeterminismSHA256      string          `json:"determinism_sha256"`
	ImportClosureSHA256    string          `json:"import_closure_sha256"`
	EffectDisposition      string          `json:"effect_disposition"`
	ElapsedMS              float64         `json:"elapsed_ms"`
}

type errorResponse struct {
	ProtocolVersion string `json:"protocol_version"`
	RequestID       string `json:"request_id,omitempty"`
	ErrorClass      string `json:"error_class"`
	Message         string `json:"message"`
}

type executor func(context.Context, executeRequest) (executeResponse, error)

type serviceExecutor struct {
	artifact       []byte
	identity       runtimeconfig.VerifiedArtifactIdentity
	artifactSHA    string
	manifestSHA    string
	profileID      string
	available      []string
	qualified      []string
	allowed        []string
	randomSeed     string
	projectSHA     string
	policyEpochSHA string
	flights        *agentfunction.FlightGroup
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "loopback listen address")
	artifactPath := flag.String("artifact", "", "verified WASI Guest artifact")
	manifestPath := flag.String("manifest", "", "artifact manifest")
	inventoryPath := flag.String("import-inventory", "", "artifact import inventory")
	qualificationPath := flag.String("import-qualification", "", "artifact import qualification")
	allowedCSV := flag.String("allowed-imports", "sys", "comma-separated Host allowed imports")
	randomSeed := flag.String("deterministic-seed", "remote-execution-v1", "Host deterministic profile seed")
	flag.Parse()
	if *artifactPath == "" || *manifestPath == "" || *inventoryPath == "" || *qualificationPath == "" {
		log.Fatal("artifact, manifest, import-inventory, and import-qualification are required")
	}
	token := os.Getenv("PYSOLATE_HTTP_BEARER_TOKEN")
	if token == "" {
		log.Fatal("PYSOLATE_HTTP_BEARER_TOKEN is required")
	}
	exec, err := loadServiceExecutor(*artifactPath, *manifestPath, *inventoryPath, *qualificationPath, strings.Split(*allowedCSV, ","), *randomSeed)
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{Addr: *listen, Handler: newHTTPServer(token, exec.execute), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 6 * time.Minute, IdleTimeout: 30 * time.Second}
	log.Printf("pysolate-httpd ready on %s artifact=%s", *listen, exec.artifactSHA)
	log.Fatal(server.ListenAndServe())
}

func loadServiceExecutor(artifactPath, manifestPath, inventoryPath, qualificationPath string, allowed []string, randomSeed string) (*serviceExecutor, error) {
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("read artifact: %w", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	inventoryBytes, err := os.ReadFile(inventoryPath)
	if err != nil {
		return nil, fmt.Errorf("read import inventory: %w", err)
	}
	qualificationBytes, err := os.ReadFile(qualificationPath)
	if err != nil {
		return nil, fmt.Errorf("read import qualification: %w", err)
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact(filepath.Base(artifactPath), artifact, manifestBytes, inventoryBytes, qualificationBytes)
	if err != nil {
		return nil, fmt.Errorf("verify artifact evidence: %w", err)
	}
	artifactSHA := identity.ArtifactSHA256
	allowed = normalizeImports(allowed)
	if len(allowed) == 0 {
		return nil, errors.New("allowed imports are required")
	}
	qualifiedSet := make(map[string]bool, len(identity.QualifiedImportRoots))
	for _, name := range identity.QualifiedImportRoots {
		qualifiedSet[name] = true
	}
	for _, name := range allowed {
		if !qualifiedSet[name] {
			return nil, fmt.Errorf("allowed import %q is not qualified", name)
		}
	}
	projectDigest := sha256.Sum256([]byte("pysolate.remote-execution.project.v1\x00" + artifactSHA))
	policyDigest := sha256.Sum256([]byte("pysolate.remote-execution.policy.v1\x00" + strings.Join(allowed, "\x00")))
	return &serviceExecutor{
		artifact: artifact, identity: identity, artifactSHA: artifactSHA, manifestSHA: identity.ManifestSHA256,
		profileID: identity.ProfileID, available: append([]string(nil), identity.ImportRoots...), qualified: append([]string(nil), identity.QualifiedImportRoots...), allowed: allowed,
		randomSeed: randomSeed, projectSHA: "sha256:" + hex.EncodeToString(projectDigest[:]), policyEpochSHA: "sha256:" + hex.EncodeToString(policyDigest[:]), flights: agentfunction.NewFlightGroup(),
	}, nil
}

func normalizeImports(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (service *serviceExecutor) execute(ctx context.Context, envelope executeRequest) (executeResponse, error) {
	started := time.Now()
	requestBytes, err := runtimeconfig.EncodeRunRequest(envelope.RunRequest)
	if err != nil {
		return executeResponse{}, err
	}
	decoded, err := runtimeconfig.DecodeRunRequest(requestBytes)
	if err != nil {
		return executeResponse{}, err
	}
	var canonicalInputs any
	if err := json.Unmarshal(decoded.Inputs, &canonicalInputs); err != nil {
		return executeResponse{}, err
	}
	decoded.Inputs, err = json.Marshal(canonicalInputs)
	if err != nil {
		return executeResponse{}, err
	}
	if err := runtimeconfig.AdmitRunRequirements(decoded); err != nil {
		return executeResponse{}, err
	}
	if decoded.Compatibility != nil {
		if decoded.Compatibility.Profile != service.profileID {
			return executeResponse{}, runtimeconfig.ErrExecutionProfileUnsupported
		}
		for _, requested := range decoded.Compatibility.Imports {
			if !contains(service.allowed, requested) {
				return executeResponse{}, runtimeconfig.ErrExecutionProfileUnsupported
			}
		}
	}
	profile, err := runtimeconfig.NewExecutionProfile(service.profileID, service.allowed)
	if err != nil {
		return executeResponse{}, err
	}
	profile, err = profile.BindVerifiedArtifact(service.identity)
	if err != nil {
		return executeResponse{}, err
	}
	decoded, err = runtimeconfig.BindAgentSource(decoded, &profile)
	if err != nil {
		return executeResponse{}, err
	}
	requestBytes, err = runtimeconfig.EncodeRunRequest(decoded)
	if err != nil {
		return executeResponse{}, err
	}
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(service.artifactSHA, service.randomSeed)
	if err != nil {
		return executeResponse{}, err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.Timeout = 90 * time.Second
	runConfig.ExecutionProfile = &profile
	runConfig.DeterministicVerification = &deterministic
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(runConfig)
	if err != nil {
		return executeResponse{}, err
	}
	codeDigest := sha256.Sum256([]byte(decoded.Code))
	outputDigest := sha256.Sum256(decoded.OutputSchema)
	invocation := agentfunction.Invocation{
		SchemaVersion: agentfunction.InvocationSchemaVersion, Admission: agentfunction.Cacheable,
		ProjectSHA256: service.projectSHA, FunctionSourceSHA256: "sha256:" + hex.EncodeToString(codeDigest[:]), ArtifactSHA256: service.artifactSHA,
		ExecutionProfileSHA256: profileSHA, ImportClosureSHA256: agentfunction.ImportClosureIdentity(service.available, service.qualified), CanonicalInputs: append(json.RawMessage(nil), decoded.Inputs...),
		ImmutableRootSHA256: []string{service.artifactSHA, service.manifestSHA}, DeterministicSettingsSHA256: deterministic.Identity(), OutputSchemaSHA256: "sha256:" + hex.EncodeToString(outputDigest[:]),
		PrivacyPartition: "remote-execution-v1", PolicyEpochSHA256: service.policyEpochSHA,
	}
	sort.Strings(invocation.ImmutableRootSHA256)
	identity, _, err := invocation.Identity()
	if err != nil {
		return executeResponse{}, err
	}
	compute := agentfunction.FreshGuestCompute{
		NewRunner: func(runContext context.Context) (string, enginecontract.Runner, error) {
			physicalID, err := randomID("physical-")
			if err != nil {
				return "", nil, err
			}
			runner, err := (wazeroengine.Factory{}).New(runContext, service.artifact, runConfig)
			return physicalID, runner, err
		},
		Request: requestBytes, MaxResultBytes: uint64(runConfig.MaxResponseBytes), DecodeResult: decodeSuccessfulGuestResult,
	}
	result, err := (agentfunction.Engine{Flights: service.flights}).ExecuteGuest(ctx, invocation, compute)
	if err != nil {
		return executeResponse{}, err
	}
	resultDigest := sha256.Sum256(result.Value)
	return executeResponse{
		ProtocolVersion: protocolVersion, RequestID: envelope.RequestID, InvocationIdentity: identity, PhysicalExecutionID: result.PhysicalExecutionID,
		Disposition: string(result.Disposition), Shared: result.Shared, Result: append(json.RawMessage(nil), result.Value...), ResultSHA256: "sha256:" + hex.EncodeToString(resultDigest[:]),
		ArtifactSHA256: service.artifactSHA, ExecutionProfileSHA256: profileSHA, DeterminismSHA256: deterministic.Identity(), ImportClosureSHA256: invocation.ImportClosureSHA256,
		EffectDisposition: "effect_free", ElapsedMS: float64(time.Since(started)) / float64(time.Millisecond),
	}, nil
}

func contains(values []string, wanted string) bool {
	index := sort.SearchStrings(values, wanted)
	return index < len(values) && values[index] == wanted
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}

func decodeSuccessfulGuestResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, err
	}
	if response.Status != "ok" || len(response.Result) == 0 {
		return nil, errors.New("Guest result is not publishable")
	}
	return append([]byte(nil), response.Result...), nil
}

func newHTTPServer(token string, run executor) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ready", "protocol_version": protocolVersion, "execution_instance": "fresh_per_physical_execution", "prepared_runtime": "disabled", "memory_cow": "disabled"})
	})
	mux.HandleFunc("POST /v1/executions", func(writer http.ResponseWriter, request *http.Request) {
		if !validBearer(request.Header.Get("Authorization"), token) {
			writeJSON(writer, http.StatusUnauthorized, errorResponse{ProtocolVersion: protocolVersion, ErrorClass: "unauthorized", Message: "authentication required"})
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxHTTPBodyBytes))
		if err != nil {
			writeJSON(writer, http.StatusRequestEntityTooLarge, errorResponse{ProtocolVersion: protocolVersion, ErrorClass: "request_too_large", Message: "request exceeds bound"})
			return
		}
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		var envelope executeRequest
		if decoder.Decode(&envelope) != nil || decoder.Decode(&struct{}{}) != io.EOF || envelope.ProtocolVersion != protocolVersion || envelope.RequestID == "" || envelope.RunRequest.RunID == "" {
			writeJSON(writer, http.StatusBadRequest, errorResponse{ProtocolVersion: protocolVersion, RequestID: envelope.RequestID, ErrorClass: "invalid_request", Message: "invalid execution request"})
			return
		}
		if run == nil {
			writeJSON(writer, http.StatusServiceUnavailable, errorResponse{ProtocolVersion: protocolVersion, RequestID: envelope.RequestID, ErrorClass: "unavailable", Message: "executor unavailable"})
			return
		}
		response, err := run(request.Context(), envelope)
		if err != nil {
			writeJSON(writer, http.StatusUnprocessableEntity, errorResponse{ProtocolVersion: protocolVersion, RequestID: envelope.RequestID, ErrorClass: classifyError(err), Message: "execution rejected"})
			return
		}
		writeJSON(writer, http.StatusOK, response)
	})
	return mux
}

func validBearer(header, token string) bool {
	provided, ok := strings.CutPrefix(header, "Bearer ")
	return ok && token != "" && len(provided) == len(token) && subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1
}

func classifyError(err error) string {
	switch {
	case errors.Is(err, agentfunction.ErrGuestNotShareable):
		return "not_shareable"
	case errors.Is(err, agentfunction.ErrGuestIdentity):
		return "identity_mismatch"
	case errors.Is(err, runtimeconfig.ErrExecutionProfileUnsupported):
		return "profile_unsupported"
	default:
		return "execution_rejected"
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
