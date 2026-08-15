package main

import (
	"encoding/json"
	"errors"
	"os"
)

const evidenceBundleSchema = "pysolate.effectgraph-census-bundle.v1"

type evidenceBundle struct {
	SchemaVersion             string `json:"schema_version"`
	CorpusFileSHA256          string `json:"corpus_file_sha256"`
	EffectReportFileSHA256    string `json:"effect_report_file_sha256"`
	RegionReportFileSHA256    string `json:"region_report_file_sha256"`
	PlacementReportFileSHA256 string `json:"placement_report_file_sha256"`
	CorpusIdentitySHA256      string `json:"corpus_identity_sha256"`
	ArtifactSHA256            string `json:"artifact_sha256"`
	ArtifactSourceCommit      string `json:"artifact_source_commit"`
}

func encodeEvidenceBundle(corpus, effectReport, regionReport, placementReport []byte, corpusIdentity, artifactSHA, artifactSourceCommit string) ([]byte, error) {
	bundle := evidenceBundle{
		SchemaVersion:    evidenceBundleSchema,
		CorpusFileSHA256: digest(corpus), EffectReportFileSHA256: digest(effectReport), RegionReportFileSHA256: digest(regionReport), PlacementReportFileSHA256: digest(placementReport),
		CorpusIdentitySHA256: corpusIdentity, ArtifactSHA256: artifactSHA, ArtifactSourceCommit: artifactSourceCommit,
	}
	if !validEvidenceBundle(bundle) {
		return nil, errors.New("invalid evidence bundle")
	}
	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func validEvidenceBundle(bundle evidenceBundle) bool {
	return bundle.SchemaVersion == evidenceBundleSchema &&
		validSHA256(bundle.CorpusFileSHA256) && validSHA256(bundle.EffectReportFileSHA256) &&
		validSHA256(bundle.RegionReportFileSHA256) && validSHA256(bundle.PlacementReportFileSHA256) && validSHA256(bundle.CorpusIdentitySHA256) &&
		validSHA256(bundle.ArtifactSHA256) && len(bundle.ArtifactSourceCommit) == 40 && isLowerHex(bundle.ArtifactSourceCommit)
}

func verifyEvidenceBundle(bundleJSON, corpus, effectReport, regionReport, placementReport []byte) error {
	var bundle evidenceBundle
	if json.Unmarshal(bundleJSON, &bundle) != nil || !validEvidenceBundle(bundle) ||
		bundle.CorpusFileSHA256 != digest(corpus) || bundle.EffectReportFileSHA256 != digest(effectReport) ||
		bundle.RegionReportFileSHA256 != digest(regionReport) || bundle.PlacementReportFileSHA256 != digest(placementReport) {
		return errors.New("evidence bundle mismatch")
	}
	return nil
}

func writeEvidenceBundle(bundlePath, corpusPath, effectReportPath, regionReportPath, placementReportPath string, corpus, effectReport, regionReport, placementReport []byte, corpusIdentity, artifactSHA, artifactSourceCommit string) error {
	bundle, err := encodeEvidenceBundle(corpus, effectReport, regionReport, placementReport, corpusIdentity, artifactSHA, artifactSourceCommit)
	if err != nil {
		return err
	}
	// The generation marker is replaced last. A crash or write failure may leave data
	// files from different generations, but the old/missing marker then fails validation.
	for _, output := range []struct {
		path    string
		content []byte
	}{{corpusPath, corpus}, {effectReportPath, effectReport}, {regionReportPath, regionReport}, {placementReportPath, placementReport}, {bundlePath, bundle}} {
		if err := writeAtomic(output.path, output.content); err != nil {
			return err
		}
	}
	return nil
}

func verifyEvidenceBundleFiles(bundlePath, corpusPath, effectReportPath, regionReportPath, placementReportPath string) error {
	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	corpus, err := os.ReadFile(corpusPath)
	if err != nil {
		return err
	}
	effectReport, err := os.ReadFile(effectReportPath)
	if err != nil {
		return err
	}
	regionReport, err := os.ReadFile(regionReportPath)
	if err != nil {
		return err
	}
	placementReport, err := os.ReadFile(placementReportPath)
	if err != nil {
		return err
	}
	return verifyEvidenceBundle(bundle, corpus, effectReport, regionReport, placementReport)
}

func validSHA256(value string) bool {
	return len(value) == 71 && value[:7] == "sha256:" && isLowerHex(value[7:])
}

func isLowerHex(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
