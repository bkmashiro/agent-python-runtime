package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	runtimeevidence "github.com/bkmashiro/agent-python-runtime/runtime/evidence"
)

type phase7CellCampaignEntry struct {
	SampleIndex      uint32 `json:"sample_index"`
	FragmentFilename string `json:"fragment_filename"`
	FragmentSHA256   string `json:"fragment_sha256"`
	ArchiveFilename  string `json:"archive_filename"`
	ArchiveSHA256    string `json:"archive_sha256"`
	READYFilename    string `json:"ready_filename"`
	ACKEDFilename    string `json:"acked_filename"`
	SlurmFilename    string `json:"slurm_filename"`
	JobID            string `json:"job_id"`
	ArrayJobID       string `json:"array_job_id"`
	ArrayTaskID      uint32 `json:"array_task_id"`
	CgroupSHA256     string `json:"cgroup_sha256"`
}

type phase7CellSlurmReceipt struct {
	JobID            string `json:"job_id"`
	ArrayJobID       string `json:"array_job_id"`
	ArrayTaskID      uint32 `json:"array_task_id"`
	State            string `json:"state"`
	ExitCode         string `json:"exit_code"`
	Partition        string `json:"partition"`
	CPUs             uint32 `json:"cpus"`
	MemoryPerNodeMiB uint64 `json:"memory_per_node_mib"`
	GPUType          string `json:"gpu_type"`
	GPUs             uint32 `json:"gpus"`
	Restarts         uint32 `json:"restarts"`
	SnapshotFilename string `json:"snapshot_filename"`
	SnapshotSHA256   string `json:"snapshot_sha256"`
}

type phase7CellCampaignManifest struct {
	SchemaVersion int                       `json:"schema_version"`
	EvidenceClass string                    `json:"evidence_class"`
	ArmOrder      string                    `json:"arm_order"`
	Repeats       uint32                    `json:"repeats"`
	Entries       []phase7CellCampaignEntry `json:"entries"`
}

