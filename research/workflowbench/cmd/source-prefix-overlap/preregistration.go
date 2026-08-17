package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

const sourcePrefixOracleSchema = "pysolate.source-prefix-oracle.v1"
const sourcePrefixLaneConfigSchema = "pysolate.source-prefix-lane-config.v1"

type sourcePrefixOracle struct {
	SchemaVersion        string          `json:"schema_version"`
	ExpectedResult       json.RawMessage `json:"expected_result"`
	LogicalCalls         uint32          `json:"logical_calls"`
	PhysicalDispatches   uint32          `json:"physical_dispatches"`
	WorkspaceDisposition string          `json:"workspace_disposition"`
	ExternalWrites       uint32          `json:"external_writes"`
}

type sourcePrefixLaneConfig struct {
	SchemaVersion  string `json:"schema_version"`
	Mechanism      string `json:"mechanism"`
	QueueMaxChunks uint32 `json:"queue_max_chunks"`
	QueueMaxBytes  uint32 `json:"queue_max_bytes"`
	Clock          string `json:"clock"`
	Baseline       string `json:"baseline"`
	Treatment      string `json:"treatment"`
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func canonicalResultSHA(raw json.RawMessage) (string, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", errors.New("invalid oracle result")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digestBytes(canonical), nil
}

func loadPreregistration(contractPath, oraclePath, lanePath string) (workflowbench.SourcePrefixExperimentContract, sourcePrefixOracle, sourcePrefixLaneConfig, error) {
	var oracle sourcePrefixOracle
	var lane sourcePrefixLaneConfig
	contractRaw, err := os.ReadFile(contractPath)
	if err != nil {
		return workflowbench.SourcePrefixExperimentContract{}, oracle, lane, err
	}
	oracleRaw, err := os.ReadFile(oraclePath)
	if err != nil {
		return workflowbench.SourcePrefixExperimentContract{}, oracle, lane, err
	}
	laneRaw, err := os.ReadFile(lanePath)
	if err != nil {
		return workflowbench.SourcePrefixExperimentContract{}, oracle, lane, err
	}
	contract, err := workflowbench.DecodeSourcePrefixExperimentContract(contractRaw)
	if err != nil || decodeStrictJSON(oracleRaw, &oracle) != nil || decodeStrictJSON(laneRaw, &lane) != nil {
		return workflowbench.SourcePrefixExperimentContract{}, sourcePrefixOracle{}, sourcePrefixLaneConfig{}, errors.New("invalid source-prefix preregistration")
	}
	expectedSHA, expectedErr := canonicalResultSHA(oracle.ExpectedResult)
	if expectedErr != nil || contract.OracleSHA256 != digestBytes(oracleRaw) || contract.LaneConfigSHA256 != digestBytes(laneRaw) || contract.ExpectedResultSHA256 != expectedSHA {
		return workflowbench.SourcePrefixExperimentContract{}, sourcePrefixOracle{}, sourcePrefixLaneConfig{}, errors.New("source-prefix preregistration identity mismatch")
	}
	if oracle.SchemaVersion != sourcePrefixOracleSchema || oracle.LogicalCalls != 1 || oracle.PhysicalDispatches != 1 || oracle.WorkspaceDisposition != "published" || oracle.ExternalWrites != 0 {
		return workflowbench.SourcePrefixExperimentContract{}, sourcePrefixOracle{}, sourcePrefixLaneConfig{}, errors.New("invalid source-prefix oracle")
	}
	if lane.SchemaVersion != sourcePrefixLaneConfigSchema || lane.Mechanism != "reach_gated_source_prefix" || lane.Clock != "monotonic_host" || lane.Baseline != "release_after_generation_complete" || lane.Treatment != "release_at_frozen_offsets" || lane.QueueMaxChunks != contract.Schedule.MaxBufferedChunks || lane.QueueMaxBytes != contract.Schedule.MaxBufferedBytes {
		return workflowbench.SourcePrefixExperimentContract{}, sourcePrefixOracle{}, sourcePrefixLaneConfig{}, errors.New("invalid source-prefix lane config")
	}
	return contract, oracle, lane, nil
}

func stableStreamResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(payload, &response) != nil || response.Status != "ok" || len(response.Result) == 0 || (len(response.Error) != 0 && string(response.Error) != "null") {
		return nil, errors.New("Guest response is not publishable")
	}
	var stream struct {
		Result        json.RawMessage `json:"result"`
		ResultPresent bool            `json:"result_present"`
		ResultSource  string          `json:"result_source"`
	}
	if json.Unmarshal(response.Result, &stream) != nil || !stream.ResultPresent || stream.ResultSource != "legacy_result" || len(stream.Result) == 0 || string(stream.Result) == "null" {
		return nil, errors.New("stream terminal result is missing")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(stream.Result))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("stream result is not canonical JSON")
	}
	return json.Marshal(value)
}

func atomicPrivateWrite(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".source-prefix-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func decodeStrictJSON(raw []byte, target any) error {
	if rejectDuplicateObjectKeys(raw) != nil {
		return errors.New("duplicate JSON key")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("invalid strict JSON")
	}
	return nil
}

func rejectDuplicateObjectKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate JSON key")
				}
				seen[key] = true
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("invalid JSON delimiter")
		}
	}
	if err := visit(); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}
