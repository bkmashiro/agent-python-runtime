package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeP7FormalCampaignFixture(t *testing.T, root, order string, arrayBase, jobBase uint32) string {
	t.Helper()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := phase7CellCampaignManifest{SchemaVersion: 1, EvidenceClass: "phase7-cell-campaign", ArmOrder: order, Repeats: 3, Entries: make([]phase7CellCampaignEntry, 0, 21)}
	slots := []uint32{1, 2, 4, 8, 16, 32, 64}
	for slotIndex, requested := range slots {
		for repeat := uint32(0); repeat < 3; repeat++ {
			task := uint32(slotIndex)*3 + repeat
			fragment := testP7Fragment(t, order, 3, requested, repeat, task)
			array := arrayBase + task/7
			fragment.Allocation.ArrayJobID = fmt.Sprintf("%d", array)
			fragment.Allocation.JobID = fmt.Sprintf("%d", jobBase+task)
			fragment.Allocation.CgroupPathSHA256 = fmt.Sprintf("%064x", jobBase+task+1)
			encoded, err := json.Marshal(fragment)
			if err != nil {
				t.Fatal(err)
			}
			encoded = append(encoded, '\n')
			filename := fmt.Sprintf("cell-%02d.json", task)
			if err := os.WriteFile(filepath.Join(root, filename), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(encoded)
			tag := fmt.Sprintf("%d_%d", array, task)
			manifest.Entries = append(manifest.Entries, phase7CellCampaignEntry{
				SampleIndex: task, FragmentFilename: filename, FragmentSHA256: hex.EncodeToString(digest[:]),
				ArchiveFilename: fmt.Sprintf("cell-%d-result-%s.tar.gz", task, tag), ArchiveSHA256: strings.Repeat("a", 64),
				READYFilename: "READY-" + tag, ACKEDFilename: "ACKED-" + tag, SlurmFilename: fmt.Sprintf("slurm-%02d.json", task),
				JobID: fragment.Allocation.JobID, ArrayJobID: fragment.Allocation.ArrayJobID, ArrayTaskID: task, CgroupSHA256: fragment.Allocation.CgroupPathSHA256,
			})
		}
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "campaign-manifest.json")
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPhase7FormalDisjointnessRequiresOppositeDisjointCampaigns(t *testing.T) {
	root := t.TempDir()
	first := writeP7FormalCampaignFixture(t, filepath.Join(root, "cow"), "cow-first", 900, 1000)
	second := writeP7FormalCampaignFixture(t, filepath.Join(root, "non-cow"), "non-cow-first", 910, 2000)
	output := filepath.Join(root, "verdict.json")
	if err := runPhase7FormalDisjointnessMain(benchmarkOptions{InputPath: first, OtherInputPath: second, OutputPath: output}); err != nil {
		t.Fatal(err)
	}
	var verdict phase7FormalDisjointnessVerdict
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &verdict); err != nil {
		t.Fatal(err)
	}
	if !verdict.Valid || verdict.UniqueJobs != 42 || verdict.UniqueCgroups != 42 {
		t.Fatalf("verdict drift: %#v", verdict)
	}

	duplicateRoot := filepath.Join(root, "duplicate")
	duplicate := writeP7FormalCampaignFixture(t, duplicateRoot, "non-cow-first", 920, 3000)
	manifestBytes, _ := os.ReadFile(duplicate)
	var manifest phase7CellCampaignManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	fragmentPath := filepath.Join(duplicateRoot, manifest.Entries[0].FragmentFilename)
	fragmentBytes, _ := os.ReadFile(fragmentPath)
	var fragment phase7PairedCellFragment
	if err := json.Unmarshal(fragmentBytes, &fragment); err != nil {
		t.Fatal(err)
	}
	fragment.Allocation.JobID = "1000"
	fragment.Allocation.CgroupPathSHA256 = fmt.Sprintf("%064x", 1001)
	fragmentBytes, _ = json.Marshal(fragment)
	fragmentBytes = append(fragmentBytes, '\n')
	if err := os.WriteFile(fragmentPath, fragmentBytes, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(fragmentBytes)
	manifest.Entries[0].JobID = fragment.Allocation.JobID
	manifest.Entries[0].CgroupSHA256 = fragment.Allocation.CgroupPathSHA256
	manifest.Entries[0].FragmentSHA256 = hex.EncodeToString(digest[:])
	manifestBytes, _ = json.Marshal(manifest)
	if err := os.WriteFile(duplicate, append(manifestBytes, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runPhase7FormalDisjointnessMain(benchmarkOptions{InputPath: first, OtherInputPath: duplicate, OutputPath: filepath.Join(root, "duplicate-verdict.json")}); err == nil {
		t.Fatal("cross-order allocation reuse accepted")
	}

	sameOrder := writeP7FormalCampaignFixture(t, filepath.Join(root, "same"), "cow-first", 930, 4000)
	if err := runPhase7FormalDisjointnessMain(benchmarkOptions{InputPath: first, OtherInputPath: sameOrder, OutputPath: filepath.Join(root, "same-verdict.json")}); err == nil {
		t.Fatal("same arm order accepted")
	}
}
