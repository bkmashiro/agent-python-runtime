package numpyproducer

import (
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
)

func TestPreparedLineageBindsOriginalAdmissionAndFinalSource(t *testing.T) {
	raw, declaration, source := validDeclaration(t)
	bindings := testBindings()
	originalAnalysis := testAnalysis(source, bindings)
	admission, err := admitAnalysis(raw, source, originalAnalysis, bindings)
	if err != nil {
		t.Fatal(err)
	}
	plan := numpycodec.MaterializationPlan{
		LeaseID: digestA, ConsumerBindingSHA256: digestB, ConsumerSourceSHA256: digestA,
		FinalSourceSHA256: digestB, InputsSHA256: digestA, RequestSHA256: digestB,
		HostToGuestCopyBytes: 48, RequestBytes: 1024,
	}
	finalAnalysis := testAnalysis("final source placeholder", bindings)
	finalAnalysis.SourceSHA256 = plan.FinalSourceSHA256
	finalAnalysis.ASTSHA256 = digestB
	rawLineage, lineage, err := sealPreparedLineageAnalysis(admission, declaration, plan, finalAnalysis)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawLineage) == 0 || lineage.Validate(admission, plan) != nil || lineage.Patch.FinalSourceSHA256 != plan.FinalSourceSHA256 ||
		lineage.Patch.FinalASTSHA256 != admission.ASTSHA256 || lineage.Patch.DerivedASTSHA256 != finalAnalysis.ASTSHA256 ||
		lineage.Selection.FinalSourceSHA256 != plan.FinalSourceSHA256 {
		t.Fatalf("lineage=%+v", lineage)
	}
	changed := plan
	changed.FinalSourceSHA256 = digestA
	if _, _, err := sealPreparedLineageAnalysis(admission, declaration, changed, finalAnalysis); !errors.Is(err, ErrLineage) {
		t.Fatalf("final-source substitution err=%v", err)
	}
	changedAdmission := admission
	changedAdmission.InputsSHA256 = digestB
	if lineage.Validate(changedAdmission, plan) == nil {
		t.Fatal("admission substitution accepted")
	}
	lineage.Decision.SourceSHA256 = digestA
	if lineage.Validate(admission, plan) == nil {
		t.Fatal("decision binding substitution accepted")
	}
	_, lineage, err = sealPreparedLineageAnalysis(admission, declaration, plan, finalAnalysis)
	if err != nil {
		t.Fatal(err)
	}
	changed = plan
	changed.InputsSHA256 = digestB
	if lineage.Validate(admission, changed) == nil {
		t.Fatal("plan inputs substitution accepted")
	}
}
