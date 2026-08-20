package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
)

const maxEagerComparatorContractBytes = 16 << 10

func SealEagerComparatorContract(value EagerComparatorContract) (EagerComparatorContract, error) {
	value.Identity = ""
	if !validEagerComparatorContract(value, false) {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	digest := sha256.Sum256(append([]byte(EagerComparatorSchemaVersion+"\x00"), raw...))
	value.Identity = "sha256:" + hex.EncodeToString(digest[:])
	if value.Identity != EagerStyleGateV1Identity {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	return value, nil
}

func EncodeEagerComparatorContract(value EagerComparatorContract) ([]byte, error) {
	if !validEagerComparatorContract(value, true) {
		return nil, ErrInvalidPreregistration
	}
	return json.Marshal(value)
}

func DecodeEagerComparatorContract(raw []byte) (EagerComparatorContract, error) {
	if len(raw) == 0 || len(raw) > maxEagerComparatorContractBytes || rejectDuplicateKeys(raw) != nil {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value EagerComparatorContract
	if decoder.Decode(&value) != nil {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || !validEagerComparatorContract(value, true) {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(raw, canonical) {
		return EagerComparatorContract{}, ErrInvalidPreregistration
	}
	return value, nil
}

func validEagerComparatorContract(value EagerComparatorContract, sealed bool) bool {
	if value.SchemaVersion != EagerComparatorSchemaVersion || value.Treatment != "eager_style_gate" || value.TargetPython != "cpython-3.14.0-wasi" ||
		value.Source.Title != "Executing as You Generate: Hiding Execution Latency in LLM Code Generation" || value.Source.ArxivID != "2604.00491" ||
		value.Source.PDFURL != "https://arxiv.org/pdf/2604.00491" || value.Source.PDFSHA256 != "sha256:23af671ca94b7cbbc0866a37391520ae39e75c964320e7809b1612dfb3e023cb" ||
		value.Chunking.Parser != "target_cpython_ast" || value.Chunking.Boundary != "complete_top_level_statement" || value.Chunking.LookaheadTokens != 1 || !value.Chunking.FinalSuffixMustParse ||
		!value.Execution.PersistentInterpreter || !value.Execution.DynamicBatching || value.Execution.EarlyInterrupt || value.Execution.ErrorReport != "after_frozen_source_schedule" ||
		value.Gate.NameMatch != "ast_name_or_attribute_root" || value.Gate.DeniedAction != "seal_remaining_suffix" ||
		value.Gate.InvalidFinalSuffix != "syntax_error_after_source_seal" || value.Gate.PreviouslyRunPrefixes != "not_replayed" ||
		!slices.Equal(value.Gate.LowYieldNodes, []string{"AsyncFunctionDef", "ClassDef", "FunctionDef"}) ||
		!slices.Equal(value.Gate.DeniedModules, []string{"multiprocessing", "os", "shutil", "signal", "socket", "subprocess", "threading", "time"}) ||
		!slices.Equal(value.Gate.DynamicNames, []string{"__import__", "compile", "eval", "exec"}) {
		return false
	}
	copyValue := value
	copyValue.Identity = ""
	raw, err := json.Marshal(copyValue)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(append([]byte(EagerComparatorSchemaVersion+"\x00"), raw...))
	expected := "sha256:" + hex.EncodeToString(digest[:])
	return expected == EagerStyleGateV1Identity && sealed == (value.Identity == expected)
}
