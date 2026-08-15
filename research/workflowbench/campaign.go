package workflowbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const CampaignManifestSchemaVersion = "pysolate.transparent-campaign-manifest.v2"

const maxCampaignReleaseOffsetMS int64 = 60_000

var campaignDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var ErrInvalidCampaignManifest = errors.New("invalid transparent campaign manifest")

type CampaignExpectation struct {
	Admission   string          `json:"admission"`
	Sharing     string          `json:"sharing"`
	Disposition string          `json:"disposition"`
	Oracle      json.RawMessage `json:"oracle"`
}

type CampaignExecutionKind string

const (
	CampaignExecutePython   CampaignExecutionKind = "execute_python"
	CampaignConsumeResult   CampaignExecutionKind = "consume_result"
	CampaignExactRequest    CampaignExecutionKind = "exact_request"
	CampaignVerifyWorkspace CampaignExecutionKind = "verify_workspace"
	CampaignStartWorkflow   CampaignExecutionKind = "start_workflow"
	CampaignResumeWorkflow  CampaignExecutionKind = "resume_workflow"
	CampaignDelegateChild   CampaignExecutionKind = "delegate_child"
)

type CampaignCancelPoint string

const (
	CampaignCancelNone                CampaignCancelPoint = "none"
	CampaignCancelAfterWorkspaceFork  CampaignCancelPoint = "after_workspace_fork"
	CampaignCancelAfterParentTerminal CampaignCancelPoint = "after_parent_terminal"
)

type CampaignResumeTransition string

const (
	CampaignResumeSameAuthority    CampaignResumeTransition = "same_authority"
	CampaignResumeFreshnessChanged CampaignResumeTransition = "freshness_changed"
	CampaignResumePlanGrantChanged CampaignResumeTransition = "plan_grant_changed"
	CampaignResumeExpired          CampaignResumeTransition = "expired"
)

