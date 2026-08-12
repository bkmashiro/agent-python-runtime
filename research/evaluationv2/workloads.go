package evaluationv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unicode/utf8"
)

const catalogFixture = `{"items":[{"id":"alpha","score":7,"title":"Alpha"},{"id":"beta","score":4,"title":"Beta"},{"id":"gamma","score":10,"title":"Gamma"}]}`

const (
	catalogFixtureSHA256  = "sha256:536b8b7b057fb135c7526d721e2627b15b68ec7aec6ba35c78fe7f852e6e1769"
	manifestFixtureSHA256 = "sha256:628dd5eb86f3e652c9280ab20191472badd289553c2e52bc51a7d18721fd0584"
)

type Definition struct {
	Workload Workload
	Code     string
	Inputs   json.RawMessage
	Expected json.RawMessage
}

func PilotDefinitions() ([]Definition, error) {
	catalogExpected := json.RawMessage(`{"id":"gamma","score":10,"title":"Gamma"}`)
	joinExpected := json.RawMessage(`{"catalog_id":"gamma","catalog_score":10,"case_id":"workspace-summary","metric_ids":["latency","quality"],"suite":"pysolate-core@2026.08","task_class":"workspace_transform"}`)
	definitions := []Definition{
		{
			Code:   "items=sources.demo_catalog()\nbest=sorted(items,key=lambda item:(-item['score'],item['id']))[0]\nresult={'id':best['id'],'score':best['score'],'title':best['title']}",
			Inputs: json.RawMessage(`{}`), Expected: catalogExpected,
			Workload: Workload{ID: "catalog-top-direct", Version: 1, RequiredCapabilities: []string{"sources.demo_catalog"}, ExpectedCapabilityCalls: 1},
		},
		{
			Code:   "items=sources.demo_catalog()\nmanifest=sources.benchmark_manifest()\nbest=sorted(items,key=lambda item:(-item['score'],item['id']))[0]\ncase=sorted(manifest['cases'],key=lambda item:item['id'])[0]\nresult={'catalog_id':best['id'],'catalog_score':best['score'],'case_id':case['id'],'metric_ids':sorted(metric['id'] for metric in case['metrics']),'suite':manifest['suite']['id']+'@'+manifest['suite']['version'],'task_class':case['task_class']}",
			Inputs: json.RawMessage(`{}`), Expected: joinExpected,
			Workload: Workload{ID: "source-join-ranking", Version: 1, RequiredCapabilities: []string{"sources.demo_catalog", "sources.benchmark_manifest"}, ExpectedCapabilityCalls: 2},
		},
	}
	for i := range definitions {
		definitions[i].Workload.SourceFixtureSHA256 = []string{catalogFixtureSHA256}
		if definitions[i].Workload.ID == "source-join-ranking" {
			definitions[i].Workload.SourceFixtureSHA256 = append(definitions[i].Workload.SourceFixtureSHA256, manifestFixtureSHA256)
		}
		codeHash := sha256.Sum256([]byte(definitions[i].Code))
		inputBytes, err := canonicalJSON(definitions[i].Inputs)
		if err != nil {
			return nil, ErrInvalid
		}
		definitions[i].Inputs = inputBytes
		inputHash := sha256.Sum256(inputBytes)
		expected, err := canonicalJSON(definitions[i].Expected)
		if err != nil {
			return nil, err
		}
		resultHash := sha256.Sum256(expected)
		definitions[i].Expected = expected
		definitions[i].Workload.CodeSHA256 = fmt.Sprintf("sha256:%x", codeHash)
		definitions[i].Workload.InputSHA256 = fmt.Sprintf("sha256:%x", inputHash)
		definitions[i].Workload.ExpectedResultSHA256 = fmt.Sprintf("sha256:%x", resultHash)
	}
	return definitions, nil
}

func ExpandedDefinitions() ([]Definition, error) {
	pilot, err := PilotDefinitions()
	if err != nil {
		return nil, err
	}
	definitions := []Definition{
		pilot[0],
		{Code: "manifest=sources.benchmark_manifest()\nresult={'suite':manifest['suite']['id']+'@'+manifest['suite']['version'],'case_count':len(manifest['cases']),'artifact_count':sum(len(case['input_artifacts']) for case in manifest['cases']),'metric_count':sum(len(case['metrics']) for case in manifest['cases'])}", Inputs: json.RawMessage(`{}`), Expected: json.RawMessage(`{"artifact_count":1,"case_count":1,"metric_count":2,"suite":"pysolate-core@2026.08"}`), Workload: Workload{ID: "manifest-suite-direct", Version: 1, RequiredCapabilities: []string{"sources.benchmark_manifest"}, SourceFixtureSHA256: []string{manifestFixtureSHA256}, ExpectedCapabilityCalls: 1}},
		{Code: "items=sources.demo_catalog()\nminimum=inputs['minimum_score']\nselected=sorted((item for item in items if item['score']>=minimum),key=lambda item:(-item['score'],item['id']))\nresult={'minimum_score':minimum,'ids':[item['id'] for item in selected],'count':len(selected),'score_total':sum(item['score'] for item in selected)}", Inputs: json.RawMessage(`{"minimum_score":5}`), Expected: json.RawMessage(`{"count":2,"ids":["gamma","alpha"],"minimum_score":5,"score_total":17}`), Workload: Workload{ID: "catalog-threshold-loop", Version: 1, RequiredCapabilities: []string{"sources.demo_catalog"}, SourceFixtureSHA256: []string{catalogFixtureSHA256}, ExpectedCapabilityCalls: 1}},
		{Code: "manifest=sources.benchmark_manifest()\nrows=[]\nfor case in manifest['cases']:\n artifacts=sorted(item['id'] for item in case['input_artifacts'])\n for metric in case['metrics']:\n  rows.append({'case_id':case['id'],'task_class':case['task_class'],'artifact_ids':artifacts,'metric_id':metric['id'],'direction':metric['direction'],'unit':metric['unit'],'minimum':metric['bounds']['minimum'],'maximum':metric['bounds']['maximum']})\nresult={'suite':manifest['suite']['id']+'@'+manifest['suite']['version'],'rows':sorted(rows,key=lambda row:(row['case_id'],row['metric_id']))}", Inputs: json.RawMessage(`{}`), Expected: json.RawMessage(`{"rows":[{"artifact_ids":["metrics-input"],"case_id":"workspace-summary","direction":"minimize","maximum":30000,"metric_id":"latency","minimum":0,"task_class":"workspace_transform","unit":"milliseconds"},{"artifact_ids":["metrics-input"],"case_id":"workspace-summary","direction":"maximize","maximum":100,"metric_id":"quality","minimum":0,"task_class":"workspace_transform","unit":"score"}],"suite":"pysolate-core@2026.08"}`), Workload: Workload{ID: "manifest-matrix", Version: 1, RequiredCapabilities: []string{"sources.benchmark_manifest"}, SourceFixtureSHA256: []string{manifestFixtureSHA256}, ExpectedCapabilityCalls: 1}},
		pilot[1],
	}
	return finalizeDefinitions(definitions)
}