func runPhase7CellAggregationMain(options benchmarkOptions) error {
	if options.InputPath == "" || options.SchemaPath == "" || options.ArtifactPath == "" || options.ManifestPath == "" || options.OutputPath == "" ||
		options.Class != "profile-candidate" || options.PreparedWarmupProfile != "numpy-ready-v1" || options.LifecycleDensityChild ||
		(options.Strategy != "cow-ready-single-use" && options.Strategy != "single-use-preinitialized") || (options.Samples != 1 && options.Samples != 3) {
		return errors.New("aggregate-phase7-density-cells requires canonical input/schema/artifact/manifest/output/strategy/repeats")
	}
	manifestBytes, err := readRegularFileBounded(options.InputPath, 1<<20)
	if err != nil {
		return fmt.Errorf("read Phase 7 campaign manifest: %w", err)
	}
	campaign, err := decodePhase7CellCampaignManifest(manifestBytes)
	if err != nil {
		return err
	}
	if campaign.Repeats != uint32(options.Samples) {
		return errors.New("Phase 7 campaign repeat identity drifted")
	}
	schemaBytes, err := readRegularFileBounded(options.SchemaPath, maximumLifecycleDensitySchemaBytes)
	if err != nil {
		return fmt.Errorf("read Phase 7 cell schema: %w", err)
	}
	base := filepath.Dir(options.InputPath)
	fragments := make([]phase7PairedCellFragment, 0, len(campaign.Entries))
	for index, entry := range campaign.Entries {
		if err := validatePhase7CampaignEntryIdentity(entry, index); err != nil {
			return err
		}
		archiveBytes, err := readPhase7CampaignFile(base, entry.ArchiveFilename, 2<<20)
		if err != nil {
			return err
		}
		archiveDigest := sha256.Sum256(archiveBytes)
		if hex.EncodeToString(archiveDigest[:]) != entry.ArchiveSHA256 || validatePhase7CellArchive(archiveBytes, entry.FragmentSHA256) != nil {
			return errors.New("Phase 7 campaign archive receipt is invalid")
		}
		readyBytes, err := readPhase7CampaignFile(base, entry.READYFilename, 256)
		if err != nil || string(readyBytes) != entry.ArchiveSHA256+"  "+entry.ArchiveFilename+"\n" {
			return errors.New("Phase 7 campaign READY receipt is invalid")
		}
		ackedBytes, err := readPhase7CampaignFile(base, entry.ACKEDFilename, 256)
		if err != nil || validatePhase7ACKEDReceipt(ackedBytes, entry.ArchiveSHA256) != nil {
			return errors.New("Phase 7 campaign ACKED receipt is invalid")
		}
		slurmBytes, err := readPhase7CampaignFile(base, entry.SlurmFilename, 4096)
		if err != nil {
			return err
		}
		slurm, err := decodePhase7SlurmReceipt(slurmBytes)
		if err != nil || slurm.JobID != entry.JobID || slurm.ArrayJobID != entry.ArrayJobID || slurm.ArrayTaskID != entry.ArrayTaskID ||
			slurm.State != "COMPLETED" || slurm.ExitCode != "0:0" || slurm.Partition != "t4" || slurm.CPUs != 4 ||
			slurm.MemoryPerNodeMiB != 16384 || slurm.GPUType != "tesla_t4" || slurm.GPUs != 1 || slurm.Restarts != 0 ||
			slurm.SnapshotFilename != fmt.Sprintf("slurm-%02d.scontrol.txt", index) || !lowerHexString(slurm.SnapshotSHA256, 64) {
			return errors.New("Phase 7 campaign Slurm receipt is invalid")
		}
		snapshotBytes, err := readPhase7CampaignFile(base, slurm.SnapshotFilename, 16384)
		if err != nil {
			return err
		}
		snapshotDigest := sha256.Sum256(snapshotBytes)
		if hex.EncodeToString(snapshotDigest[:]) != slurm.SnapshotSHA256 || validatePhase7SlurmSnapshot(snapshotBytes, slurm) != nil {
			return errors.New("Phase 7 campaign raw Slurm snapshot is invalid")
		}
		path := filepath.Join(base, entry.FragmentFilename)
		if filepath.Base(path) != entry.FragmentFilename {
			return errors.New("Phase 7 campaign fragment path is unsafe")
		}
		document, err := readRegularFileBounded(path, maximumLifecycleDensityEvidenceBytes)
		if err != nil {
			return fmt.Errorf("read Phase 7 fragment %d: %w", index, err)
		}
		digest := sha256.Sum256(document)
		if hex.EncodeToString(digest[:]) != entry.FragmentSHA256 {
			return errors.New("Phase 7 campaign fragment digest drifted")
		}
		if err := validateJSONSchemaDocument(document, schemaBytes, "phase7-paired-density-cell"); err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(document))
		decoder.DisallowUnknownFields()
		var fragment phase7PairedCellFragment
		if err := decoder.Decode(&fragment); err != nil {
			return fmt.Errorf("decode Phase 7 fragment %d: %w", index, err)
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return errors.New("Phase 7 fragment has trailing JSON")
		}
		if err := validatePhase7PairedCellFragment(fragment); err != nil {
			return err
		}
		if fragment.Allocation.JobID != entry.JobID || fragment.Allocation.ArrayJobID != entry.ArrayJobID || fragment.Allocation.ArrayTaskID != entry.ArrayTaskID || fragment.Allocation.CgroupPathSHA256 != entry.CgroupSHA256 || fragment.Allocation.ArmOrder != campaign.ArmOrder {
			return errors.New("Phase 7 fragment does not match its accepted scheduler receipt")
		}
		fragments = append(fragments, fragment)
	}
	evidence, encoded, err := aggregatePhase7PairedCellFragments(fragments, options.Strategy, campaign.Repeats)
	if err != nil {
		return err
	}
	artifact, artifactBytes, err := loadArtifactIdentity(options.ArtifactPath, options.ManifestPath)
	if err != nil {
		return err
	}
	if err := evidence.ValidateArtifactBytes(artifactBytes); err != nil {
		return err
	}
	expectedArtifact := runtimeArtifactIdentityFromArtifact(artifact)
	if !reflect.DeepEqual(evidence.Artifact, expectedArtifact) {
		return errors.New("aggregated Phase 7 artifact manifest identity drifted")
	}
	if err := writeAtomic(options.OutputPath, encoded); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(os.Stdout, "{\"output\":%q,\"arm_order\":%q,\"strategy\":%q,\"cells\":%d}\n", options.OutputPath, campaign.ArmOrder, options.Strategy, len(campaign.Entries))
	return nil
}

