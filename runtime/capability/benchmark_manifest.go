package capability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const (
	benchmarkManifestCapability      = "sources.benchmark_manifest"
	benchmarkManifestVersion         = "pysolate.sources.benchmark-manifest.v1"
	benchmarkManifestHandlerIdentity = "pysolate.sources.benchmark-manifest-http.v1"
)

// BenchmarkManifestPolicy is Host-owned exact-endpoint policy for the
// benchmark_manifest source. No field is projected into Agent Python.
type BenchmarkManifestPolicy struct {
	Endpoint         string
	Timeout          time.Duration
	MaxResponseBytes uint32
}

// benchmarkManifestOutputSchema is deliberately concrete rather than a
// generic JSON pass-through. Semantic ID uniqueness and bound ordering are
// additionally checked by validateBenchmarkManifest after schema validation.
var benchmarkManifestOutputSchema = json.RawMessage(`{
  "type":"object",
  "properties":{
    "schema_version":{"const":"pysolate.benchmark-manifest.v1"},
    "suite":{
      "type":"object",
      "properties":{
        "id":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"},
        "version":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9][A-Za-z0-9._+-]*$"},
        "title":{"type":"string","minLength":1,"maxLength":256},
        "categories":{"type":"array","maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"}}
      },
      "required":["id","version","title"],
      "additionalProperties":false
    },
    "cases":{
      "type":"array","minItems":1,"maxItems":128,
      "items":{
        "type":"object",
        "properties":{
          "id":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"},
          "title":{"type":"string","minLength":1,"maxLength":256},
          "task_class":{"enum":["code_generation","reasoning","retrieval","workspace_transform"]},
          "input_artifacts":{
            "type":"array","minItems":1,"maxItems":32,
            "items":{
              "type":"object",
              "properties":{
                "id":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"},
                "kind":{"enum":["code","dataset","prompt","workspace_tree"]},
                "sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},
                "media_type":{"type":"string","minLength":3,"maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*/[A-Za-z0-9][A-Za-z0-9!#$&^_.+-]*$"},
                "bytes":{"type":"integer","minimum":0,"maximum":1099511627776}
              },
              "required":["id","kind","sha256","media_type","bytes"],
              "additionalProperties":false
            }
          },
          "metrics":{
            "type":"array","minItems":1,"maxItems":32,
            "items":{
              "type":"object",
              "properties":{
                "id":{"type":"string","minLength":1,"maxLength":128,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"},
                "title":{"type":"string","minLength":1,"maxLength":256},
                "direction":{"enum":["maximize","minimize"]},
                "unit":{"enum":["bytes","count","milliseconds","ratio","score","seconds"]},
                "bounds":{
                  "type":"object",
                  "properties":{
                    "minimum":{"type":"number","minimum":-1000000000000,"maximum":1000000000000},
                    "maximum":{"type":"number","minimum":-1000000000000,"maximum":1000000000000}
                  },
                  "required":["minimum","maximum"],
                  "additionalProperties":false
                }
              },
              "required":["id","title","direction","unit","bounds"],
              "additionalProperties":false
            }
          },
          "tags":{"type":"array","maxItems":32,"uniqueItems":true,"items":{"type":"string","minLength":1,"maxLength":64,"pattern":"^[A-Za-z0-9][A-Za-z0-9._-]*$"}}
        },
        "required":["id","title","task_class","input_artifacts","metrics"],
        "additionalProperties":false
      }
    }
  },
  "required":["schema_version","suite","cases"],
  "additionalProperties":false
}`)

func BenchmarkManifestDefinition(policy BenchmarkManifestPolicy) (Spec, Grant, error) {
	if err := validateBenchmarkManifestPolicy(policy); err != nil {
		return Spec{}, Grant{}, err
	}
	policyDocument, err := json.Marshal(exactJSONSourcePolicyDocument{
		SchemaVersion: "pysolate.source-policy.v1", Capability: benchmarkManifestCapability,
		Endpoint: policy.Endpoint, Method: http.MethodGet, Redirects: "deny",
		ExpectedStatus: http.StatusOK, ExpectedMediaType: "application/json",
		TimeoutMS: policy.Timeout.Milliseconds(), MaxResponseBytes: policy.MaxResponseBytes,
	})
	if err != nil {
		return Spec{}, Grant{}, ErrInvalidGrant
	}
	grant, err := NewGrant(policyDocument)
	if err != nil {
		return Spec{}, Grant{}, err
	}
	return Spec{
		Name: benchmarkManifestCapability, Version: benchmarkManifestVersion,
		Description:     "Read the Host-approved versioned benchmark suite manifest.",
		EffectClass:     EffectExternalRead,
		Playback:        PlaybackCaptured,
		HandlerIdentity: benchmarkManifestHandlerIdentity,
		InputSchema:     json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema:    append(json.RawMessage(nil), benchmarkManifestOutputSchema...),
		Python:          &PythonProjection{Module: "sources", Method: "benchmark_manifest", Arguments: []string{}},
	}, grant, nil
}

