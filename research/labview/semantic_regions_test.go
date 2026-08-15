package labview_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestSemanticRegionGraphProjectionIsBoundedAndPrivacySafe(t *testing.T) {
	source := "result = inputs['value']\n"
	view := labview.SemanticRegionGraph{
		SchemaVersion:  labview.SemanticRegionGraphSchemaVersion,
		AnalysisSHA256: semanticViewDigest("analysis"), SourceSHA256: semanticSourceDigest(source),
		AnalyzerSHA256: semanticViewDigest("analyzer"), SourcePrivacy: labview.PrivacyPrivate,
		SourceAvailable: true, Source: source,
		Regions: []labview.SemanticRegionView{{
			ID: semanticViewDigest("region"), Kind: string(semantic.CandidateRegionStraightLine),
			Span:                semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 24},
			ControlPredecessors: []string{}, DataDependencies: []labview.SemanticRegionDataEdge{},
			LiveIns: []string{"inputs"}, LiveOuts: []string{"result"}, LiveInsCanonical: true, LiveOutsCanonical: true,
			CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []string{},
		}},
	}
	if err := labview.ValidateSemanticRegionGraph(view); err != nil {
		t.Fatalf("valid private projection: %v", err)
	}
	portable := view
	portable.SourcePrivacy = labview.PrivacyPortable
	portable.SourceAvailable = false
	portable.Source = ""
	if err := labview.ValidateSemanticRegionGraph(portable); err != nil {
		t.Fatalf("valid portable projection: %v", err)
	}

	for name, mutate := range map[string]func(*labview.SemanticRegionGraph){
		"source hash mismatch": func(value *labview.SemanticRegionGraph) { value.Source += "# changed" },
		"portable source leak": func(value *labview.SemanticRegionGraph) { value.SourcePrivacy = labview.PrivacyPortable },
		"first predecessor": func(value *labview.SemanticRegionGraph) {
			value.Regions[0].ControlPredecessors = []string{semanticViewDigest("other")}
		},
		"unknown producer": func(value *labview.SemanticRegionGraph) {
			value.Regions[0].DataDependencies = []labview.SemanticRegionDataEdge{{Name: "inputs", ProducerRegionID: semanticViewDigest("other")}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := view
			candidate.Regions = append([]labview.SemanticRegionView(nil), view.Regions...)
			mutate(&candidate)
			if err := labview.ValidateSemanticRegionGraph(candidate); err == nil {
				t.Fatal("invalid projection accepted")
			}
		})
	}
}

func TestSemanticRegionProjectionRejectsUnverifiedHandle(t *testing.T) {
	if _, err := labview.ProjectSemanticRegionGraph(semantic.VerifiedAnalysis{}, "", false); err == nil {
		t.Fatal("zero verified handle accepted")
	}
}

func semanticViewDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func semanticSourceDigest(source string) string { return semanticViewDigest(source) }
