package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"unicode/utf8"

	capabilityreceipt "github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type RunResponseStatus string

type WorkspaceDispositionPolicy string

const (
	RunResponseOK    RunResponseStatus = "ok"
	RunResponseError RunResponseStatus = "error"

	WorkspaceExportOnSuccess  WorkspaceDispositionPolicy = "export_on_success"
	WorkspaceExportOnResponse WorkspaceDispositionPolicy = "export_on_response"
	WorkspaceDiscardPolicy    WorkspaceDispositionPolicy = "discard"

	WorkspaceExported  WorkspaceDisposition = "exported"
	WorkspaceDiscarded WorkspaceDisposition = "discarded"
)

var ErrRunResultSchemaMismatch = errors.New("run result does not match output_schema")

type RunMetrics struct {
	GuestTimeMS     *float64 `json:"guest_time_ms,omitempty"`
	CapabilityCalls uint32   `json:"capability_calls"`
	ResultBytes     uint32   `json:"result_bytes"`
}

type RunError struct {
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	ErrorType *string `json:"error_type,omitempty"`
	Traceback *string `json:"traceback,omitempty"`
}

const WorkspaceReceiptSchemaVersion = "pysolate.workspace-disposition.v1"

type WorkspaceReceipt struct {
	SchemaVersion          string                     `json:"schema_version"`
	RequestSHA256          string                     `json:"request_sha256"`
	Policy                 WorkspaceDispositionPolicy `json:"policy"`
	Disposition            WorkspaceDisposition       `json:"disposition"`
	InitialWorkspaceSHA256 string                     `json:"initial_workspace_sha256"`
	FinalWorkspaceSHA256   string                     `json:"final_workspace_sha256"`
	FinalTreeSHA256        string                     `json:"final_tree_sha256"`
	EntryCount             uint32                     `json:"entry_count"`
	TotalBytes             uint64                     `json:"total_bytes"`
	CapsuleSHA256          *string                    `json:"capsule_sha256,omitempty"`
}

func (receipt WorkspaceReceipt) Validate() error {
	if receipt.SchemaVersion != WorkspaceReceiptSchemaVersion || !receipt.Policy.Valid() || !validPrefixedSHA256(receipt.RequestSHA256) ||
		!validPrefixedSHA256(receipt.InitialWorkspaceSHA256) || !validPrefixedSHA256(receipt.FinalWorkspaceSHA256) || !validPrefixedSHA256(receipt.FinalTreeSHA256) {
		return errors.New("workspace receipt has invalid identity fields")
	}
	switch receipt.Disposition {
	case WorkspaceExported:
		if receipt.Policy == WorkspaceDiscardPolicy || receipt.CapsuleSHA256 == nil || !validPrefixedSHA256(*receipt.CapsuleSHA256) {
			return errors.New("exported workspace receipt has invalid capsule identity")
		}
	case WorkspaceDiscarded:
		if receipt.CapsuleSHA256 != nil {
			return errors.New("discarded workspace receipt contains a capsule identity")
		}
	default:
		return errors.New("workspace receipt has invalid disposition")
	}
	return nil
}

func (receipt WorkspaceReceipt) ValidateForStatus(status RunResponseStatus) error {
	if err := receipt.Validate(); err != nil {
		return err
	}
	expected := WorkspaceDiscarded
	if receipt.Policy == WorkspaceExportOnResponse || (receipt.Policy == WorkspaceExportOnSuccess && status == RunResponseOK) {
		expected = WorkspaceExported
	}
	if (status != RunResponseOK && status != RunResponseError) || receipt.Disposition != expected {
		return errors.New("workspace receipt disposition does not match response status and policy")
	}
	return nil
}

func (policy WorkspaceDispositionPolicy) Valid() bool {
	return policy == WorkspaceExportOnSuccess || policy == WorkspaceExportOnResponse || policy == WorkspaceDiscardPolicy
}

func validPrefixedSHA256(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if character < '0' || (character > '9' && character < 'a') || character > 'f' {
			return false
		}
	}
	return true
}

type RunSourceContract struct {
	SchemaVersion         string `json:"schema_version"`
	Authority             string `json:"authority"`
	ModelSourceSHA256     string `json:"model_source_sha256"`
	EffectiveASTSHA256    string `json:"effective_ast_sha256"`
	WrapperContractSHA256 string `json:"wrapper_contract_sha256"`
}

