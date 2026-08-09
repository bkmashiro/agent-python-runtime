package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
)

type phase7FormalDisjointnessVerdict struct {
	SchemaVersion  int      `json:"schema_version"`
	EvidenceClass  string   `json:"evidence_class"`
	Valid          bool     `json:"valid"`
	CampaignSHA256 []string `json:"campaign_sha256"`
	ArmOrders      []string `json:"arm_orders"`
	CellsPerOrder  int      `json:"cells_per_order"`
	UniqueJobs     int      `json:"unique_jobs"`
	UniqueCgroups  int      `json:"unique_cgroups"`
	SourceCommit   string   `json:"source_commit"`
}

type loadedPhase7FormalCampaign struct {
	Manifest  phase7CellCampaignManifest
	Fragments []phase7PairedCellFragment
	SHA256    string
}

func runPhase7FormalDisjointnessMain(options benchmarkOptions) error {
	if options.InputPath == "" || options.OtherInputPath == "" || options.OutputPath == "" || options.ArtifactPath != "" || options.ManifestPath != "" || options.SchemaPath != "" || options.LifecycleDensityChild {
		return errors.New("validate-phase7-formal-disjointness requires only -input, -other-input, and -output")
	}
	first, err := loadPhase7FormalCampaign(options.InputPath)
	if err != nil {
		return fmt.Errorf("load first Phase 7 formal campaign: %w", err)
	}
	second, err := loadPhase7FormalCampaign(options.OtherInputPath)
	if err != nil {
		return fmt.Errorf("load second Phase 7 formal campaign: %w", err)
	}
	if first.Manifest.ArmOrder == second.Manifest.ArmOrder ||
		!((first.Manifest.ArmOrder == "cow-first" && second.Manifest.ArmOrder == "non-cow-first") || (first.Manifest.ArmOrder == "non-cow-first" && second.Manifest.ArmOrder == "cow-first")) {
		return errors.New("Phase 7 formal campaigns do not have opposite arm orders")
	}
	left, right := first.Fragments[0], second.Fragments[0]
	if !reflect.DeepEqual(left.Artifact, right.Artifact) || !reflect.DeepEqual(left.HostSource, right.HostSource) ||
		!reflect.DeepEqual(left.Backend, right.Backend) || !reflect.DeepEqual(left.Environment, right.Environment) ||
		!reflect.DeepEqual(left.Warmup, right.Warmup) || !reflect.DeepEqual(left.Plan, right.Plan) {
		return errors.New("Phase 7 formal campaign experiment identity drifted across arm orders")
	}
	jobs := make(map[string]struct{}, 42)
	cgroups := make(map[string]struct{}, 42)
	for _, campaign := range []loadedPhase7FormalCampaign{first, second} {
		for _, fragment := range campaign.Fragments {
			if _, duplicate := jobs[fragment.Allocation.JobID]; duplicate {
				return errors.New("Phase 7 formal campaigns reused a Slurm job identity")
			}
			if _, duplicate := cgroups[fragment.Allocation.CgroupPathSHA256]; duplicate {
				return errors.New("Phase 7 formal campaigns reused a cgroup identity")
			}
			jobs[fragment.Allocation.JobID] = struct{}{}
			cgroups[fragment.Allocation.CgroupPathSHA256] = struct{}{}
		}
	}
	verdict := phase7FormalDisjointnessVerdict{
		SchemaVersion: 1, EvidenceClass: "phase7-formal-disjointness", Valid: true,
		CampaignSHA256: []string{first.SHA256, second.SHA256}, ArmOrders: []string{first.Manifest.ArmOrder, second.Manifest.ArmOrder},
		CellsPerOrder: 21, UniqueJobs: len(jobs), UniqueCgroups: len(cgroups), SourceCommit: left.HostSource.Revision,
	}
	encoded, err := json.MarshalIndent(verdict, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return writeAtomic(options.OutputPath, encoded)
}

func loadPhase7FormalCampaign(path string) (loadedPhase7FormalCampaign, error) {
	document, err := readRegularFileBounded(path, 1<<20)
	if err != nil {
		return loadedPhase7FormalCampaign{}, err
	}
	manifest, err := decodePhase7CellCampaignManifest(document)
	if err != nil {
		return loadedPhase7FormalCampaign{}, err
	}
	if manifest.Repeats != 3 || len(manifest.Entries) != 21 {
		return loadedPhase7FormalCampaign{}, errors.New("Phase 7 campaign is not a complete formal campaign")
	}
	base := filepath.Dir(path)
	fragments := make([]phase7PairedCellFragment, 0, 21)
	for index, entry := range manifest.Entries {
		if err := validatePhase7CampaignEntryIdentity(entry, index); err != nil {
			return loadedPhase7FormalCampaign{}, err
		}
		if !canonicalPhase7SlurmDecimal(entry.JobID) || !canonicalPhase7SlurmDecimal(entry.ArrayJobID) {
			return loadedPhase7FormalCampaign{}, errors.New("Phase 7 campaign scheduler identity is not canonical")
		}
		fragmentDocument, err := readRegularFileBounded(filepath.Join(base, entry.FragmentFilename), maximumLifecycleDensityEvidenceBytes)
		if err != nil {
			return loadedPhase7FormalCampaign{}, err
		}
		digest := sha256.Sum256(fragmentDocument)
		if hex.EncodeToString(digest[:]) != entry.FragmentSHA256 {
			return loadedPhase7FormalCampaign{}, errors.New("Phase 7 campaign fragment digest drifted")
		}
		if err := rejectDuplicateJSONDocument(fragmentDocument); err != nil {
			return loadedPhase7FormalCampaign{}, err
		}
		decoder := json.NewDecoder(bytes.NewReader(fragmentDocument))
		decoder.DisallowUnknownFields()
		var fragment phase7PairedCellFragment
		if err := decoder.Decode(&fragment); err != nil {
			return loadedPhase7FormalCampaign{}, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return loadedPhase7FormalCampaign{}, errors.New("Phase 7 formal fragment has trailing JSON")
		}
		if err := validatePhase7PairedCellFragment(fragment); err != nil {
			return loadedPhase7FormalCampaign{}, err
		}
		if fragment.Allocation.JobID != entry.JobID || fragment.Allocation.ArrayJobID != entry.ArrayJobID ||
			fragment.Allocation.ArrayTaskID != entry.ArrayTaskID || fragment.Allocation.CgroupPathSHA256 != entry.CgroupSHA256 || fragment.Allocation.ArmOrder != manifest.ArmOrder {
			return loadedPhase7FormalCampaign{}, errors.New("Phase 7 formal fragment does not match its campaign receipt")
		}
		fragments = append(fragments, fragment)
	}
	if _, _, err := aggregatePhase7PairedCellFragments(fragments, "cow-ready-single-use", 3); err != nil {
		return loadedPhase7FormalCampaign{}, err
	}
	digest := sha256.Sum256(document)
	return loadedPhase7FormalCampaign{Manifest: manifest, Fragments: fragments, SHA256: hex.EncodeToString(digest[:])}, nil
}