func validatePhase7CampaignEntryIdentity(entry phase7CellCampaignEntry, index int) error {
	expectedTag := fmt.Sprintf("%s_%d", entry.ArrayJobID, index)
	if entry.SampleIndex != uint32(index) || entry.ArrayTaskID != uint32(index) || entry.FragmentFilename != fmt.Sprintf("cell-%02d.json", index) ||
		entry.ArchiveFilename != fmt.Sprintf("cell-%d-result-%s.tar.gz", index, expectedTag) || entry.READYFilename != "READY-"+expectedTag ||
		entry.ACKEDFilename != "ACKED-"+expectedTag || entry.SlurmFilename != fmt.Sprintf("slurm-%02d.json", index) ||
		!canonicalPhase7SlurmDecimal(entry.JobID) || !canonicalPhase7SlurmDecimal(entry.ArrayJobID) ||
		!lowerHexString(entry.FragmentSHA256, 64) || !lowerHexString(entry.ArchiveSHA256, 64) || !lowerHexString(entry.CgroupSHA256, 64) {
		return errors.New("Phase 7 campaign entry receipt is invalid")
	}
	return nil
}

func decodePhase7CellCampaignManifest(document []byte) (phase7CellCampaignManifest, error) {
	if err := rejectDuplicateJSONDocument(document); err != nil {
		return phase7CellCampaignManifest{}, fmt.Errorf("Phase 7 campaign manifest JSON is invalid: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var manifest phase7CellCampaignManifest
	if err := decoder.Decode(&manifest); err != nil {
		return phase7CellCampaignManifest{}, fmt.Errorf("decode Phase 7 campaign manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return phase7CellCampaignManifest{}, errors.New("Phase 7 campaign manifest has trailing JSON")
	}
	if manifest.SchemaVersion != 1 || manifest.EvidenceClass != "phase7-cell-campaign" || (manifest.ArmOrder != "cow-first" && manifest.ArmOrder != "non-cow-first") || (manifest.Repeats != 1 && manifest.Repeats != 3) || len(manifest.Entries) != int(7*manifest.Repeats) {
		return phase7CellCampaignManifest{}, errors.New("Phase 7 campaign manifest identity or coverage is invalid")
	}
	return manifest, nil
}

func readPhase7CampaignFile(base, filename string, maximum int64) ([]byte, error) {
	if filepath.Base(filename) != filename || filename == "." {
		return nil, errors.New("Phase 7 campaign receipt path is unsafe")
	}
	document, err := readRegularFileBounded(filepath.Join(base, filename), maximum)
	if err != nil {
		return nil, fmt.Errorf("read Phase 7 campaign receipt %s: %w", filename, err)
	}
	return document, nil
}

func validatePhase7CellArchive(archiveBytes []byte, fragmentSHA256 string) error {
	compressed, err := gzip.NewReader(bytes.NewReader(archiveBytes))
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	expected := map[string]byte{
		"ENVIRONMENT.txt": tar.TypeReg, "RUN_COMPLETE": tar.TypeReg, "result": tar.TypeDir,
		"result/SHA256SUMS": tar.TypeReg, "result/cell.json": tar.TypeReg, "result/cell.json.validation.json": tar.TypeReg,
	}
	seen := make(map[string]struct{}, len(expected))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		typeFlag, ok := expected[name]
		if !ok || header.Typeflag != typeFlag || header.Uid != 0 || header.Gid != 0 || header.ModTime.Unix() != 0 ||
			strings.HasPrefix(name, "/") || strings.Contains(name, "..") || header.Linkname != "" {
			return errors.New("Phase 7 cell archive topology is unsafe")
		}
		if _, duplicate := seen[name]; duplicate {
			return errors.New("Phase 7 cell archive member was duplicated")
		}
		seen[name] = struct{}{}
		if header.Typeflag == tar.TypeReg {
			if header.Size < 0 || header.Size > maximumLifecycleDensityEvidenceBytes {
				return errors.New("Phase 7 cell archive member exceeds its bound")
			}
			body, err := io.ReadAll(io.LimitReader(reader, header.Size+1))
			if err != nil || int64(len(body)) != header.Size {
				return errors.New("Phase 7 cell archive member is truncated")
			}
			if name == "result/cell.json" {
				digest := sha256.Sum256(body)
				if hex.EncodeToString(digest[:]) != fragmentSHA256 {
					return errors.New("Phase 7 cell archive does not bind the accepted fragment")
				}
			}
		}
	}
	if len(seen) != len(expected) {
		return errors.New("Phase 7 cell archive is incomplete")
	}
	return nil
}

func validatePhase7ACKEDReceipt(document []byte, archiveSHA256 string) error {
	parts := strings.Split(strings.TrimSuffix(string(document), "\n"), "  ")
	if len(parts) != 2 || parts[0] != archiveSHA256 {
		return errors.New("Phase 7 ACKED digest is invalid")
	}
	if _, err := time.Parse(time.RFC3339, parts[1]); err != nil {
		return errors.New("Phase 7 ACKED timestamp is invalid")
	}
	return nil
}

func canonicalPhase7SlurmDecimal(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func validatePhase7SlurmSnapshot(document []byte, receipt phase7CellSlurmReceipt) error {
	if len(document) == 0 || !bytes.HasSuffix(document, []byte("\n")) || bytes.Count(document, []byte("\n")) != 1 {
		return errors.New("Phase 7 Slurm snapshot is not one complete line")
	}
	values := make(map[string]string)
	for _, token := range strings.Fields(string(document)) {
		parts := strings.SplitN(token, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return errors.New("Phase 7 Slurm snapshot token is invalid")
		}
		if _, duplicate := values[parts[0]]; duplicate {
			return errors.New("Phase 7 Slurm snapshot field is duplicated")
		}
		values[parts[0]] = parts[1]
	}
	required := map[string]string{
		"JobId": receipt.JobID, "ArrayJobId": receipt.ArrayJobID, "ArrayTaskId": strconv.FormatUint(uint64(receipt.ArrayTaskID), 10),
		"JobState": "COMPLETED", "ExitCode": "0:0", "Partition": "t4", "NumCPUs": "4", "NumNodes": "1",
		"MinMemoryNode": "16G", "Restarts": "0", "TresPerNode": "gres/gpu:tesla_t4:1",
	}
	for key, expected := range required {
		if values[key] != expected {
			return fmt.Errorf("Phase 7 Slurm snapshot %s drifted", key)
		}
	}
	return nil
}

func decodePhase7SlurmReceipt(document []byte) (phase7CellSlurmReceipt, error) {
	if err := rejectDuplicateJSONDocument(document); err != nil {
		return phase7CellSlurmReceipt{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	var receipt phase7CellSlurmReceipt
	if err := decoder.Decode(&receipt); err != nil {
		return phase7CellSlurmReceipt{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return phase7CellSlurmReceipt{}, errors.New("Phase 7 Slurm receipt has trailing JSON")
	}
	return receipt, nil
}

func runtimeArtifactIdentityFromArtifact(artifact artifactIdentity) runtimeevidence.ArtifactIdentity {
	return runtimeevidence.ArtifactIdentity{Filename: artifact.Filename, SHA256: artifact.SHA256, SizeBytes: uint64(artifact.Size), SourceCommit: artifact.SourceCommit, ArtifactProfile: artifact.ArtifactProfile, Target: artifact.Target, ExecutionModel: artifact.Execution}
}