func (contract RunSourceContract) Validate() error {
	if contract.SchemaVersion != "pysolate.guest-source-contract.v1" || contract.Authority != "guest_reported_execution_fact" || !validPrefixedSHA256(contract.ModelSourceSHA256) || !validPrefixedSHA256(contract.EffectiveASTSHA256) || !validPrefixedSHA256(contract.WrapperContractSHA256) {
		return errors.New("run source contract is invalid")
	}
	return nil
}

func validateModelOutputContract(request RunRequest, response RunResponse) error {
	if len(response.Logs) > 256 {
		return errors.New("run response contains too many model logs")
	}
	logBytes := 0
	for _, line := range response.Logs {
		if !utf8.ValidString(line) {
			return errors.New("run response contains invalid model logs")
		}
		logBytes += len([]byte(line))
	}
	if logBytes > 64*1024 {
		return errors.New("run response model logs exceed the byte bound")
	}
	if response.SourceContract != nil {
		if response.SourceContract.Validate() != nil {
			return errors.New("run response source contract is invalid")
		}
		digest := sha256.Sum256([]byte(request.Code))
		expected := "sha256:" + hex.EncodeToString(digest[:])
		if response.SourceContract.ModelSourceSHA256 != expected {
			return errors.New("run response model source identity mismatch")
		}
	}
	if response.ResultPresent == nil && response.ResultSource == "" && response.SourceContract == nil {
		return nil
	}
	if response.ResultPresent == nil || response.SourceContract == nil {
		return errors.New("run response model output contract is incomplete")
	}
	switch response.ResultSource {
	case "return", "legacy_result":
		if !*response.ResultPresent {
			return errors.New("run response result source contradicts presence")
		}
	case "missing":
		if *response.ResultPresent || string(bytes.TrimSpace(response.Result)) != "null" {
			return errors.New("missing run result has a value")
		}
	default:
		return errors.New("run response has an invalid result source")
	}
	return nil
}

type RunResponse struct {
	Status               RunResponseStatus  `json:"status"`
	Result               json.RawMessage    `json:"result"`
	Logs                 []string           `json:"logs,omitempty"`
	ResultPresent        *bool              `json:"result_present,omitempty"`
	ResultSource         string             `json:"result_source,omitempty"`
	SourceContract       *RunSourceContract `json:"source_contract,omitempty"`
	Receipts             json.RawMessage    `json:"receipts"`
	Metrics              *RunMetrics        `json:"metrics"`
	Error                *RunError          `json:"error"`
	CapabilityPlanSHA256 *string            `json:"capability_plan_sha256,omitempty"`
	ExecutionRef         *ExecutionRef      `json:"execution_ref,omitempty"`
	WorkspaceReceipt     *WorkspaceReceipt  `json:"workspace_receipt,omitempty"`
}

func DecodeAndValidateGuestRunResponse(request RunRequest, data []byte) (RunResponse, error) {
	if err := rejectDuplicateBoundedJSON(data); err != nil {
		return RunResponse{}, errors.New("Guest run response contains duplicate JSON keys")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(data, &envelope) != nil || !hasRequiredAndOnlyExactKeys(envelope,
		[]string{"status", "result", "receipts", "metrics", "error"}, "logs", "result_present", "result_source", "source_contract") {
		return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
	}
	var metrics map[string]json.RawMessage
	if json.Unmarshal(envelope["metrics"], &metrics) != nil || !hasRequiredAndOnlyExactKeys(metrics,
		[]string{"capability_calls", "result_bytes"}, "guest_time_ms") {
		return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
	}
	if rawError := bytes.TrimSpace(envelope["error"]); !bytes.Equal(rawError, []byte("null")) {
		var guestError map[string]json.RawMessage
		if json.Unmarshal(rawError, &guestError) != nil || !hasRequiredAndOnlyExactKeys(guestError,
			[]string{"code", "message"}, "error_type", "traceback") {
			return RunResponse{}, errors.New("Guest run response has non-canonical envelope keys")
		}
	}
	return decodeAndValidateRunResponse(request, data, false)
}

func rejectDuplicateBoundedJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueBoundedJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("run response contains trailing JSON")
	}
	return nil
}

const maxCanonicalJSONNodes = 16384

func consumeUniqueBoundedJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= maxCanonicalJSONNodes {
		return errors.New("run response JSON is too complex")
	}
	*nodes++
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] {
				return errors.New("run response contains duplicate JSON keys")
			}
			seen[key] = true
			if err := consumeUniqueBoundedJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("run response contains invalid JSON")
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueBoundedJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("run response contains invalid JSON")
		}
	default:
		return errors.New("run response contains invalid JSON")
	}
	return nil
}

func hasExactKeys(values map[string]json.RawMessage, required ...string) bool {
	if len(values) != len(required) {
		return false
	}
	for _, key := range required {
		if _, exists := values[key]; !exists {
			return false
		}
	}
	return true
}

func hasRequiredAndOnlyExactKeys(values map[string]json.RawMessage, required []string, optional ...string) bool {
	allow := make(map[string]struct{}, len(required)+len(optional))
	for _, key := range required {
		allow[key] = struct{}{}
		if _, exists := values[key]; !exists {
			return false
		}
	}
	for _, key := range optional {
		allow[key] = struct{}{}
	}
	for key := range values {
		if _, exists := allow[key]; !exists {
			return false
		}
	}
	return true
}

func rejectExplicitNullHostEvidence(data []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return errors.New("run response JSON is invalid")
	}
	for _, key := range []string{"capability_plan_sha256", "execution_ref", "workspace_receipt"} {
		if raw, present := envelope[key]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("run response optional Host evidence cannot be null")
		}
	}
	rawReceipt, present := envelope["workspace_receipt"]
	if !present {
		return nil
	}
	var receipt map[string]json.RawMessage
	if err := json.Unmarshal(rawReceipt, &receipt); err != nil {
		return nil
	}
	required := []string{
		"schema_version", "request_sha256", "policy", "disposition",
		"initial_workspace_sha256", "final_workspace_sha256", "final_tree_sha256",
		"entry_count", "total_bytes",
	}
	for _, key := range required {
		raw, present := receipt[key]
		if !present {
			return errors.New("workspace receipt is missing required fields")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return errors.New("workspace receipt fields cannot be null")
		}
	}
	if raw, present := receipt["capsule_sha256"]; present && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("workspace receipt fields cannot be null")
	}
	return nil
}

func DecodeAndValidateRunResponse(request RunRequest, data []byte) (RunResponse, error) {
	return decodeAndValidateRunResponse(request, data, true)
}

