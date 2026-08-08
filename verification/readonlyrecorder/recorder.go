// Package readonlyrecorder captures bounded Host-provided JSON observations
// and infers untrusted, read-only shape candidates. It does not fetch URLs,
// execute commands, resolve credentials, expose tools, or commit effects.
package readonlyrecorder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	RecordingVersion = "readonly-recording/v1"
	ContractVersion  = "readonly-contract/v1"
	maxPayloadBytes  = 64 << 10
	maxNodes         = 2048
	maxDepth         = 32
)

type SourceKind string

const (
	SourceWeb SourceKind = "web"
	SourceCLI SourceKind = "cli"
)

type Trust string

const TrustUntrusted Trust = "untrusted"

var (
	ErrInvalidObservation   = errors.New("invalid read-only observation")
	ErrSensitiveObservation = errors.New("sensitive read-only observation rejected")
	ErrRecordingIntegrity   = errors.New("read-only recording integrity failure")
	ErrInvalidCandidate     = errors.New("invalid inferred contract candidate")
	ErrAuthorityEscalation  = errors.New("inferred contract cannot increase authority")
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var valueSecretPattern = regexp.MustCompile(`(?i)(?:bearer\s+[A-Za-z0-9._~+/=-]{8,}|-----BEGIN [A-Z ]*PRIVATE KEY-----|sk-[A-Za-z0-9]{12,}|AKIA[0-9A-Z]{16})`)
var sensitiveKeys = map[string]struct{}{
	"auth": {},
}
var sensitiveKeyFragments = []string{
	"apikey", "authorization", "cookie", "credential", "password", "privatekey", "secret", "token",
}

type Observation struct {
	Source  SourceKind
	Locator string
	Payload []byte
}

type Recording struct {
	Version              string     `json:"version"`
	Source               SourceKind `json:"source"`
	SourceIdentityDigest string     `json:"source_identity_digest"`
	CanonicalPayload     []byte     `json:"canonical_payload"`
	PayloadDigest        string     `json:"payload_digest"`
	Digest               string     `json:"digest"`
}

type AuthorityCeiling struct {
	ReadOnly     bool `json:"read_only"`
	Credentials  bool `json:"credentials"`
	ToolExposure bool `json:"tool_exposure"`
	Commit       bool `json:"commit"`
}

type Field struct {
	Path  string   `json:"path"`
	Types []string `json:"types"`
}

type ContractCandidate struct {
	Version              string           `json:"version"`
	Source               SourceKind       `json:"source"`
	SourceIdentityDigest string           `json:"source_identity_digest"`
	SampleDigest         string           `json:"sample_digest"`
	Fields               []Field          `json:"fields"`
	Trust                Trust            `json:"trust"`
	Authority            AuthorityCeiling `json:"authority"`
}

type TypeChange struct {
	Path   string   `json:"path"`
	Before []string `json:"before"`
	After  []string `json:"after"`
}

type Drift struct {
	Changed     bool             `json:"changed"`
	Added       []string         `json:"added,omitempty"`
	Removed     []string         `json:"removed,omitempty"`
	TypeChanged []TypeChange     `json:"type_changed,omitempty"`
	Authority   AuthorityCeiling `json:"authority"`
}

func Capture(observation Observation) (Recording, error) {
	if !validSource(observation.Source) || observation.Locator == "" || len(observation.Locator) > 2048 ||
		strings.ContainsRune(observation.Locator, '\x00') || len(observation.Payload) == 0 || len(observation.Payload) > maxPayloadBytes {
		return Recording{}, ErrInvalidObservation
	}
	if locatorContainsSensitiveData(observation.Locator) {
		return Recording{}, ErrSensitiveObservation
	}
	value, err := decodeStrict(observation.Payload)
	if err != nil {
		return Recording{}, err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return Recording{}, ErrInvalidObservation
	}
	recording := Recording{
		Version: RecordingVersion, Source: observation.Source,
		SourceIdentityDigest: digest([]byte(string(observation.Source) + "\x00" + observation.Locator)),
		CanonicalPayload:     append([]byte(nil), canonical...), PayloadDigest: digest(canonical),
	}
	recording.Digest = recordingDigest(recording)
	return recording, nil
}

func Infer(recording Recording) (ContractCandidate, error) {
	value, err := verifyRecording(recording)
	if err != nil {
		return ContractCandidate{}, err
	}
	typesByPath := map[string]map[string]struct{}{}
	collectShape(value, "", typesByPath)
	paths := make([]string, 0, len(typesByPath))
	for path := range typesByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	fields := make([]Field, 0, len(paths))
	for _, path := range paths {
		types := make([]string, 0, len(typesByPath[path]))
		for kind := range typesByPath[path] {
			types = append(types, kind)
		}
		sort.Strings(types)
		fields = append(fields, Field{Path: path, Types: types})
	}
	candidate := ContractCandidate{
		Version: ContractVersion, Source: recording.Source,
		SourceIdentityDigest: recording.SourceIdentityDigest, SampleDigest: recording.PayloadDigest,
		Fields: fields, Trust: TrustUntrusted, Authority: AuthorityCeiling{ReadOnly: true},
	}
	if err := candidate.Validate(); err != nil {
		return ContractCandidate{}, err
	}
	return candidate, nil
}

func (candidate ContractCandidate) Validate() error {
	if candidate.Version != ContractVersion || !validSource(candidate.Source) || !digestPattern.MatchString(candidate.SourceIdentityDigest) ||
		!digestPattern.MatchString(candidate.SampleDigest) || candidate.Trust != TrustUntrusted {
		return ErrInvalidCandidate
	}
	if candidate.Authority != (AuthorityCeiling{ReadOnly: true}) {
		return ErrAuthorityEscalation
	}
	if len(candidate.Fields) == 0 || candidate.Fields[0].Path != "" {
		return ErrInvalidCandidate
	}
	previous := ""
	for index, field := range candidate.Fields {
		if !validFieldPath(field.Path) {
			return ErrInvalidCandidate
		}
		if field.Path == "" && index != 0 {
			return ErrInvalidCandidate
		}
		if index > 0 && field.Path <= previous {
			return ErrInvalidCandidate
		}
		if len(field.Types) == 0 {
			return ErrInvalidCandidate
		}
		lastType := ""
		for typeIndex, kind := range field.Types {
			if !validJSONType(kind) || (typeIndex > 0 && kind <= lastType) {
				return ErrInvalidCandidate
			}
			lastType = kind
		}
		previous = field.Path
	}
	return nil
}

func DetectDrift(baseline, current ContractCandidate) (Drift, error) {
	if err := baseline.Validate(); err != nil {
		return Drift{}, err
	}
	if err := current.Validate(); err != nil {
		return Drift{}, err
	}
	if baseline.Source != current.Source || baseline.SourceIdentityDigest != current.SourceIdentityDigest {
		return Drift{}, ErrInvalidCandidate
	}
	before := fieldMap(baseline.Fields)
	after := fieldMap(current.Fields)
	drift := Drift{Authority: AuthorityCeiling{ReadOnly: true}}
	for path, beforeTypes := range before {
		afterTypes, exists := after[path]
		if !exists {
			drift.Removed = append(drift.Removed, path)
			continue
		}
		if strings.Join(beforeTypes, "\x00") != strings.Join(afterTypes, "\x00") {
			drift.TypeChanged = append(drift.TypeChanged, TypeChange{Path: path, Before: append([]string(nil), beforeTypes...), After: append([]string(nil), afterTypes...)})
		}
	}
	for path := range after {
		if _, exists := before[path]; !exists {
			drift.Added = append(drift.Added, path)
		}
	}
	sort.Strings(drift.Added)
	sort.Strings(drift.Removed)
	sort.Slice(drift.TypeChanged, func(i, j int) bool { return drift.TypeChanged[i].Path < drift.TypeChanged[j].Path })
	drift.Changed = len(drift.Added)+len(drift.Removed)+len(drift.TypeChanged) > 0
	return drift, nil
}

func verifyRecording(recording Recording) (any, error) {
	if recording.Version != RecordingVersion || !validSource(recording.Source) || !digestPattern.MatchString(recording.SourceIdentityDigest) ||
		!digestPattern.MatchString(recording.PayloadDigest) || !digestPattern.MatchString(recording.Digest) || len(recording.CanonicalPayload) == 0 ||
		digest(recording.CanonicalPayload) != recording.PayloadDigest || recordingDigest(recording) != recording.Digest {
		return nil, ErrRecordingIntegrity
	}
	value, err := decodeStrict(recording.CanonicalPayload)
	if err != nil {
		return nil, ErrRecordingIntegrity
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, recording.CanonicalPayload) {
		return nil, ErrRecordingIntegrity
	}
	return value, nil
}

func recordingDigest(recording Recording) string {
	body := struct {
		Version              string     `json:"version"`
		Source               SourceKind `json:"source"`
		SourceIdentityDigest string     `json:"source_identity_digest"`
		PayloadDigest        string     `json:"payload_digest"`
	}{recording.Version, recording.Source, recording.SourceIdentityDigest, recording.PayloadDigest}
	encoded, _ := json.Marshal(body)
	return digest(encoded)
}

func decodeStrict(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	value, err := parseValue(decoder, 0, &nodes)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF || token != nil {
		return nil, ErrInvalidObservation
	}
	return value, nil
}

func parseValue(decoder *json.Decoder, depth int, nodes *int) (any, error) {
	if depth > maxDepth || *nodes >= maxNodes {
		return nil, ErrInvalidObservation
	}
	*nodes = *nodes + 1
	token, err := decoder.Token()
	if err != nil {
		return nil, ErrInvalidObservation
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				key, ok := keyToken.(string)
				if err != nil || !ok {
					return nil, ErrInvalidObservation
				}
				if _, exists := object[key]; exists {
					return nil, ErrInvalidObservation
				}
				if isSensitiveKey(key) {
					return nil, ErrSensitiveObservation
				}
				child, err := parseValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				object[key] = child
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrInvalidObservation
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				child, err := parseValue(decoder, depth+1, nodes)
				if err != nil {
					return nil, err
				}
				array = append(array, child)
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrInvalidObservation
			}
			return array, nil
		default:
			return nil, ErrInvalidObservation
		}
	case string:
		if valueSecretPattern.MatchString(value) {
			return nil, ErrSensitiveObservation
		}
		return value, nil
	case json.Number, bool, nil:
		return value, nil
	default:
		return nil, ErrInvalidObservation
	}
}

