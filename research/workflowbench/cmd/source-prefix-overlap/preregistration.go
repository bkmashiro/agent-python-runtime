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

const fixedSourcePrefixContractSHA256 = "sha256:dab34bfa2a6ea8dce909c375c0b963569cfc67f988fa1adae56de561b1b009ff"
const fixedSourcePrefixOracleSHA256 = "sha256:6d82769ce151d64df3f040cfa191c677abbd2c9f9016a6856cfbbcf01f97b7f5"
const fixedSourcePrefixLaneConfigSHA256 = "sha256:c233da70e9d0636d684108cbea8d15a423abf965fc0027dce9840ba0f2d00e42"
const dayTripSourcePrefixContractSHA256 = "sha256:941467e08e2c0d4dd7351823113b6fa780d68895d8660c709b80575c37094dcd"
const dayTripSourcePrefixOracleSHA256 = "sha256:602027bd21be45470d9fca66c94b23ea5d4cc1a00ddf4bd889518eec9e3b4324"

type preregistrationAnchors struct {
	contractSHA256 string
	oracleSHA256   string
	laneSHA256     string
}

var fixedSourcePrefixAnchors = preregistrationAnchors{
	contractSHA256: fixedSourcePrefixContractSHA256,
	oracleSHA256:   fixedSourcePrefixOracleSHA256,
	laneSHA256:     fixedSourcePrefixLaneConfigSHA256,
}

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
	contractRaw, contractErr := os.ReadFile(contractPath)
	oracleRaw, oracleErr := os.ReadFile(oraclePath)
	laneRaw, laneErr := os.ReadFile(lanePath)
	if contractErr != nil || oracleErr != nil || laneErr != nil {
		return workflowbench.SourcePrefixExperimentContract{}, sourcePrefixOracle{}, sourcePrefixLaneConfig{}, errors.New("read source-prefix preregistration")
	}
	anchors := fixedSourcePrefixAnchors
	if digestBytes(contractRaw) == dayTripSourcePrefixContractSHA256 && digestBytes(oracleRaw) == dayTripSourcePrefixOracleSHA256 && digestBytes(laneRaw) == fixedSourcePrefixLaneConfigSHA256 {
		anchors = preregistrationAnchors{contractSHA256: dayTripSourcePrefixContractSHA256, oracleSHA256: dayTripSourcePrefixOracleSHA256, laneSHA256: fixedSourcePrefixLaneConfigSHA256}
	}
	return loadPreregistrationWithAnchors(contractPath, oraclePath, lanePath, anchors)
}

func loadPreregistrationWithAnchors(contractPath, oraclePath, lanePath string, anchors preregistrationAnchors) (workflowbench.SourcePrefixExperimentContract, sourcePrefixOracle, sourcePrefixLaneConfig, error) {
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
	if digestBytes(contractRaw) != anchors.contractSHA256 || digestBytes(oracleRaw) != anchors.oracleSHA256 || digestBytes(laneRaw) != anchors.laneSHA256 {
		return workflowbench.SourcePrefixExperimentContract{}, oracle, lane, errors.New("source-prefix preregistration does not match fixed anchors")
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