func decodeAndValidateRunResponse(request RunRequest, data []byte, requireHostEvidence bool) (RunResponse, error) {
	if err := rejectDuplicateBoundedJSON(data); err != nil {
		return RunResponse{}, errors.New("run response JSON is invalid or contains duplicate keys")
	}
	if err := rejectExplicitNullHostEvidence(data); err != nil {
		return RunResponse{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response RunResponse
	if err := decoder.Decode(&response); err != nil {
		return RunResponse{}, fmt.Errorf("decode run response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RunResponse{}, errors.New("run response contains trailing JSON")
	}
	hostEvidenceInvalid := !requireHostEvidence && (response.CapabilityPlanSHA256 != nil || response.ExecutionRef != nil || response.WorkspaceReceipt != nil)
	capabilityPlanInvalid := response.CapabilityPlanSHA256 != nil && !validPrefixedSHA256(*response.CapabilityPlanSHA256)
	workspaceReceiptInvalid := false
	if response.WorkspaceReceipt != nil {
		expectedRequestSHA, err := RunRequestSHA256(request)
		workspaceReceiptInvalid = err != nil || response.WorkspaceReceipt.RequestSHA256 != expectedRequestSHA ||
			response.WorkspaceReceipt.ValidateForStatus(response.Status) != nil
	}
	if (response.Status != RunResponseOK && response.Status != RunResponseError) || len(response.Result) == 0 || len(response.Receipts) == 0 || response.Metrics == nil ||
		(response.Metrics.GuestTimeMS != nil && *response.Metrics.GuestTimeMS < 0) || (response.ExecutionRef != nil && response.ExecutionRef.Validate() != nil) ||
		capabilityPlanInvalid || workspaceReceiptInvalid || hostEvidenceInvalid || validateModelOutputContract(request, response) != nil {
		return RunResponse{}, errors.New("run response has invalid required fields")
	}
	if err := validateCapabilityReceipts(response.Receipts, response.CapabilityPlanSHA256); err != nil {
		return RunResponse{}, err
	}
	if response.Status == RunResponseOK {
		if response.Error != nil {
			return RunResponse{}, errors.New("successful run response contains an error")
		}
		if len(request.OutputSchema) != 0 && string(request.OutputSchema) != "null" {
			if err := validateRunOutput(request.OutputSchema, response.Result); err != nil {
				return response, err
			}
		}
	} else {
		if response.Error == nil || response.Error.Code == "" || len(response.Error.Code) > 128 || response.Error.Message == "" || len(response.Error.Message) > 4096 || string(response.Result) != "null" {
			return RunResponse{}, errors.New("failed run response has invalid error fields")
		}
	}
	return response, nil
}

func validateCapabilityReceipts(raw json.RawMessage, capabilityPlanSHA256 *string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("run response receipts are not an array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return errors.New("run response receipts are not an array")
	}
	if len(items) > 256 {
		return errors.New("run response contains too many receipts")
	}
	if len(items) != 0 && capabilityPlanSHA256 == nil {
		return errors.New("run response receipts are missing capability plan evidence")
	}
	allowed := map[string]struct{}{
		"receipt_id": {}, "run_id": {}, "capability_plan_sha256": {}, "capability": {},
		"call_id": {}, "parent_call_id": {}, "approval_request_id": {},
		"operation_index": {}, "request_sha256": {}, "response_sha256": {}, "outcome": {}, "source": {},
	}
	required := []string{"receipt_id", "run_id", "capability_plan_sha256", "capability", "operation_index", "request_sha256", "outcome"}
	programmaticSequences := make(map[string]uint64)
	for _, item := range items {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
			return errors.New("run response receipt is not an object")
		}
		for name, value := range fields {
			if _, ok := allowed[name]; !ok {
				return errors.New("run response receipt contains an unknown field")
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return errors.New("run response receipt contains an explicit null field")
			}
		}
		for _, name := range required {
			if _, ok := fields[name]; !ok {
				return errors.New("run response receipt is missing a required field")
			}
		}
		var callReceipt capabilityReceiptDocument
		decoder := json.NewDecoder(bytes.NewReader(item))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		if err := decoder.Decode(&callReceipt); err != nil {
			return errors.New("run response receipt has invalid field types")
		}
		expectedProgrammaticSequence := programmaticSequences[callReceipt.ParentCallID] + 1
		if !boundedString(callReceipt.ReceiptID, 1, 160) || !boundedString(callReceipt.RunID, 1, 128) ||
			!boundedString(callReceipt.Capability, 1, 128) || !validPrefixedSHA256(callReceipt.CapabilityPlanSHA256) ||
			(present(fields, "call_id") && !validReceiptCallIdentity(callReceipt.CallID, 128)) ||
			(present(fields, "parent_call_id") && !validReceiptCallIdentity(callReceipt.ParentCallID, 96)) ||
			(present(fields, "approval_request_id") && !validApprovalRequestID(callReceipt.ApprovalRequestID)) ||
			(present(fields, "request_sha256") && !validBareSHA256(callReceipt.RequestSHA256)) ||
			(present(fields, "response_sha256") && !validBareSHA256(callReceipt.ResponseSHA256)) ||
			!validProgrammaticReceiptRelation(fields, callReceipt, expectedProgrammaticSequence) ||
			!validSourceReceiptRelation(fields, callReceipt) ||
			!validOperationIndex(callReceipt.OperationIndex) || !validReceiptOutcome(callReceipt.Outcome) {
			return errors.New("run response receipt has invalid field values")
		}
		if capabilityPlanSHA256 == nil || callReceipt.CapabilityPlanSHA256 != *capabilityPlanSHA256 {
			return errors.New("run response receipt capability plan does not match Host evidence")
		}
		operationIndex, _ := receiptOperationIndex(callReceipt.OperationIndex)
		if !capabilityreceipt.ValidIdentity(capabilityreceipt.Receipt{
			ReceiptID: callReceipt.ReceiptID, RunID: callReceipt.RunID, CapabilityPlanSHA256: callReceipt.CapabilityPlanSHA256,
			CallID: callReceipt.CallID, ParentCallID: callReceipt.ParentCallID, ApprovalRequestID: callReceipt.ApprovalRequestID,
			Capability: callReceipt.Capability, OperationIndex: operationIndex, RequestSHA256: callReceipt.RequestSHA256,
			ResponseSHA256: callReceipt.ResponseSHA256, Outcome: callReceipt.Outcome, Source: callReceipt.Source,
		}) {
			return errors.New("run response receipt identity does not match its bound operation")
		}
		if present(fields, "parent_call_id") {
			programmaticSequences[callReceipt.ParentCallID] = expectedProgrammaticSequence
		}
	}
	return nil
}

type capabilityReceiptDocument struct {
	ReceiptID            string                           `json:"receipt_id"`
	RunID                string                           `json:"run_id"`
	CapabilityPlanSHA256 string                           `json:"capability_plan_sha256"`
	CallID               string                           `json:"call_id,omitempty"`
	ParentCallID         string                           `json:"parent_call_id,omitempty"`
	ApprovalRequestID    string                           `json:"approval_request_id,omitempty"`
	Capability           string                           `json:"capability"`
	OperationIndex       json.Number                      `json:"operation_index"`
	RequestSHA256        string                           `json:"request_sha256,omitempty"`
	ResponseSHA256       string                           `json:"response_sha256,omitempty"`
	Outcome              string                           `json:"outcome"`
	Source               *capabilityreceipt.SourceBinding `json:"source,omitempty"`
}

func present(fields map[string]json.RawMessage, name string) bool {
	_, ok := fields[name]
	return ok
}

func boundedString(value string, minimum, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func validProgrammaticReceiptRelation(fields map[string]json.RawMessage, receipt capabilityReceiptDocument, expectedSequence uint64) bool {
	_, hasParent := fields["parent_call_id"]
	_, hasCall := fields["call_id"]
	if !hasParent {
		return !hasCall || !strings.Contains(receipt.CallID, ":program:")
	}
	if !hasCall {
		return false
	}
	return receipt.CallID == fmt.Sprintf("%s:program:%d", receipt.ParentCallID, expectedSequence)
}

func validSourceReceiptRelation(fields map[string]json.RawMessage, document capabilityReceiptDocument) bool {
	_, hasSource := fields["source"]
	if !hasSource {
		return document.Source == nil
	}
	return document.Source != nil && present(fields, "parent_call_id") && present(fields, "call_id") &&
		document.Source.Capability == document.Capability && capabilityreceipt.ValidSourceBinding(*document.Source)
}

func validReceiptCallIdentity(value string, limit int) bool {
	if !boundedString(value, 1, limit) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

func validApprovalRequestID(value string) bool {
	return len(value) == len("apr_")+64 && value[:len("apr_")] == "apr_" && validBareSHA256(value[len("apr_"):])
}

func validBareSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validOperationIndex(value json.Number) bool {
	_, ok := receiptOperationIndex(value)
	return ok
}

func receiptOperationIndex(value json.Number) (uint32, bool) {
	rational, ok := new(big.Rat).SetString(value.String())
	if !ok || rational.Sign() < 0 || !rational.IsInt() {
		return 0, false
	}
	maximum := new(big.Int).SetUint64(uint64(^uint32(0)))
	if rational.Num().Cmp(maximum) > 0 {
		return 0, false
	}
	return uint32(rational.Num().Uint64()), true
}

func validReceiptOutcome(outcome string) bool {
	return outcome == "ok" || outcome == "denied" || outcome == "error" || outcome == "timeout" || outcome == "ambiguous"
}

type denySchemaURLLoader struct{}

func (denySchemaURLLoader) Load(string) (any, error) {
	return nil, errors.New("external schema resources are disabled")
}

func validateRunOutput(schemaBytes, resultBytes []byte) error {
	var schemaDocument any
	if json.Unmarshal(schemaBytes, &schemaDocument) != nil {
		return errors.New("output_schema is not valid JSON")
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	compiler.UseLoader(denySchemaURLLoader{})
	if err := compiler.AddResource("mem:///run-output.schema.json", schemaDocument); err != nil {
		return errors.New("output_schema is invalid")
	}
	compiled, err := compiler.Compile("mem:///run-output.schema.json")
	if err != nil {
		return errors.New("output_schema is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(resultBytes))
	decoder.UseNumber()
	var result any
	if decoder.Decode(&result) != nil {
		return errors.New("run result is invalid JSON")
	}
	if err := compiled.Validate(result); err != nil {
		return ErrRunResultSchemaMismatch
	}
	return nil
}