func collectShape(value any, path string, result map[string]map[string]struct{}) {
	kind := jsonType(value)
	if result[path] == nil {
		result[path] = map[string]struct{}{}
	}
	result[path][kind] = struct{}{}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			collectShape(child, path+"/"+escapePointer(key), result)
		}
	case []any:
		for _, child := range typed {
			collectShape(child, path+"/*", result)
		}
	}
}

func fieldMap(fields []Field) map[string][]string {
	result := make(map[string][]string, len(fields))
	for _, field := range fields {
		result[field.Path] = field.Types
	}
	return result
}

func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		panic(fmt.Sprintf("unexpected decoded JSON type %T", value))
	}
}

func validJSONType(kind string) bool {
	switch kind {
	case "null", "boolean", "number", "string", "array", "object":
		return true
	default:
		return false
	}
}

func validSource(source SourceKind) bool { return source == SourceWeb || source == SourceCLI }

func locatorContainsSensitiveData(locator string) bool {
	if valueSecretPattern.MatchString(locator) {
		return true
	}
	if urlContainsSensitiveData(locator) {
		return true
	}
	for _, token := range strings.FieldsFunc(locator, func(r rune) bool {
		return unicode.IsSpace(r) || r == '?' || r == '&'
	}) {
		candidate := strings.Trim(token, `"'(),;`)
		if urlContainsSensitiveData(candidate) {
			return true
		}
		if strings.HasPrefix(candidate, "-") {
			if index := strings.IndexAny(candidate, "=:"); index >= 0 && urlContainsSensitiveData(strings.Trim(candidate[index+1:], `"'(),;`)) {
				return true
			}
		}
		token = strings.TrimLeft(token, "-/")
		if index := strings.IndexAny(token, "=:"); index >= 0 {
			token = token[:index]
		}
		if isSensitiveKey(token) {
			return true
		}
	}
	return false
}

func urlContainsSensitiveData(locator string) bool {
	parsed, err := url.Parse(locator)
	if err != nil {
		return false
	}
	if parsed.User != nil {
		if _, hasPassword := parsed.User.Password(); hasPassword {
			return true
		}
	}
	for key := range parsed.Query() {
		if isSensitiveKey(key) {
			return true
		}
	}
	return false
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	normalized = strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(normalized)
	if _, sensitive := sensitiveKeys[normalized]; sensitive {
		return true
	}
	for _, fragment := range sensitiveKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func validFieldPath(path string) bool {
	if path == "" {
		return true
	}
	if len(path) > 4096 || !strings.HasPrefix(path, "/") {
		return false
	}
	for _, encoded := range strings.Split(path[1:], "/") {
		if encoded == "*" {
			continue
		}
		decoded := strings.ReplaceAll(encoded, "~2", "*")
		decoded = strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
		if decoded == "" || escapePointer(decoded) != encoded || isSensitiveKey(decoded) {
			return false
		}
	}
	return true
}

func escapePointer(value string) string {
	value = strings.ReplaceAll(value, "~", "~0")
	value = strings.ReplaceAll(value, "/", "~1")
	return strings.ReplaceAll(value, "*", "~2")
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