func finalizeDefinitions(definitions []Definition) ([]Definition, error) {
	for i := range definitions {
		codeHash := sha256.Sum256([]byte(definitions[i].Code))
		inputBytes, err := canonicalJSON(definitions[i].Inputs)
		if err != nil {
			return nil, ErrInvalid
		}
		definitions[i].Inputs = inputBytes
		inputHash := sha256.Sum256(inputBytes)
		expected, err := canonicalJSON(definitions[i].Expected)
		if err != nil {
			return nil, ErrInvalid
		}
		definitions[i].Expected = expected
		resultHash := sha256.Sum256(expected)
		definitions[i].Workload.CodeSHA256 = fmt.Sprintf("sha256:%x", codeHash)
		definitions[i].Workload.InputSHA256 = fmt.Sprintf("sha256:%x", inputHash)
		definitions[i].Workload.ExpectedResultSHA256 = fmt.Sprintf("sha256:%x", resultHash)
	}
	return definitions, nil
}

func ExpandedCorpus() (Corpus, error) {
	definitions, err := ExpandedDefinitions()
	if err != nil {
		return Corpus{}, err
	}
	workloads := make([]Workload, len(definitions))
	for i := range definitions {
		workloads[i] = definitions[i].Workload
	}
	corpus := Corpus{SchemaVersion: ExpandedCorpusSchemaVersion, EvidenceClass: EvidenceClass, Workloads: workloads}
	if validateCorpus(corpus) != nil {
		return Corpus{}, ErrInvalid
	}
	return corpus, nil
}

func definitionsForCorpusSchema(schema string) ([]Definition, error) {
	switch schema {
	case CorpusSchemaVersion:
		return PilotDefinitions()
	case ExpandedCorpusSchemaVersion:
		return ExpandedDefinitions()
	default:
		return nil, ErrInvalid
	}
}

func PilotCorpus() (Corpus, error) {
	definitions, err := PilotDefinitions()
	if err != nil {
		return Corpus{}, err
	}
	workloads := make([]Workload, len(definitions))
	for i := range definitions {
		workloads[i] = definitions[i].Workload
	}
	corpus := Corpus{SchemaVersion: CorpusSchemaVersion, EvidenceClass: EvidenceClass, Workloads: workloads}
	if validateCorpus(corpus) != nil {
		return Corpus{}, ErrInvalid
	}
	return corpus, nil
}

func VerifyResult(definition Definition, raw json.RawMessage, calls uint32) error {
	if err := validateDefinition(definition); err != nil {
		return err
	}
	canonical, err := canonicalJSON(raw)
	if err != nil || !json.Valid(definition.Expected) || !bytes.Equal(canonical, definition.Expected) || calls != definition.Workload.ExpectedCapabilityCalls {
		return ErrInvalid
	}
	d := sha256.Sum256(canonical)
	if fmt.Sprintf("sha256:%x", d) != definition.Workload.ExpectedResultSHA256 {
		return ErrInvalid
	}
	return nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		return nil, ErrInvalid
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	nodes := 0
	if err := consumeUniqueJSON(decoder, 0, &nodes); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func consumeUniqueJSON(decoder *json.Decoder, depth int, nodes *int) error {
	if depth > 64 || *nodes >= 4096 {
		return ErrInvalid
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
		seen := map[string]struct{}{}
		for decoder.More() {
			token, err := decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok {
				return ErrInvalid
			}
			if _, exists := seen[key]; exists {
				return ErrInvalid
			}
			seen[key] = struct{}{}
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for decoder.More() {
			if err := consumeUniqueJSON(decoder, depth+1, nodes); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func validateDefinition(definition Definition) error {
	definitions, err := ExpandedDefinitions()
	if err != nil {
		return ErrInvalid
	}
	for _, candidate := range definitions {
		if candidate.Workload.ID == definition.Workload.ID && reflect.DeepEqual(candidate, definition) {
			return nil
		}
	}
	return ErrInvalid
}
