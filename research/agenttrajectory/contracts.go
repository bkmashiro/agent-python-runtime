// Package agenttrajectory contains the public, deterministic day-trip fixture
// and the fail-closed contracts used by the experiment-only agent harness.
package agenttrajectory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	PlanningBriefSchemaVersion     = "pysolate.day-trip-planning-brief.v1"
	CandidateResponseSchemaVersion = "pysolate.day-trip-candidate.v1"
	SelectionResponseSchemaVersion = "pysolate.day-trip-selection.v1"
	FinalResponseSchemaVersion     = "pysolate.day-trip-final.v1"
	UserRequestSchemaVersion       = "pysolate.day-trip-user.v1"
	WorkspaceRequestSchemaVersion  = "pysolate.day-trip-workspace-request.v1"
	APIFixtureSchemaVersion        = "pysolate.day-trip-api-fixture.v1"

	// MaxContractTextBytes bounds every free-text field, including generated source.
	MaxContractTextBytes = 16 << 10
	MaxContractJSONBytes = 64 << 10
	MaxContractJSONDepth = 32
	MaxFixtureFileBytes  = 64 << 10

	// This is filled with the digest of the checked-in public fixture. Keeping the
	// value in the package makes accidental fixture drift fail closed in tests and
	// in callers that load the fixture before a run.
	DayTripFixtureAggregateSHA256 = "sha256:13ec6da68d48f7f14f5801f7034fb536ddd68c4934174ffa9b681b29093d4671"
)