func RegisterBenchmarkManifest(registry *Registry, policy BenchmarkManifestPolicy) error {
	if registry == nil {
		return ErrInvalidTool
	}
	spec, grant, err := BenchmarkManifestDefinition(policy)
	if err != nil {
		return err
	}
	handler, err := newBenchmarkManifestHandler(policy)
	if err != nil {
		return err
	}
	return registry.Register(spec, grant, handler)
}

// RegisterBenchmarkManifestPlayback installs the exact same sealed definition
// with a handler that cannot perform network I/O. Broker playback intercepts
// calls and still applies both schema and semantic validation to recorded data.
func RegisterBenchmarkManifestPlayback(registry *Registry, policy BenchmarkManifestPolicy) error {
	if registry == nil {
		return ErrInvalidTool
	}
	spec, grant, err := BenchmarkManifestDefinition(policy)
	if err != nil {
		return err
	}
	return registry.Register(spec, grant, NewPlaybackHandler())
}

func validateBenchmarkManifestPolicy(policy BenchmarkManifestPolicy) error {
	if err := validateExactJSONSourcePolicy(exactJSONSourcePolicy(policy)); err != nil {
		return errors.New("invalid benchmark manifest policy")
	}
	return nil
}

type benchmarkManifestHandler struct {
	source *exactJSONSourceAdapter
}

func newBenchmarkManifestHandler(policy BenchmarkManifestPolicy) (Handler, error) {
	if err := validateBenchmarkManifestPolicy(policy); err != nil {
		return nil, err
	}
	source, err := newExactJSONSourceAdapter(exactJSONSourcePolicy(policy))
	if err != nil {
		return nil, err
	}
	return &benchmarkManifestHandler{source: source}, nil
}

func (handler *benchmarkManifestHandler) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := handler.CallWithEvidence(ctx, arguments)
	return result, err
}

func (handler *benchmarkManifestHandler) CallWithEvidence(ctx context.Context, _ json.RawMessage) (json.RawMessage, TransportEvidence, error) {
	if handler == nil || handler.source == nil {
		return nil, TransportEvidence{}, errors.New("invalid benchmark manifest call")
	}
	return handler.source.read(ctx, "benchmark manifest")
}

type benchmarkManifestDocument struct {
	SchemaVersion string `json:"schema_version"`
	Cases         []struct {
		ID             string `json:"id"`
		InputArtifacts []struct {
			ID string `json:"id"`
		} `json:"input_artifacts"`
		Metrics []struct {
			ID     string `json:"id"`
			Unit   string `json:"unit"`
			Bounds struct {
				Minimum float64 `json:"minimum"`
				Maximum float64 `json:"maximum"`
			} `json:"bounds"`
		} `json:"metrics"`
	} `json:"cases"`
}

func validateSpecResultSemantics(spec Spec, canonical json.RawMessage) error {
	if spec.Name == benchmarkManifestCapability && spec.Version == benchmarkManifestVersion && spec.HandlerIdentity == benchmarkManifestHandlerIdentity {
		return validateBenchmarkManifest(canonical)
	}
	return nil
}

func validateBenchmarkManifest(canonical json.RawMessage) error {
	var manifest benchmarkManifestDocument
	if err := json.Unmarshal(canonical, &manifest); err != nil || manifest.SchemaVersion != "pysolate.benchmark-manifest.v1" {
		return errors.New("invalid benchmark manifest")
	}
	caseIDs := make(map[string]struct{}, len(manifest.Cases))
	for _, benchmarkCase := range manifest.Cases {
		// Case IDs are suite-wide. Artifact and metric IDs are case-local so
		// separate cases may intentionally measure the same named metric or
		// refer to equivalently named inputs without losing stable coordinates.
		if _, duplicate := caseIDs[benchmarkCase.ID]; duplicate {
			return errors.New("duplicate benchmark case ID")
		}
		caseIDs[benchmarkCase.ID] = struct{}{}
		artifactIDs := make(map[string]struct{}, len(benchmarkCase.InputArtifacts))
		for _, artifact := range benchmarkCase.InputArtifacts {
			if _, duplicate := artifactIDs[artifact.ID]; duplicate {
				return errors.New("duplicate benchmark artifact ID")
			}
			artifactIDs[artifact.ID] = struct{}{}
		}
		metricIDs := make(map[string]struct{}, len(benchmarkCase.Metrics))
		for _, metric := range benchmarkCase.Metrics {
			if _, duplicate := metricIDs[metric.ID]; duplicate {
				return errors.New("duplicate benchmark metric ID")
			}
			metricIDs[metric.ID] = struct{}{}
			if metric.Bounds.Minimum > metric.Bounds.Maximum ||
				(metric.Unit == "ratio" && (metric.Bounds.Minimum < 0 || metric.Bounds.Maximum > 1)) {
				return errors.New("invalid benchmark metric bounds")
			}
		}
	}
	return nil
}
