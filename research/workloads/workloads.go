// Package workloads defines the three fixed, mechanism-only local evaluation workloads.
package workloads

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

var ErrOracle = errors.New("workload oracle mismatch")

type WorkspaceEntry struct {
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Executable bool   `json:"executable"`
	Size       uint64 `json:"size"`
	SHA256     string `json:"sha256"`
}

type Descriptor struct {
	ID                      string                 `json:"id"`
	Identity                string                 `json:"identity"`
	CodeSHA256              string                 `json:"code_sha256"`
	InputSHA256             string                 `json:"input_sha256"`
	WorkspaceSeedSHA256     string                 `json:"workspace_seed_sha256"`
	ExpectedResultSHA256    string                 `json:"expected_result_sha256"`
	ExpectedWorkspace       []WorkspaceEntry       `json:"expected_workspace"`
	ExpectedCapabilityCalls uint32                 `json:"expected_capability_calls"`
	Treatments              []TreatmentDisposition `json:"treatments"`
}

type TreatmentDisposition struct {
	Treatment string `json:"treatment"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
}

type Workload struct {
	ID                      string
	Code                    string
	Inputs                  json.RawMessage
	SeedFiles               map[string][]byte
	CodeSHA256              string
	InputSHA256             string
	WorkspaceSeedSHA256     string
	ExpectedResult          json.RawMessage
	ExpectedResultSHA256    string
	ExpectedWorkspace       []WorkspaceEntry
	ExpectedCapabilityCalls uint32
	Treatments              []TreatmentDisposition
}

func digest(raw []byte) string { sum := sha256.Sum256(raw); return fmt.Sprintf("sha256:%x", sum[:]) }
func fileEntry(path string, body []byte) WorkspaceEntry {
	return WorkspaceEntry{Path: path, Kind: "file", Size: uint64(len(body)), SHA256: digest(body)}
}

func Canonical() ([]Workload, error) {
	structuredCode := `import json
catalog=sources.demo_catalog()
manifest=sources.benchmark_manifest()
ranked=sorted([{'id':item['id'],'score':item['score'],'title':item['title']} for item in catalog],key=lambda item:(-item['score'],item['id']))
suite={'id':manifest['suite']['id'],'version':manifest['suite']['version'],'case_ids':sorted([item['id'] for item in manifest['cases']])}
report={'catalog':ranked,'suite':suite}
with open('/workspace/structured-report.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(report,sort_keys=True,separators=(',',':')))
result={'top_id':ranked[0]['id'],'suite_id':suite['id'],'case_count':len(suite['case_ids'])}`
	structuredReport := []byte(`{"catalog":[{"id":"alpha","score":7,"title":"Alpha"}],"suite":{"case_ids":["workspace-summary"],"id":"pysolate-core","version":"2026.08"}}`)
	structuredResult := json.RawMessage(`{"top_id":"alpha","suite_id":"pysolate-core","case_count":1}`)

	statefulCode := `import csv,json
with open('/workspace/metrics.csv','r',encoding='utf-8',newline='') as handle:
    rows=[{'id':row['id'],'value':int(row['value'])} for row in csv.DictReader(handle)]
rows=sorted(rows,key=lambda row:row['id'])
total=sum(row['value'] for row in rows)
normalized=[{'id':row['id'],'share_milli':(row['value']*1000)//total,'value':row['value']} for row in rows]
with open('/workspace/normalized.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(normalized,sort_keys=True,separators=(',',':')))
summary={'count':len(rows),'max_id':max(rows,key=lambda row:(row['value'],row['id']))['id'],'total':total}
with open('/workspace/summary.json','w',encoding='utf-8') as handle:
    handle.write(json.dumps(summary,sort_keys=True,separators=(',',':')))
result=summary`
	seed := []byte("id,value\nalpha,7\nbeta,3\n")
	normalized := []byte(`[{"id":"alpha","share_milli":700,"value":7},{"id":"beta","share_milli":300,"value":3}]`)
	summary := []byte(`{"count":2,"max_id":"alpha","total":10}`)
	statefulResult := json.RawMessage(summary)

	planningCode := `import json
candidates=sources.demo_catalog()
trace=[]
for item in sorted(candidates,key=lambda item:item['id']):
    score=item['score']*3+len(item['title'])
    trace.append({'id':item['id'],'score':score})
best=max(trace,key=lambda item:(item['score'],item['id']))
result={'selected_id':best['id'],'selected_score':best['score'],'trace':trace}`
	planningResult := json.RawMessage(`{"selected_id":"beta","selected_score":31,"trace":[{"id":"alpha","score":26},{"id":"beta","score":31},{"id":"gamma","score":23}]}`)

	values := []Workload{
		{ID: "structured-source-v1", Code: structuredCode, Inputs: json.RawMessage(`{}`), ExpectedResult: structuredResult, ExpectedWorkspace: []WorkspaceEntry{fileEntry("structured-report.json", structuredReport)}, ExpectedCapabilityCalls: 2, Treatments: treatmentMatrix(true, true, true, false)},
		{ID: "stateful-local-v1", Code: statefulCode, Inputs: json.RawMessage(`{}`), SeedFiles: map[string][]byte{"metrics.csv": seed}, ExpectedResult: statefulResult, ExpectedWorkspace: []WorkspaceEntry{fileEntry("metrics.csv", seed), fileEntry("normalized.json", normalized), fileEntry("summary.json", summary)}, ExpectedCapabilityCalls: 0, Treatments: treatmentMatrix(true, true, false, false)},
		{ID: "bounded-planning-v1", Code: planningCode, Inputs: json.RawMessage(`{}`), ExpectedResult: planningResult, ExpectedWorkspace: []WorkspaceEntry{}, ExpectedCapabilityCalls: 1, Treatments: treatmentMatrix(true, true, true, true)},
	}
	for index := range values {
		value := &values[index]
		value.CodeSHA256 = digest([]byte(value.Code))
		value.InputSHA256 = digest(value.Inputs)
		resultSHA, err := playback.CanonicalSHA256(value.ExpectedResult)
		if err != nil {
			return nil, err
		}
		value.ExpectedResultSHA256 = resultSHA
		if len(value.SeedFiles) > 0 {
			value.WorkspaceSeedSHA256 = seedIdentity(value.SeedFiles)
		}
		if err := value.Validate(); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func treatmentMatrix(live, offline, branch, deterministic bool) []TreatmentDisposition {
	values := []struct {
		name      string
		supported bool
		reason    string
	}{
		{name: "live_capture", supported: live, reason: "live_not_admitted"},
		{name: "offline_playback", supported: offline, reason: "offline_not_admitted"},
		{name: "counterfactual_branch", supported: branch, reason: "no_captured_capability_boundary"},
		{name: "deterministic", supported: deterministic, reason: "mounted_workspace_or_unqualified_scope"},
	}
	out := make([]TreatmentDisposition, len(values))
	for i, value := range values {
		out[i] = TreatmentDisposition{Treatment: value.name, Status: "supported"}
		if !value.supported {
			out[i].Status = "unsupported"
			out[i].Reason = value.reason
		}
	}
	return out
}

func seedIdentity(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	h.Write([]byte("pysolate-workload-seed-v1"))
	h.Write([]byte{0})
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte{0})
		sum := sha256.Sum256(files[name])
		h.Write(sum[:])
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func (w Workload) Validate() error {
	if w.ID == "" || w.Code == "" || !validDigest(w.CodeSHA256) || !validDigest(w.InputSHA256) || !validDigest(w.ExpectedResultSHA256) || w.ExpectedWorkspace == nil || w.ExpectedCapabilityCalls > 2 {
		return ErrOracle
	}
	if digest([]byte(w.Code)) != w.CodeSHA256 || digest(w.Inputs) != w.InputSHA256 {
		return ErrOracle
	}
	if got, err := playback.CanonicalSHA256(w.ExpectedResult); err != nil || got != w.ExpectedResultSHA256 {
		return ErrOracle
	}
	if w.ID == "stateful-local-v1" {
		if w.WorkspaceSeedSHA256 == "" || len(w.ExpectedWorkspace) != 3 || len(w.SeedFiles) == 0 || seedIdentity(w.SeedFiles) != w.WorkspaceSeedSHA256 {
			return ErrOracle
		}
	} else if w.WorkspaceSeedSHA256 != "" || len(w.SeedFiles) != 0 {
		return ErrOracle
	}
	previous := ""
	for _, entry := range w.ExpectedWorkspace {
		if !validGuestPath(entry.Path) || entry.Path <= previous || entry.Kind != "file" || entry.Size == 0 || !validDigest(entry.SHA256) {
			return ErrOracle
		}
		previous = entry.Path
	}
	if !validFamilyTreatments(w.ID, w.Treatments) {
		return ErrOracle
	}
	return ValidateProgramBoundary(w.Code)
}

func CanonicalDescriptorJSON() ([]byte, error) {
	workloads, err := Canonical()
	if err != nil {
		return nil, err
	}
	descriptors := make([]Descriptor, len(workloads))
	for i, workload := range workloads {
		descriptor, err := workload.PublicDescriptor()
		if err != nil {
			return nil, err
		}
		descriptors[i] = descriptor
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion string       `json:"schema_version"`
		Descriptors   []Descriptor `json:"descriptors"`
	}{SchemaVersion: "pysolate.real-workloads.v1", Descriptors: descriptors})
	if err != nil {
		return nil, ErrOracle
	}
	return append(encoded, '\n'), nil
}

func (w Workload) PublicDescriptor() (Descriptor, error) {
	if err := w.Validate(); err != nil {
		return Descriptor{}, err
	}
	descriptor := Descriptor{
		ID:                      w.ID,
		CodeSHA256:              w.CodeSHA256,
		InputSHA256:             w.InputSHA256,
		WorkspaceSeedSHA256:     w.WorkspaceSeedSHA256,
		ExpectedResultSHA256:    w.ExpectedResultSHA256,
		ExpectedWorkspace:       append([]WorkspaceEntry(nil), w.ExpectedWorkspace...),
		ExpectedCapabilityCalls: w.ExpectedCapabilityCalls,
		Treatments:              append([]TreatmentDisposition(nil), w.Treatments...),
	}
	payload, err := json.Marshal(struct {
		Domain     string     `json:"domain"`
		Descriptor Descriptor `json:"descriptor"`
	}{Domain: "pysolate-workload-descriptor-v1", Descriptor: descriptor})
	if err != nil {
		return Descriptor{}, ErrOracle
	}
	descriptor.Identity = digest(payload)
	return descriptor, nil
}

func validDigest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validTreatmentDispositions(values []TreatmentDisposition) bool {
	if len(values) != 4 {
		return false
	}
	expected := []string{"live_capture", "offline_playback", "counterfactual_branch", "deterministic"}
	for i, value := range values {
		if value.Treatment != expected[i] || (value.Status != "supported" && value.Status != "unsupported") || (value.Status == "supported" && value.Reason != "") || (value.Status == "unsupported" && value.Reason == "") {
			return false
		}
	}
	return true
}

func validFamilyTreatments(id string, values []TreatmentDisposition) bool {
	if !validTreatmentDispositions(values) {
		return false
	}
	want := map[string][]TreatmentDisposition{
		"structured-source-v1": treatmentMatrix(true, true, true, false),
		"stateful-local-v1":    treatmentMatrix(true, true, false, false),
		"bounded-planning-v1":  treatmentMatrix(true, true, true, true),
	}[id]
	return want != nil && slices.Equal(values, want)
}

func validGuestPath(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.ContainsAny(value, "\\:") && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../")
}

func validDescriptorFamilyShape(d Descriptor) bool {
	switch d.ID {
	case "structured-source-v1":
		return d.WorkspaceSeedSHA256 == "" && d.ExpectedCapabilityCalls == 2 && len(d.ExpectedWorkspace) == 1
	case "stateful-local-v1":
		return validDigest(d.WorkspaceSeedSHA256) && d.ExpectedCapabilityCalls == 0 && len(d.ExpectedWorkspace) == 3
	case "bounded-planning-v1":
		return d.WorkspaceSeedSHA256 == "" && d.ExpectedCapabilityCalls == 1 && len(d.ExpectedWorkspace) == 0
	default:
		return false
	}
}

func (d Descriptor) Validate() error {
	identity := d.Identity
	d.Identity = ""
	payload, err := json.Marshal(struct {
		Domain     string     `json:"domain"`
		Descriptor Descriptor `json:"descriptor"`
	}{Domain: "pysolate-workload-descriptor-v1", Descriptor: d})
	if err != nil || identity != digest(payload) || !validDescriptorFamilyShape(d) || !validFamilyTreatments(d.ID, d.Treatments) {
		return ErrOracle
	}
	for _, value := range []string{d.CodeSHA256, d.InputSHA256, d.ExpectedResultSHA256} {
		if !validDigest(value) {
			return ErrOracle
		}
	}
	previous := ""
	for _, entry := range d.ExpectedWorkspace {
		if !validGuestPath(entry.Path) || entry.Path <= previous || entry.Kind != "file" || entry.Size == 0 || !validDigest(entry.SHA256) {
			return ErrOracle
		}
		previous = entry.Path
	}
	return nil
}

func (w Workload) Verify(result json.RawMessage, entries []WorkspaceEntry, calls uint32) error {
	got, err := playback.CanonicalSHA256(result)
	if err != nil || got != w.ExpectedResultSHA256 || calls != w.ExpectedCapabilityCalls || !slices.Equal(entries, w.ExpectedWorkspace) {
		return ErrOracle
	}
	return nil
}

func ValidateProgramBoundary(code string) error {
	for _, forbidden := range []string{"subprocess", "socket", "urllib", "requests", "http.client", "os.system", "popen", "eval(", "exec(", "__import__", "open('/tmp", "open(\"/tmp"} {
		if strings.Contains(code, forbidden) {
			return ErrOracle
		}
	}
	return nil
}

func EqualResult(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return bytes.Equal(mustJSON(x), mustJSON(y))
}
func mustJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