var (
	ErrInvalidContract = errors.New("invalid agent trajectory contract")
	ErrInvalidFixture  = errors.New("invalid public agent trajectory fixture")

	importRE     = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_][A-Za-z0-9_.]*)`)
	fromRE       = regexp.MustCompile(`(?m)^\s*from\s+([A-Za-z_][A-Za-z0-9_.]*)\s+import\s+`)
	travelCallRE = regexp.MustCompile(`\btravel\.([A-Za-z_][A-Za-z0-9_]*)\b`)
	resultRE     = regexp.MustCompile(`(?m)(?:^|;)\s*result\s*=`)
)

const (
	CandidateBrighton = "brighton"
	CandidateOxford   = "oxford"
)

var fixedCandidateIDs = []string{CandidateBrighton, CandidateOxford}
var fixedSkillIDs = []string{"travel-research", "budget-checking", "itinerary-formatting"}
var allowedTravelCapabilities = map[string]struct{}{
	"weather": {}, "rail": {}, "attractions": {}, "delay": {},
}
var allowedPythonImports = map[string]struct{}{"json": {}, "math": {}, "travel": {}}
var blockedPythonWords = []string{
	"__import__", "eval", "exec", "compile", "open", "input", "socket", "requests",
	"urllib", "http", "https", "subprocess", "os", "sys", "pathlib", "shutil", "ctypes",
	"pickle", "marshal", "importlib", "pip", "connect", "urlopen",
}
var blockedPythonPatterns = func() []*regexp.Regexp {
	patterns := make([]*regexp.Regexp, 0, len(blockedPythonWords))
	for _, word := range blockedPythonWords {
		patterns = append(patterns, regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(word)+`\b`))
	}
	return patterns
}()

// PlanningBrief is the main Agent's bounded task and fixed two-candidate plan.
type PlanningBrief struct {
	SchemaVersion string   `json:"schema_version"`
	Task          string   `json:"task"`
	CandidateIDs  []string `json:"candidate_ids"`
}

// CandidateResponse is the visible result and constrained Python emitted by a
// candidate Agent. Python is validated here, before any Guest execution.
type CandidateResponse struct {
	SchemaVersion string `json:"schema_version"`
	CandidateID   string `json:"candidate_id"`
	Summary       string `json:"summary"`
	PythonSource  string `json:"python_source"`
}

// SelectionResponse is the main Agent's branch decision.
type SelectionResponse struct {
	SchemaVersion       string `json:"schema_version"`
	SelectedCandidateID string `json:"selected_candidate_id"`
	Justification       string `json:"justification"`
}

// FinalResponse is the user-facing synthesis after the selected branch is sealed.
type FinalResponse struct {
	SchemaVersion       string  `json:"schema_version"`
	SelectedCandidateID string  `json:"selected_candidate_id"`
	Itinerary           string  `json:"itinerary"`
	TotalCostGBP        float64 `json:"total_cost_gbp"`
}

type UserRequest struct {
	SchemaVersion string   `json:"schema_version"`
	DepartureCity string   `json:"departure_city"`
	Day           string   `json:"day"`
	Travellers    int      `json:"travellers"`
	BudgetGBP     float64  `json:"budget_gbp"`
	CandidateIDs  []string `json:"candidate_ids"`
	Request       string   `json:"request"`
}

type WorkspaceRequest struct {
	SchemaVersion  string   `json:"schema_version"`
	Origin         string   `json:"origin"`
	Day            string   `json:"day"`
	Travellers     int      `json:"travellers"`
	BudgetGBP      float64  `json:"budget_gbp"`
	CandidateIDs   []string `json:"candidate_ids"`
	RequiredChecks []string `json:"required_checks"`
}

type WeatherResult struct {
	Forecast      string  `json:"forecast"`
	HighC         float64 `json:"high_c"`
	RainChancePct int     `json:"rain_chance_pct"`
}

type RailLeg struct {
	Departure string  `json:"departure"`
	Arrival   string  `json:"arrival"`
	CostGBP   float64 `json:"cost_gbp"`
}

type RailResult struct {
	Outbound     RailLeg `json:"outbound"`
	Return       RailLeg `json:"return"`
	TotalCostGBP float64 `json:"total_cost_gbp"`
	Currency     string  `json:"currency"`
}

type AttractionResult struct {
	Name         string  `json:"name"`
	OpenSaturday bool    `json:"open_saturday"`
	OpeningHours string  `json:"opening_hours"`
	EntryCostGBP float64 `json:"entry_cost_gbp"`
}

type DeterministicAPIFixture struct {
	SchemaVersion string                      `json:"schema_version"`
	Weather       map[string]WeatherResult    `json:"weather"`
	Rail          map[string]RailResult       `json:"rail"`
	Attractions   map[string]AttractionResult `json:"attractions"`
	Delays        map[string]int              `json:"delays"`
	APILatencyMS  map[string]int              `json:"api_latency_ms"`
}

type WorkspaceFixture struct {
	Request WorkspaceRequest
	API     DeterministicAPIFixture
}

// Fixture is the complete repository-owned public input. Files retains exact
// bytes for provenance and identity calculations; callers must treat them as
// data, never as instructions overriding the contract.
type Fixture struct {
	System          string
	Skills          map[string]string
	User            UserRequest
	Workspace       WorkspaceFixture
	Files           map[string][]byte
	SystemSHA256    string
	SkillSHA256     map[string]string
	UserSHA256      string
	WorkspaceSHA256 string
	AggregateSHA256 string
}

type fixtureFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

var expectedFixtureFiles = []string{
	"system.md",
	"skills/travel-research.md",
	"skills/budget-checking.md",
	"skills/itinerary-formatting.md",
	"user.json",
	"workspace/request.json",
	"workspace/deterministic-api-fixture.json",
}

func (brief PlanningBrief) Validate() error {
	if brief.SchemaVersion != PlanningBriefSchemaVersion || !boundedText(brief.Task, 20, MaxContractTextBytes) || !sameCandidates(brief.CandidateIDs) {
		return ErrInvalidContract
	}
	return nil
}

func (candidate CandidateResponse) Validate() error {
	if candidate.SchemaVersion != CandidateResponseSchemaVersion || !validCandidateID(candidate.CandidateID) ||
		!boundedText(candidate.Summary, 1, MaxContractTextBytes) || !validCandidatePythonSource(candidate.PythonSource, candidate.CandidateID) {
		return ErrInvalidContract
	}
	return nil
}

func (selection SelectionResponse) Validate() error {
	if selection.SchemaVersion != SelectionResponseSchemaVersion || !validCandidateID(selection.SelectedCandidateID) ||
		!boundedText(selection.Justification, 1, MaxContractTextBytes) {
		return ErrInvalidContract
	}
	return nil
}

func (response FinalResponse) Validate() error {
	if response.SchemaVersion != FinalResponseSchemaVersion || !validCandidateID(response.SelectedCandidateID) ||
		!boundedText(response.Itinerary, 1, MaxContractTextBytes) || math.IsNaN(response.TotalCostGBP) || math.IsInf(response.TotalCostGBP, 0) || response.TotalCostGBP < 0 || response.TotalCostGBP > 100 {
		return ErrInvalidContract
	}
	return nil
}

func EncodePlanningBrief(value PlanningBrief) ([]byte, string, error) {
	return encodeContract(value, value.Validate)
}
func DecodePlanningBrief(data []byte) (PlanningBrief, string, error) {
	var value PlanningBrief
	identity, err := decodeContract(data, &value, func() error { return value.Validate() })
	return value, identity, err
}
func EncodeCandidateResponse(value CandidateResponse) ([]byte, string, error) {
	return encodeContract(value, value.Validate)
}
func DecodeCandidateResponse(data []byte) (CandidateResponse, string, error) {
	var value CandidateResponse
	identity, err := decodeContract(data, &value, func() error { return value.Validate() })
	return value, identity, err
}
func EncodeSelectionResponse(value SelectionResponse) ([]byte, string, error) {
	return encodeContract(value, value.Validate)
}
func DecodeSelectionResponse(data []byte) (SelectionResponse, string, error) {
	var value SelectionResponse
	identity, err := decodeContract(data, &value, func() error { return value.Validate() })
	return value, identity, err
}
func EncodeFinalResponse(value FinalResponse) ([]byte, string, error) {
	return encodeContract(value, value.Validate)
}
func DecodeFinalResponse(data []byte) (FinalResponse, string, error) {
	var value FinalResponse
	identity, err := decodeContract(data, &value, func() error { return value.Validate() })
	return value, identity, err
}

func (value PlanningBrief) Identity() string     { return contractIdentity(value, value.Validate) }
func (value CandidateResponse) Identity() string { return contractIdentity(value, value.Validate) }
func (value SelectionResponse) Identity() string { return contractIdentity(value, value.Validate) }
func (value FinalResponse) Identity() string     { return contractIdentity(value, value.Validate) }

func encodeContract(value any, validate func() error) ([]byte, string, error) {
	if err := validate(); err != nil {
		return nil, "", err
	}
	data, err := json.Marshal(value)
	if err != nil || len(data) > MaxContractJSONBytes {
		return nil, "", ErrInvalidContract
	}
	return data, digest(data), nil
}

func contractIdentity(value any, validate func() error) string {
	data, _, err := encodeContract(value, validate)
	if err != nil {
		return ""
	}
	return digest(data)
}

func decodeContract(data []byte, destination any, validate func() error) (string, error) {
	if len(data) == 0 || len(data) > MaxContractJSONBytes || scanJSON(data) != nil {
		return "", ErrInvalidContract
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "", ErrInvalidContract
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", ErrInvalidContract
	}
	if err := validate(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(data, canonical) {
		return "", ErrInvalidContract
	}
	return digest(canonical), nil
}

// scanJSON rejects duplicate object keys, nulls, excessive nesting, and trailing
// JSON before the typed decoder sees the document.
func scanJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalidContract
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder, depth int) error {
	if depth > MaxContractJSONDepth {
		return ErrInvalidContract
	}
	token, err := decoder.Token()
	if err != nil {
		return ErrInvalidContract
	}
	switch value := token.(type) {
	case nil:
		return ErrInvalidContract
	case json.Delim:
		switch value {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				key, ok := mustStringToken(decoder)
				if !ok {
					return ErrInvalidContract
				}
				if _, duplicate := seen[key]; duplicate {
					return ErrInvalidContract
				}
				seen[key] = struct{}{}
				if err := scanJSONValue(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalidContract
			}
		case '[':
			for decoder.More() {
				if err := scanJSONValue(decoder, depth+1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalidContract
			}
		default:
			return ErrInvalidContract
		}
	}
	return nil
}

func mustStringToken(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	value, ok := token.(string)
	return value, err == nil && ok
}

func validPythonSource(source string) bool {
	if !boundedText(source, 1, MaxContractTextBytes) || strings.ContainsRune(source, '\x00') || strings.Contains(source, "..") || strings.Contains(source, "/") || strings.Contains(source, "\\") || strings.Contains(source, "://") || !resultRE.MatchString(source) {
		return false
	}
	for _, pattern := range blockedPythonPatterns {
		if pattern.MatchString(source) {
			return false
		}
	}
	for _, match := range importRE.FindAllStringSubmatch(source, -1) {
		if _, ok := allowedPythonImports[match[1]]; !ok {
			return false
		}
	}
	for _, match := range fromRE.FindAllStringSubmatch(source, -1) {
		if _, ok := allowedPythonImports[match[1]]; !ok {
			return false
		}
	}
	for _, match := range travelCallRE.FindAllStringSubmatch(source, -1) {
		if _, ok := allowedTravelCapabilities[match[1]]; !ok {
			return false
		}
	}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") {
			if strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") {
				return false
			}
		}
	}
	return true
}

func validCandidatePythonSource(source, candidateID string) bool {
	if !validPythonSource(source) || !strings.Contains(source, `"`+candidateID+`"`) {
		return false
	}
	counts := map[string]int{}
	for _, match := range travelCallRE.FindAllStringSubmatch(source, -1) {
		counts[match[1]]++
	}
	return counts["weather"] == 1 && counts["rail"] == 1 && counts["attractions"] == 1 && counts["delay"] == 0 && len(counts) == 3
}

func boundedText(value string, min, max int) bool {
	return len(value) >= min && len(value) <= max && strings.TrimSpace(value) != ""
}
func validCandidateID(value string) bool {
	return value == CandidateBrighton || value == CandidateOxford
}
func validSkillID(value string) bool {
	for _, skillID := range fixedSkillIDs {
		if value == skillID {
			return true
		}
	}
	return false
}

// ValidateSkillID enforces the repository-owned public skill vocabulary.
func ValidateSkillID(value string) error {
	if !validSkillID(value) {
		return ErrInvalidContract
	}
	return nil
}

// AllowedSkillIDs returns a defensive copy of the fixed public skill vocabulary.
func AllowedSkillIDs() []string { return append([]string(nil), fixedSkillIDs...) }
func sameCandidates(values []string) bool {
	return len(values) == len(fixedCandidateIDs) && values[0] == fixedCandidateIDs[0] && values[1] == fixedCandidateIDs[1]
}
func sameStrings(values, expected []string) bool {
	if len(values) != len(expected) {
		return false
	}
	for i := range expected {
		if values[i] != expected[i] {
			return false
		}
	}
	return true
}

func (value UserRequest) Validate() error {
	if value.SchemaVersion != UserRequestSchemaVersion || value.DepartureCity != "London" || value.Day != "Saturday" || value.Travellers != 2 || value.BudgetGBP != 100 || !sameCandidates(value.CandidateIDs) || !boundedText(value.Request, 20, MaxContractTextBytes) {
		return ErrInvalidFixture
	}
	return nil
}
func (value WorkspaceRequest) Validate() error {
	if value.SchemaVersion != WorkspaceRequestSchemaVersion || value.Origin != "London" || value.Day != "Saturday" || value.Travellers != 2 || value.BudgetGBP != 100 || !sameCandidates(value.CandidateIDs) || !sameStrings(value.RequiredChecks, []string{"weather", "rail", "attractions"}) {
		return ErrInvalidFixture
	}
	return nil
}
func (value DeterministicAPIFixture) Validate() error {
	if value.SchemaVersion != APIFixtureSchemaVersion || len(value.Weather) != 2 || len(value.Rail) != 2 || len(value.Attractions) != 2 || len(value.Delays) != 2 || len(value.APILatencyMS) != 3 {
		return ErrInvalidFixture
	}
	for _, capabilityName := range []string{"weather", "rail", "attractions"} {
		latency, ok := value.APILatencyMS[capabilityName]
		if !ok || latency < 50 || latency > 2000 {
			return ErrInvalidFixture
		}
	}
	for _, candidate := range fixedCandidateIDs {
		weather, weatherOK := value.Weather[candidate]
		rail, railOK := value.Rail[candidate]
		attraction, attractionOK := value.Attractions[candidate]
		delay, delayOK := value.Delays[candidate]
		if !weatherOK || !railOK || !attractionOK || !delayOK || weather.Forecast == "" || math.IsNaN(weather.HighC) || math.IsInf(weather.HighC, 0) || weather.RainChancePct < 0 || weather.RainChancePct > 100 || !validRailLeg(rail.Outbound) || !validRailLeg(rail.Return) || math.IsNaN(rail.TotalCostGBP) || math.IsInf(rail.TotalCostGBP, 0) || rail.TotalCostGBP < 0 || rail.Currency != "GBP" || attraction.Name == "" || !attraction.OpenSaturday || math.IsNaN(attraction.EntryCostGBP) || math.IsInf(attraction.EntryCostGBP, 0) || attraction.EntryCostGBP < 0 || delay < 0 {
			return ErrInvalidFixture
		}
	}
	return nil
}

func validRailLeg(leg RailLeg) bool {
	return boundedText(leg.Departure, 1, 32) && boundedText(leg.Arrival, 1, 32) && !math.IsNaN(leg.CostGBP) && !math.IsInf(leg.CostGBP, 0) && leg.CostGBP >= 0
}

func decodeFixtureJSON(data []byte, destination any, validate func() error) error {
	if len(data) == 0 || len(data) > MaxFixtureFileBytes || scanJSON(data) != nil {
		return ErrInvalidFixture
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalidFixture
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidFixture
	}
	if err := validate(); err != nil {
		return err
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(data, canonical) {
		return ErrInvalidFixture
	}
	return nil
}

// LoadFixture loads exactly the seven checked-in public files and verifies each
// typed input plus the aggregate identity. It never follows symlinks or accepts
// extra files.
func LoadFixture(root string) (Fixture, error) {
	var fixture Fixture
	if root == "" {
		return fixture, ErrInvalidFixture
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return fixture, ErrInvalidFixture
	}
	files, err := collectFixtureFiles(root)
	if err != nil {
		return fixture, err
	}
	fixture.Files = files
	fixture.System = string(files["system.md"])
	if !boundedText(fixture.System, 20, MaxFixtureFileBytes) || hasPrivateMarker(fixture.System) {
		return Fixture{}, ErrInvalidFixture
	}
	fixture.SystemSHA256 = digest(files["system.md"])
	fixture.Skills = map[string]string{}
	fixture.SkillSHA256 = map[string]string{}
	for _, skillID := range fixedSkillIDs {
		path := "skills/" + skillID + ".md"
		body := string(files[path])
		if !boundedText(body, 20, MaxFixtureFileBytes) || hasPrivateMarker(body) {
			return Fixture{}, ErrInvalidFixture
		}
		fixture.Skills[skillID] = body
		fixture.SkillSHA256[skillID] = digest(files[path])
	}
	if hasPrivateMarker(string(files["user.json"])) || hasPrivateMarker(string(files["workspace/request.json"])) || hasPrivateMarker(string(files["workspace/deterministic-api-fixture.json"])) {
		return Fixture{}, ErrInvalidFixture
	}
	if err := decodeFixtureJSON(files["user.json"], &fixture.User, func() error { return fixture.User.Validate() }); err != nil {
		return Fixture{}, err
	}
	if err := decodeFixtureJSON(files["workspace/request.json"], &fixture.Workspace.Request, func() error { return fixture.Workspace.Request.Validate() }); err != nil {
		return Fixture{}, err
	}
	if err := decodeFixtureJSON(files["workspace/deterministic-api-fixture.json"], &fixture.Workspace.API, func() error { return fixture.Workspace.API.Validate() }); err != nil {
		return Fixture{}, err
	}
	fixture.UserSHA256 = digest(files["user.json"])
	workspaceDocument := append(append([]byte(nil), files["workspace/request.json"]...), files["workspace/deterministic-api-fixture.json"]...)
	fixture.WorkspaceSHA256 = digest(workspaceDocument)
	fixture.AggregateSHA256 = aggregateIdentity(files)
	if fixture.AggregateSHA256 != DayTripFixtureAggregateSHA256 {
		return Fixture{}, ErrInvalidFixture
	}
	return fixture, nil
}

func collectFixtureFiles(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	allowedFiles := map[string]struct{}{}
	for _, name := range expectedFixtureFiles {
		allowedFiles[name] = struct{}{}
	}
	allowedDirs := map[string]struct{}{".": {}, "": {}, "skills": {}, "workspace": {}}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrInvalidFixture
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return ErrInvalidFixture
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if _, ok := allowedDirs[rel]; !ok {
				return ErrInvalidFixture
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalidFixture
		}
		if _, ok := allowedFiles[rel]; !ok {
			return ErrInvalidFixture
		}
		info, err := entry.Info()
		if err != nil || info.Size() > MaxFixtureFileBytes {
			return ErrInvalidFixture
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return ErrInvalidFixture
		}
		files[rel] = body
		return nil
	})
	if err != nil {
		return nil, ErrInvalidFixture
	}
	for _, name := range expectedFixtureFiles {
		if _, ok := files[name]; !ok {
			return nil, ErrInvalidFixture
		}
	}
	return files, nil
}

func hasPrivateMarker(value string) bool {
	for _, marker := range []string{"PRIVATE", "api_key", "authorization", "begin private key", "/Users/", "\\Users\\", "Bearer "} {
		if strings.Contains(strings.ToLower(value), strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func aggregateIdentity(files map[string][]byte) string {
	entries := make([]fixtureFile, 0, len(files))
	for name, body := range files {
		entries = append(entries, fixtureFile{Path: name, SHA256: digest(body)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	return digest(data)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FixtureAggregateIdentity is exported for tests and tooling that want to pin
// a complete file map without loading typed fixture values.
func FixtureAggregateIdentity(files map[string][]byte) string { return aggregateIdentity(files) }

// StableSHA256 returns the repository's prefixed SHA-256 representation.
func StableSHA256(data []byte) string { return digest(data) }
