package e2e_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

func TestRealGuestProgrammaticReceiptBindsExactVerifiedSourceSpan(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(wasm)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('8'),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var handlerCalls atomic.Uint32
	capabilityPlan := programmaticPlan(t, nil, &handlerCalls, 1)
	source := "result = tools.increment(8)\n"
	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms.SemanticAnalysis = true
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analysisRunner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: capabilityPlan.Identity(),
	}
	analysisRequest, err := semantic.NewRequest(source, bindings, capabilityPlan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), analysisRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	planned, err := semantic.BuildSourceBoundPlan(verified, capabilityPlan, semantic.PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := semantic.NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}

	const parent = "source-bound-parent"
	presentation, err := capabilityPlan.Present(capability.ProgramSurfaceProgrammatic, parent)
	if err != nil {
		t.Fatal(err)
	}
	workspaceRoot := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	workspaceManager, err := workspace.NewManager(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer workspaceManager.Close()
	baseWorkspace, err := workspaceManager.Create([]workspace.InitialFile{{Path: "seed.txt", Data: []byte("seed")}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	baseInfo, err := workspaceManager.Inspect(baseWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	parentLineage, _, err := workspaceManager.PortableIdentity(baseWorkspace)
	if err != nil {
		t.Fatal(err)
	}

	var broker *capability.Broker
	executionConfig := runtimeconfig.DefaultRunConfig()
	executionConfig.ExecutionProfile = &profile
	executionConfig.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	executionConfig.Mechanisms.ProgrammaticToolCalling = true
	executionRunner, err := (wazeroengine.Factory{
		WorkspaceManager: workspaceManager, WorkspaceRef: baseWorkspace, WorkspaceOwner: "source-bound-run",
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			created, createErr := capability.NewBroker(capability.Config{
				RunIdentity: "source-bound-run", Plan: capabilityPlan, ProgrammaticParentCallID: parent, SourceResolver: resolver,
			})
			broker = created
			return created, createErr
		}}).New(context.Background(), wasm, executionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer executionRunner.Close(context.Background())

	evidenceRoot := t.TempDir()
	if output := os.Getenv("PYSOLATE_EVIDENCE_OUTPUT_DIR"); output != "" {
		evidenceRoot = output
		if err := os.MkdirAll(evidenceRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(evidenceRoot, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sourceCommit := os.Getenv("PYSOLATE_EVIDENCE_SOURCE_COMMIT")
	if sourceCommit == "" {
		sourceCommit = "0123456789abcdef0123456789abcdef01234567"
	}
	evidenceStore, err := labstore.Open(filepath.Join(evidenceRoot, "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer evidenceStore.Close()
	evidenceLog, err := trajectory.CreateEvidenceLog(filepath.Join(evidenceRoot, "trace.jsonl"), evidenceStore, trajectory.TraceHeader{
		TraceID: "trace-real-source-bound-0001", RootExecutionID: "source-bound-run", SourceCommit: sourceCommit,
	}, trajectory.EvidenceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	defer evidenceLog.Close()

	artifactSHA256 := artifactSHA
	childID := "child-real-0001"
	contextSHA256, briefSHA256 := semanticTestDigest('7'), semanticTestDigest('8')
	contextBody, _, err := evidenceStore.PutJSON(labstore.KindMetadataEvent, []byte(`{"brief":"inspect one private branch","context":"selected parent context"}`), labstore.PutOptions{
		Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	modelContext, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-real-source-bound-0001",
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextSHA256, BriefSHA256: briefSHA256, Availability: trajectory.Available},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelOutput, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{modelContext.EventID},
		Payload: trajectory.ModelOutputPayload{Availability: trajectory.NotRecorded},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelBody, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{modelContext.EventID}, Body: &contextBody,
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextSHA256, BriefSHA256: briefSHA256, Availability: trajectory.Available},
	}); err != nil {
		t.Fatal(err)
	}
	childContext, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentContext, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{modelContext.EventID},
		Payload: trajectory.SubagentContextPayload{ChildID: childID, ContextSHA256: contextSHA256, BriefSHA256: briefSHA256, Availability: trajectory.Available},
	})
	if err != nil {
		t.Fatal(err)
	}
	childPlanSHA256 := semanticTestDigest('9')
	childCode := "from pathlib import Path\nPath('/workspace/child.txt').write_text('child')\nresult = {'child': 'ok'}"
	childSourceSum := sha256.Sum256([]byte(childCode))
	childSourceSHA256 := fmt.Sprintf("sha256:%x", childSourceSum)
	childDocumentSum := sha256.Sum256([]byte("child-program\x00" + childCode))
	childDocumentID := fmt.Sprintf("sha256:%x", childDocumentSum)
	childCodeBody, _, err := evidenceStore.Put(labstore.KindCode, []byte(childCode), labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	childSourceDocument, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSourceDocument, ActorID: "actor-real-source-bound-0001",
		Payload: trajectory.SourceDocumentPayload{DocumentID: childDocumentID, SourceSHA256: childSourceSHA256, Availability: trajectory.Available},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSourceBody, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{childSourceDocument.EventID}, Body: &childCodeBody,
		Payload: trajectory.SourceBodyPayload{DocumentID: childDocumentID, SourceSHA256: childSourceSHA256, DisplayPath: "child_program.py", Availability: trajectory.Available},
	}); err != nil {
		t.Fatal(err)
	}
	descriptor := subagent.Descriptor{
		SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: childID, ParentStreamEpoch: "parent-stream-real-0001",
		ParentLineageSHA256: parentLineage, SourceOccurrence: "semantic_source_binding_test:child-real-0001",
		SourceSHA256: analysis.SourceSHA256, InputsSHA256: semanticTestDigest('a'), ArtifactSHA256: artifactSHA256,
		ExecutionProfileSHA256: semanticTestDigest('b'), ChildPlanSHA256: childPlanSHA256, PrivacyPartition: "fixture-private", Depth: 1,
	}
	childExecutor := subagent.FreshRunnerExecutor{
		Factory: subagent.RunnerFactoryFunc(func(ctx context.Context, descriptor subagent.Descriptor, ref workspace.Ref) (enginecontract.Runner, error) {
			return (wazeroengine.Factory{WorkspaceManager: workspaceManager, WorkspaceRef: ref, WorkspaceOwner: "evidence-child-owner"}).New(ctx, wasm, runtimeconfig.DefaultRunConfig())
		}),
		Builder: subagent.ProgramBuilderFunc(func(descriptor subagent.Descriptor) (subagent.ChildProgram, error) {
			request, marshalErr := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{
				RunID: "child-real-run-0001", Code: childCode, Inputs: []byte(`{}`),
			})
			return subagent.ChildProgram{Request: request}, marshalErr
		}),
	}
	orchestrator, err := subagent.New(subagent.Config{
		Manager: workspaceManager, ParentRef: baseWorkspace, ParentWorkspaceSHA256: baseInfo.WorkspaceSHA256,
		ParentLineage: parentLineage, MaxFanout: 1, MaxDepth: 1, Executor: childExecutor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.Stage(context.Background(), descriptor); err != nil {
		t.Fatal(err)
	}
	joined, err := orchestrator.Seal(context.Background(), childID)
	if err != nil {
		t.Fatal(err)
	}
	childRuntime, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentRuntime, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{childContext.EventID},
		Payload: trajectory.SubagentRuntimePayload{ChildID: childID, FreshRunID: "child-real-run-0001", PreparedImageSHA256: artifactSHA256, ChildPlanSHA256: childPlanSHA256, ParentLiveStateInherited: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	childWorkspace, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentWorkspace, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{childRuntime.EventID},
		Payload: trajectory.SubagentWorkspacePayload{
			ChildID: childID, BaseRootSHA256: parentLineage, ResultRootSHA256: joined.SelectedRoot.IdentitySHA256,
			ChangedEntries: joined.SelectedRoot.ChangedEntries, ChangedBytes: joined.SelectedRoot.ChangedBytes, Disposition: "selected",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	capturedOutput, err := workspaceManager.CaptureFile(joined.SelectedRoot.Ref(), "child.txt", 1024)
	if err != nil || string(capturedOutput) != "child" {
		t.Fatalf("captured child output=%q err=%v", capturedOutput, err)
	}
	outputSum := sha256.Sum256(capturedOutput)
	outputSHA256 := fmt.Sprintf("sha256:%x", outputSum)
	outputBody, _, err := evidenceStore.Put(labstore.KindFile, capturedOutput, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evidenceLog.Append(trajectory.EvidenceInput{
		Type: trajectory.EventWorkspaceFile, ActorID: "actor-real-source-bound-0001", ParentEventIDs: []string{childWorkspace.EventID}, Body: &outputBody,
		Payload: trajectory.WorkspaceFilePayload{WorkspaceSHA256: joined.SelectedRoot.IdentitySHA256, Path: "child.txt", ContentSHA256: outputSHA256, Availability: trajectory.Available},
	}); err != nil {
		t.Fatal(err)
	}

	evidenceRecorder, err := trajectory.NewObservationRecorder(evidenceLog, trajectory.ObservationRecorderConfig{
		ActorID: "actor-real-source-bound-0001", RunID: "source-bound-run", AttemptID: "attempt-real-source-bound-0001",
		PolicySHA256: semanticTestDigest('4'), FreshnessSHA256: semanticTestDigest('5'), GrantsSHA256: semanticTestDigest('6'),
	})
	if err != nil {
		t.Fatal(err)
	}
	observationSession, err := observe.NewSession(observe.Required, evidenceRecorder, "source-bound-run")
	if err != nil {
		t.Fatal(err)
	}
	runContext, err := enginecontract.WithInvocationRef(context.Background(), runtimeconfig.InvocationRef{
		AgentRunID: "agent-source-bound-run", InvocationID: "invocation-source-bound-run", InvocationAttempt: 1, ExecutionID: "source-bound-run",
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext, err = enginecontract.WithObservationSession(runContext, observationSession)
	if err != nil {
		t.Fatal(err)
	}

	runRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "source-bound-run", Code: source, Inputs: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := executionRunner.Run(runContext, runRequest, presentation.PythonPrelude)
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, runRequest, payload)
	if string(response.Result) != `9` {
		t.Fatalf("response=%+v payload=%s", response, payload)
	}
	receipts := broker.SnapshotReceipts()
	site := analysis.CallSites[0]
	if handlerCalls.Load() != 1 || len(receipts) != 1 || receipts[0].Source == nil || !receipt.ValidIdentity(receipts[0]) {
		t.Fatalf("calls=%d receipts=%#v", handlerCalls.Load(), receipts)
	}
	bound := *receipts[0].Source
	if bound.ClaimLevel != receipt.SourceClaimBound || bound.SourceSHA256 != analysis.SourceSHA256 || bound.OccurrenceID != site.ID ||
		bound.DynamicOccurrence != site.DynamicOccurrence || bound.StartLine != site.Span.StartLine || bound.StartColumn != site.Span.StartColumn ||
		bound.EndLine != site.Span.EndLine || bound.EndColumn != site.Span.EndColumn {
		t.Fatalf("binding=%+v site=%+v", bound, site)
	}
	if codeObject, getErr := evidenceStore.Get(childCodeBody); getErr != nil || string(codeObject.Body) != childCode {
		t.Fatalf("captured child code body unavailable: err=%v", getErr)
	}
	if fileObject, getErr := evidenceStore.Get(outputBody); getErr != nil || string(fileObject.Body) != "child" {
		t.Fatalf("captured output body unavailable: err=%v", getErr)
	}
	privateFull, err := evidenceLog.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	production, err := evidenceLog.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil {
		t.Fatal(err)
	}
	publicFull, err := evidenceLog.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPortable)
	if err != nil {
		t.Fatal(err)
	}
	if e2eEvidenceCount(privateFull.Events, trajectory.EventSourceDecision) != 1 || e2eEvidenceCount(privateFull.Events, trajectory.EventRuntimeObservation) == 0 ||
		e2eEvidenceCount(privateFull.Events, trajectory.EventSourceBody) != 1 || e2eEvidenceCount(privateFull.Events, trajectory.EventWorkspaceFile) != 1 ||
		e2eEvidenceCount(privateFull.Events, trajectory.EventToolDecision) != 1 || e2eEvidenceCount(privateFull.Events, trajectory.EventModelOutput) != 1 ||
		e2eEvidenceCount(production.Events, trajectory.EventSourceDecision) != 0 || e2eEvidenceCount(production.Events, trajectory.EventEffectTransition) != 1 ||
		e2eEvidenceCount(publicFull.Events, trajectory.EventSourceDecision) != 1 || e2eEvidenceCount(publicFull.Events, trajectory.EventRuntimeObservation) != 0 ||
		e2eEvidenceCount(publicFull.Events, trajectory.EventToolDecision) != 1 || e2eEvidenceCount(publicFull.Events, trajectory.EventModelOutput) != 1 ||
		e2eEvidenceCount(publicFull.Events, trajectory.EventSourceBody) != 0 || e2eEvidenceCount(publicFull.Events, trajectory.EventWorkspaceFile) != 0 {
		t.Fatalf("private=%d production=%d public=%d", len(privateFull.Events), len(production.Events), len(publicFull.Events))
	}
	for _, event := range production.Events {
		if event.Body != nil {
			t.Fatalf("production leaked body: %+v", event)
		}
	}
	if os.Getenv("PYSOLATE_EVIDENCE_OUTPUT_DIR") != "" {
		artifacts := map[string]trajectory.Export{
			"experiment-full-private.json": privateFull,
			"production-rollback.json":     production,
			"experiment-full-public.json":  publicFull,
		}
		for name, artifact := range artifacts {
			encoded, encodeErr := trajectory.EncodeEvidenceExport(artifact)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			encoded = append(encoded, '\n')
			if writeErr := os.WriteFile(filepath.Join(evidenceRoot, name), encoded, 0o600); writeErr != nil {
				t.Fatal(writeErr)
			}
		}
	}
	t.Logf("dual-profile evidence: trace=%s header=%s private=%d production=%d public=%d", privateFull.TraceID, privateFull.HeaderSHA256, len(privateFull.Events), len(production.Events), len(publicFull.Events))
	t.Logf("source-bound receipt: plan=%s document=%s source=%s occurrence=%s dynamic=%d span=%d:%d-%d:%d receipt=%s",
		capabilityPlan.Identity(), bound.DocumentID, bound.SourceSHA256, bound.OccurrenceID, bound.DynamicOccurrence,
		bound.StartLine, bound.StartColumn, bound.EndLine, bound.EndColumn, receipts[0].ReceiptID)
}

func e2eEvidenceCount(events []trajectory.EvidenceEvent, kind trajectory.EvidenceType) int {
	count := 0
	for _, event := range events {
		if event.Type == kind {
			count++
		}
	}
	return count
}