type CampaignVerifierContract struct {
	SourceSHA256      string `json:"source_sha256"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	ProfileSHA256     string `json:"profile_sha256"`
	EnvironmentSHA256 string `json:"environment_sha256"`
	PolicySHA256      string `json:"policy_sha256"`
}

type CampaignResumeContract struct {
	FromProgramID string                   `json:"from_program_id"`
	Transition    CampaignResumeTransition `json:"transition"`
}

type CampaignDelegationContract struct {
	GroupID            string `json:"group_id"`
	ParentPlanRole     string `json:"parent_plan_role"`
	ParentPlanSHA256   string `json:"parent_plan_sha256"`
	MaxDelegatedCalls  uint32 `json:"max_delegated_calls"`
	ChildReservedCalls uint32 `json:"child_reserved_calls"`
}

// CampaignExecutionContract belongs to the research fixture. Runtime packages remain
// unaware of campaign IDs, families, expected labels, and paper outcomes.
type CampaignExecutionContract struct {
	Kind            CampaignExecutionKind       `json:"kind"`
	SourceProgramID string                      `json:"source_program_id,omitempty"`
	Verifier        *CampaignVerifierContract   `json:"verifier,omitempty"`
	Resume          *CampaignResumeContract     `json:"resume,omitempty"`
	Delegation      *CampaignDelegationContract `json:"delegation,omitempty"`
	CancelPoint     CampaignCancelPoint         `json:"cancel_point"`
}

type CampaignProgram struct {
	ID                     string                    `json:"id"`
	Family                 string                    `json:"family"`
	ReleaseOffsetMS        int64                     `json:"release_offset_ms"`
	Source                 string                    `json:"source"`
	SourceSHA256           string                    `json:"source_sha256"`
	Inputs                 json.RawMessage           `json:"inputs"`
	InputsSHA256           string                    `json:"inputs_sha256"`
	PlanSHA256             string                    `json:"plan_sha256"`
	GrantSetSHA256         string                    `json:"grant_set_sha256"`
	PrivacyPartition       string                    `json:"privacy_partition"`
	WorkspaceFixtureSHA256 string                    `json:"workspace_fixture_sha256"`
	Dependencies           []string                  `json:"dependencies,omitempty"`
	CannotProve            []string                  `json:"cannot_prove"`
	Execution              CampaignExecutionContract `json:"execution"`
	Expected               CampaignExpectation       `json:"expected"`
}

type CampaignManifest struct {
	SchemaVersion         string            `json:"schema_version"`
	CampaignID            string            `json:"campaign_id"`
	PhysicalSlots         uint32            `json:"physical_slots"`
	Programs              []CampaignProgram `json:"programs"`
	WalkthroughProgramIDs []string          `json:"walkthrough_program_ids"`
}

func CanonicalTransparentCampaign() (CampaignManifest, error) {
	type fixture struct {
		id, family, source, inputs, plan, privacy, workspace, admission, sharing, disposition, oracle string
		release                                                                                       int64
		dependencies                                                                                  []string
	}
	fixtures := []fixture{
		{"P01", "authority_bifurcation", "values = sorted(inputs['values'])\nresult = {'normalized': values}\n", `{"values":[3,1,2]}`, "pure", "private-shared", "root-base", "admit", "producer_leader", "complete", `{"normalized":[1,2,3]}`, 0, nil},
		{"P02", "authority_bifurcation", "result = {'consumer': 'left', 'total': sum(inputs['normalized'])}\n", `{"normalized":[1,2,3]}`, "consumer-left", "private-a", "root-base", "admit", "producer_consumer", "complete", `{"consumer":"left","total":6}`, 4, []string{"P01"}},
		{"P03", "authority_bifurcation", "result = {'consumer': 'right', 'largest': max(inputs['normalized'])}\n", `{"normalized":[1,2,3]}`, "consumer-right", "private-a", "root-base", "admit", "producer_consumer", "complete", `{"consumer":"right","largest":3}`, 4, []string{"P01"}},
		{"P04", "authority_bifurcation", "result = {'consumer': 'cancelled', 'count': len(inputs['normalized'])}\n", `{"normalized":[1,2,3]}`, "consumer-left", "private-a", "root-base", "admit", "producer_consumer", "cancelled", `{"consumer":"cancelled","count":3}`, 6, []string{"P01"}},
		{"P05", "exact_sharing", "result = {'square': inputs['value'] * inputs['value']}\n", `{"value":7}`, "pure", "private-a", "root-base", "admit", "exact_leader", "complete", `{"square":49}`, 10, nil},
		{"P06", "exact_sharing", "result = {'square': inputs['value'] * inputs['value']}\n", `{"value":7}`, "pure", "private-a", "root-base", "admit", "exact_waiter", "complete", `{"square":49}`, 10, nil},
		{"P07", "exact_sharing", "result = {'square': pow(inputs['value'], 2)}\n", `{"value":7}`, "pure", "private-a", "root-base", "admit", "reject_source_mismatch", "complete", `{"square":49}`, 11, nil},
		{"P08", "exact_sharing", "result = {'square': inputs['value'] * inputs['value']}\n", `{"value":8}`, "pure", "private-a", "root-base", "admit", "reject_input_mismatch", "complete", `{"square":64}`, 12, nil},
		{"P09", "exact_sharing", "result = {'square': inputs['value'] * inputs['value']}\n", `{"value":7}`, "pure", "private-b", "root-base", "admit", "reject_privacy_mismatch", "complete", `{"square":49}`, 13, nil},
		{"P10", "root_verification", "from pathlib import Path\nPath('/workspace/result.txt').write_text('same')\nresult = {'root': 'same'}\n", `{}`, "pure", "private-a", "root-base", "admit", "verifier_leader", "complete", `{"root":"same"}`, 18, nil},
		{"P11", "root_verification", "from pathlib import Path\np = Path('/workspace/result.txt')\np.write_text('same')\nresult = {'root': 'same'}\n", `{}`, "pure", "private-a", "root-base", "admit", "verifier_waiter_exact_root", "complete", `{"root":"same"}`, 19, nil},
		{"P12", "root_verification", "from pathlib import Path\nPath('/workspace/result.txt').write_text('different')\nresult = {'root': 'different'}\n", `{}`, "pure", "private-a", "root-base", "admit", "reject_root_mismatch", "complete", `{"root":"different"}`, 20, nil},
		{"P13", "authority_resume", "prefix = inputs['prefix']\nresult = {'phase': 'before_wait', 'prefix': prefix}\n", `{"prefix":"stable"}`, "observer", "private-a", "root-base", "admit", "resume_same_authority", "complete", `{"phase":"before_wait","prefix":"stable"}`, 25, nil},
		{"P14", "authority_resume", "result = {'phase': 'freshness_changed', 'value': inputs['value']}\n", `{"value":"v2"}`, "observer", "private-a", "root-base", "admit", "resume_refresh_observation", "complete", `{"phase":"freshness_changed","value":"v2"}`, 26, []string{"P13"}},
		{"P15", "authority_resume", "result = {'phase': 'grant_changed', 'value': inputs['value']}\n", `{"value":"revalidated"}`, "observer-alt", "private-a", "root-base", "admit", "resume_revalidate_authority", "complete", `{"phase":"grant_changed","value":"revalidated"}`, 27, []string{"P13"}},
		{"P16", "authority_resume", "result = {'phase': 'expired'}\n", `{}`, "observer", "private-a", "root-base", "reject_expired", "no_execution", "rejected", `{"phase":"expired"}`, 28, []string{"P13"}},
		{"P17", "delegation_attenuation", "result = {'child': 'valid', 'value': inputs['value']}\n", `{"value":17}`, "consumer-left", "private-a", "root-base", "admit", "child_narrower", "complete", `{"child":"valid","value":17}`, 32, nil},
		{"P18", "delegation_attenuation", "result = {'child': 'widened'}\n", `{}`, "consumer-widened", "private-a", "root-base", "reject_widening", "no_execution", "rejected", `{"child":"widened"}`, 33, nil},
		{"P19", "delegation_attenuation", "result = {'child': 'over_budget'}\n", `{}`, "consumer-left", "private-a", "root-base", "reject_budget", "no_execution", "rejected", `{"child":"over_budget"}`, 34, nil},
		{"P20", "delegation_attenuation", "result = {'child': 'late'}\n", `{}`, "consumer-left", "private-a", "root-base", "reject_terminal", "no_execution", "cancelled", `{"child":"late"}`, 35, []string{"P17"}},
	}
	manifest := CampaignManifest{
		SchemaVersion: CampaignManifestSchemaVersion, CampaignID: "authority-transparent-20-v2", PhysicalSlots: 3,
		Programs: make([]CampaignProgram, 0, len(fixtures)), WalkthroughProgramIDs: []string{"P02", "P06", "P11", "P15", "P18"},
	}
	executions, err := campaignExecutionContracts()
	if err != nil {
		return CampaignManifest{}, err
	}
	if len(executions) != len(fixtures) {
		return CampaignManifest{}, ErrInvalidCampaignManifest
	}
	planCache := map[string][2]string{}
	for index, fixture := range fixtures {
		identities, ok := planCache[fixture.plan]
		if !ok {
			plan, grantSet, err := campaignPlan(fixture.plan)
			if err != nil {
				return CampaignManifest{}, err
			}
			identities = [2]string{plan, grantSet}
			planCache[fixture.plan] = identities
		}
		inputs := json.RawMessage(fixture.inputs)
		oracle := json.RawMessage(fixture.oracle)
		manifest.Programs = append(manifest.Programs, CampaignProgram{
			ID: fixture.id, Family: fixture.family, ReleaseOffsetMS: fixture.release,
			Source: fixture.source, SourceSHA256: campaignDigest([]byte(fixture.source)),
			Inputs: inputs, InputsSHA256: campaignDigest(inputs), PlanSHA256: identities[0], GrantSetSHA256: identities[1],
			PrivacyPartition: fixture.privacy, WorkspaceFixtureSHA256: campaignDigest([]byte("workspace-fixture:" + fixture.workspace)),
			Dependencies: append([]string(nil), fixture.dependencies...), CannotProve: campaignCannotProve(fixture.family),
			Execution: executions[index],
			Expected:  CampaignExpectation{Admission: fixture.admission, Sharing: fixture.sharing, Disposition: fixture.disposition, Oracle: oracle},
		})
	}
	if err := manifest.Validate(); err != nil {
		return CampaignManifest{}, err
	}
	return manifest, nil
}

func campaignExecutionContracts() ([]CampaignExecutionContract, error) {
	verifier := func() *CampaignVerifierContract {
		return &CampaignVerifierContract{
			SourceSHA256:      campaignDigest([]byte("campaign-verifier-source-v1")),
			ArtifactSHA256:    campaignDigest([]byte("campaign-verifier-artifact-v1")),
			ProfileSHA256:     campaignDigest([]byte("campaign-verifier-profile-v1")),
			EnvironmentSHA256: campaignDigest([]byte("campaign-verifier-environment-v1")),
			PolicySHA256:      campaignDigest([]byte("campaign-verifier-policy-v1")),
		}
	}
	resume := func(from string, transition CampaignResumeTransition) *CampaignResumeContract {
		return &CampaignResumeContract{FromProgramID: from, Transition: transition}
	}
	parentPlan, _, err := campaignPlan("consumer-left")
	if err != nil {
		return nil, err
	}
	delegation := func(group string) *CampaignDelegationContract {
		return &CampaignDelegationContract{GroupID: group, ParentPlanRole: "consumer-left", ParentPlanSHA256: parentPlan, MaxDelegatedCalls: 1, ChildReservedCalls: 1}
	}
	return []CampaignExecutionContract{
		{Kind: CampaignExecutePython, CancelPoint: CampaignCancelNone},
		{Kind: CampaignConsumeResult, SourceProgramID: "P01", CancelPoint: CampaignCancelNone},
		{Kind: CampaignConsumeResult, SourceProgramID: "P01", CancelPoint: CampaignCancelNone},
		{Kind: CampaignConsumeResult, SourceProgramID: "P01", CancelPoint: CampaignCancelAfterWorkspaceFork},
		{Kind: CampaignExactRequest, CancelPoint: CampaignCancelNone},
		{Kind: CampaignExactRequest, CancelPoint: CampaignCancelNone},
		{Kind: CampaignExactRequest, CancelPoint: CampaignCancelNone},
		{Kind: CampaignExactRequest, CancelPoint: CampaignCancelNone},
		{Kind: CampaignExactRequest, CancelPoint: CampaignCancelNone},
		{Kind: CampaignVerifyWorkspace, Verifier: verifier(), CancelPoint: CampaignCancelNone},
		{Kind: CampaignVerifyWorkspace, Verifier: verifier(), CancelPoint: CampaignCancelNone},
		{Kind: CampaignVerifyWorkspace, Verifier: verifier(), CancelPoint: CampaignCancelNone},
		{Kind: CampaignStartWorkflow, CancelPoint: CampaignCancelNone},
		{Kind: CampaignResumeWorkflow, Resume: resume("P13", CampaignResumeFreshnessChanged), CancelPoint: CampaignCancelNone},
		{Kind: CampaignResumeWorkflow, Resume: resume("P13", CampaignResumePlanGrantChanged), CancelPoint: CampaignCancelNone},
		{Kind: CampaignResumeWorkflow, Resume: resume("P13", CampaignResumeExpired), CancelPoint: CampaignCancelNone},
		{Kind: CampaignDelegateChild, Delegation: delegation("delegation-main"), CancelPoint: CampaignCancelNone},
		{Kind: CampaignDelegateChild, Delegation: delegation("delegation-widening"), CancelPoint: CampaignCancelNone},
		{Kind: CampaignDelegateChild, Delegation: delegation("delegation-main"), CancelPoint: CampaignCancelNone},
		{Kind: CampaignDelegateChild, Delegation: delegation("delegation-main"), CancelPoint: CampaignCancelAfterParentTerminal},
	}, nil
}

func (manifest CampaignManifest) Validate() error {
	if manifest.SchemaVersion != CampaignManifestSchemaVersion || manifest.CampaignID == "" || manifest.PhysicalSlots == 0 || len(manifest.Programs) != 20 || len(manifest.WalkthroughProgramIDs) == 0 {
		return ErrInvalidCampaignManifest
	}
	seen := make(map[string]struct{}, len(manifest.Programs))
	families := map[string]int{}
	var lastRelease int64
	for index, program := range manifest.Programs {
		expectedID := fmt.Sprintf("P%02d", index+1)
		if program.ID != expectedID || program.ReleaseOffsetMS < 0 || program.ReleaseOffsetMS > maxCampaignReleaseOffsetMS || (index > 0 && program.ReleaseOffsetMS < lastRelease) || program.Source == "" || campaignDigest([]byte(program.Source)) != program.SourceSHA256 ||
			!canonicalCampaignJSON(program.Inputs) || campaignDigest(program.Inputs) != program.InputsSHA256 || !canonicalCampaignJSON(program.Expected.Oracle) ||
			!campaignDigestPattern.MatchString(program.PlanSHA256) || !campaignDigestPattern.MatchString(program.GrantSetSHA256) || !campaignDigestPattern.MatchString(program.WorkspaceFixtureSHA256) ||
			program.PrivacyPartition == "" || program.Family == "" || len(program.CannotProve) == 0 || program.Execution.validate(seen) != nil || program.Expected.Admission == "" || program.Expected.Sharing == "" || program.Expected.Disposition == "" {
			return ErrInvalidCampaignManifest
		}
		for _, dependency := range program.Dependencies {
			if _, ok := seen[dependency]; !ok {
				return ErrInvalidCampaignManifest
			}
		}
		seen[program.ID] = struct{}{}
		families[program.Family]++
		lastRelease = program.ReleaseOffsetMS
	}
	wanted := map[string]int{"authority_bifurcation": 4, "exact_sharing": 5, "root_verification": 3, "authority_resume": 4, "delegation_attenuation": 4}
	if len(families) != len(wanted) {
		return ErrInvalidCampaignManifest
	}
	for family, count := range wanted {
		if families[family] != count {
			return ErrInvalidCampaignManifest
		}
	}
	for _, id := range manifest.WalkthroughProgramIDs {
		if _, ok := seen[id]; !ok {
			return ErrInvalidCampaignManifest
		}
	}
	p05, p06, p07, p08, p09 := manifest.Programs[4], manifest.Programs[5], manifest.Programs[6], manifest.Programs[7], manifest.Programs[8]
	if p05.SourceSHA256 != p06.SourceSHA256 || p05.InputsSHA256 != p06.InputsSHA256 || p05.PlanSHA256 != p06.PlanSHA256 || p05.WorkspaceFixtureSHA256 != p06.WorkspaceFixtureSHA256 || p05.PrivacyPartition != p06.PrivacyPartition ||
		p07.SourceSHA256 == p05.SourceSHA256 || p07.InputsSHA256 != p05.InputsSHA256 || p08.SourceSHA256 != p05.SourceSHA256 || p08.InputsSHA256 == p05.InputsSHA256 ||
		p09.SourceSHA256 != p05.SourceSHA256 || p09.InputsSHA256 != p05.InputsSHA256 || p09.PrivacyPartition == p05.PrivacyPartition {
		return ErrInvalidCampaignManifest
	}
	return nil
}

func (contract CampaignExecutionContract) validate(seen map[string]struct{}) error {
	if contract.CancelPoint != CampaignCancelNone && contract.CancelPoint != CampaignCancelAfterWorkspaceFork && contract.CancelPoint != CampaignCancelAfterParentTerminal {
		return ErrInvalidCampaignManifest
	}
	withoutSpecific := func() bool {
		return contract.SourceProgramID == "" && contract.Verifier == nil && contract.Resume == nil && contract.Delegation == nil
	}
	switch contract.Kind {
	case CampaignExecutePython, CampaignExactRequest, CampaignStartWorkflow:
		if !withoutSpecific() {
			return ErrInvalidCampaignManifest
		}
	case CampaignConsumeResult:
		if _, ok := seen[contract.SourceProgramID]; !ok || contract.Verifier != nil || contract.Resume != nil || contract.Delegation != nil {
			return ErrInvalidCampaignManifest
		}
	case CampaignVerifyWorkspace:
		if contract.SourceProgramID != "" || contract.Verifier == nil || contract.Resume != nil || contract.Delegation != nil ||
			!campaignDigestPattern.MatchString(contract.Verifier.SourceSHA256) || !campaignDigestPattern.MatchString(contract.Verifier.ArtifactSHA256) ||
			!campaignDigestPattern.MatchString(contract.Verifier.ProfileSHA256) || !campaignDigestPattern.MatchString(contract.Verifier.EnvironmentSHA256) || !campaignDigestPattern.MatchString(contract.Verifier.PolicySHA256) {
			return ErrInvalidCampaignManifest
		}
	case CampaignResumeWorkflow:
		if contract.SourceProgramID != "" || contract.Verifier != nil || contract.Resume == nil || contract.Delegation != nil {
			return ErrInvalidCampaignManifest
		}
		if _, ok := seen[contract.Resume.FromProgramID]; !ok {
			return ErrInvalidCampaignManifest
		}
		switch contract.Resume.Transition {
		case CampaignResumeSameAuthority, CampaignResumeFreshnessChanged, CampaignResumePlanGrantChanged, CampaignResumeExpired:
		default:
			return ErrInvalidCampaignManifest
		}
	case CampaignDelegateChild:
		if contract.SourceProgramID != "" || contract.Verifier != nil || contract.Resume != nil || contract.Delegation == nil || contract.Delegation.GroupID == "" || contract.Delegation.ParentPlanRole == "" || !campaignDigestPattern.MatchString(contract.Delegation.ParentPlanSHA256) || contract.Delegation.MaxDelegatedCalls == 0 || contract.Delegation.ChildReservedCalls == 0 {
			return ErrInvalidCampaignManifest
		}
	default:
		return ErrInvalidCampaignManifest
	}
	return nil
}

func (manifest CampaignManifest) Clone() CampaignManifest {
	clone := manifest
	clone.Programs = make([]CampaignProgram, len(manifest.Programs))
	for index, program := range manifest.Programs {
		clone.Programs[index] = program
		clone.Programs[index].Inputs = append(json.RawMessage(nil), program.Inputs...)
		clone.Programs[index].Dependencies = append([]string(nil), program.Dependencies...)
		clone.Programs[index].CannotProve = append([]string(nil), program.CannotProve...)
		if program.Execution.Verifier != nil {
			value := *program.Execution.Verifier
			clone.Programs[index].Execution.Verifier = &value
		}
		if program.Execution.Resume != nil {
			value := *program.Execution.Resume
			clone.Programs[index].Execution.Resume = &value
		}
		if program.Execution.Delegation != nil {
			value := *program.Execution.Delegation
			clone.Programs[index].Execution.Delegation = &value
		}
		clone.Programs[index].Expected.Oracle = append(json.RawMessage(nil), program.Expected.Oracle...)
	}
	clone.WalkthroughProgramIDs = append([]string(nil), manifest.WalkthroughProgramIDs...)
	return clone
}

func campaignCannotProve(family string) []string {
	switch family {
	case "authority_bifurcation":
		return []string{"shared mutable Guest state", "cross-privacy sharing", "performance improvement"}
	case "exact_sharing":
		return []string{"semantic equivalence", "arbitrary Python purity", "performance improvement"}
	case "root_verification":
		return []string{"semantic workspace equivalence", "general merge", "performance improvement"}
	case "authority_resume":
		return []string{"Guest continuation restore", "multi-wait scheduling", "performance improvement"}
	case "delegation_attenuation":
		return []string{"grant semantic subsumption", "external-effect safety", "performance improvement"}
	default:
		return nil
	}
}

func campaignPlan(role string) (string, string, error) {
	registry := capability.NewRegistry()
	capabilities := []string{}
	switch role {
	case "pure":
	case "consumer-left":
		capabilities = []string{"consumer.left"}
	case "consumer-right":
		capabilities = []string{"consumer.right"}
	case "consumer-widened":
		capabilities = []string{"consumer.left", "network.read"}
	case "observer":
		capabilities = []string{"observation.read"}
	case "observer-alt":
		capabilities = []string{"observation.read.alt"}
	default:
		return "", "", ErrInvalidCampaignManifest
	}
	for _, name := range capabilities {
		grant, err := capability.NewGrant(json.RawMessage(`{"campaign_role":"` + role + `","capability":"` + name + `"}`))
		if err != nil {
			return "", "", err
		}
		spec := capability.Spec{
			Name: name, Version: "pysolate.campaign." + name + ".v1", Description: "Campaign capability " + name,
			EffectClass: capability.EffectPure, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.campaign." + name + ".handler.v1",
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		}
		if err := registry.Register(spec, grant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })); err != nil {
			return "", "", err
		}
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		return "", "", err
	}
	grants, err := json.Marshal(plan.Grants())
	if err != nil {
		return "", "", err
	}
	return plan.Identity(), campaignDigest(grants), nil
}

func canonicalCampaignJSON(value []byte) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, value) != nil {
		return false
	}
	return bytes.Equal(compact.Bytes(), value)
}

func campaignDigest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
