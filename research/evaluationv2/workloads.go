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
	definitions, err := PilotDefinitions()
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
