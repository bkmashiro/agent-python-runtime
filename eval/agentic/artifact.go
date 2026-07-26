package agentic

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrTrialArtifact = errors.New("invalid agentic trial artifact")

func ValidateTrialResult(result TrialResult) error {
	if (result.Version != "agentic-development-trial/v1" && result.Version != "agentic-development-trial/v2" && result.Version != "agentic-development-trial/v3") || !supportedDevelopmentModel(result.Model) ||
		!result.Condition.valid() || !result.Limits.valid() || !validExecutionIdentity(result.Identity, result.Condition) || result.Replicate > 1000 ||
		!validDigest(result.SpecDigest) || result.TrialID != "dev_"+strings.TrimPrefix(result.SpecDigest, "sha256:")[:32] ||
		result.TaskID == "" || !validDigest(result.TaskDigest) || !validDigest(result.SourceRecordDigest) ||
		!validDigest(result.PromptDigest) || !validDigest(result.SurfaceDigest) ||
		!validDigest(result.CatalogDigest) || result.ProviderCalls != uint32(len(result.Exchanges)) ||
		result.ProviderCalls > result.ProviderAttempts || result.ProviderAttempts > result.Limits.MaxProviderCalls || result.ToolCalls < 0 || result.ToolCalls > int(result.Limits.MaxToolCalls) ||
		result.PythonRuns > result.PythonAttempts || result.PythonAttempts > result.Limits.MaxPythonRuns || len(result.TextDigests) > int(result.ProviderCalls) {
		return ErrTrialArtifact
	}
	if result.Version == "agentic-development-trial/v3" {
		document := expectedTreatmentDocument(result.TreatmentID)
		if document == "" || result.TreatmentDigest != digest([]byte(document)) {
			return ErrTrialArtifact
		}
	} else if result.TreatmentID != "" || result.TreatmentDigest != "" {
		return ErrTrialArtifact
	}
	for _, value := range result.TextDigests {
		if !validDigest(value) {
			return ErrTrialArtifact
		}
	}
	if result.TreatmentID == TreatmentStructuredHostContextV1 {
		if len(result.HostContextDigests) > 31 {
			return ErrTrialArtifact
		}
		for _, value := range result.HostContextDigests {
			if !validDigest(value) {
				return ErrTrialArtifact
			}
		}
	} else if len(result.HostContextDigests) != 0 {
		return ErrTrialArtifact
	}
	routerTrial := result.Condition == ConditionHybrid && result.TreatmentID == TreatmentHybridTwoStageRouterV1
	if result.Route != nil {
		if !routerTrial {
			return ErrTrialArtifact
		}
		if !result.Route.ReasonCode.valid() || !validDigest(result.Route.RouterPromptDigest) || !validDigest(result.Route.RouterSurfaceDigest) ||
			!validDigest(result.Route.ExecutionPromptDigest) || !validDigest(result.Route.ExecutionSurfaceDigest) ||
			result.Route.RouterPromptDigest != result.PromptDigest || result.Route.RouterSurfaceDigest != result.SurfaceDigest ||
			(result.Route.Route != HybridRouteDirect && result.Route.Route != HybridRoutePython) {
			return ErrTrialArtifact
		}
	} else if routerTrial && result.ErrorCode == "" {
		return ErrTrialArtifact
	}
	var usage = result.Usage
	declaredTotal, declaredOK := checkedAdd(usage.InputTokens, usage.OutputTokens)
	if !declaredOK || usage.TotalTokens != declaredTotal {
		return ErrTrialArtifact
	}
	overBudget := usage.InputTokens > result.Limits.MaxInputTokens || usage.OutputTokens > result.Limits.MaxOutputTokens || usage.TotalTokens > result.Limits.MaxTotalTokens
	budgetExhausted := overBudget || usage.InputTokens >= result.Limits.MaxInputTokens || usage.OutputTokens >= result.Limits.MaxOutputTokens || usage.TotalTokens >= result.Limits.MaxTotalTokens || result.ProviderAttempts >= result.Limits.MaxProviderCalls
	if (overBudget && result.ErrorCode != "provider_budget_exceeded") || (result.ErrorCode == "provider_budget_exceeded" && !budgetExhausted) {
		return ErrTrialArtifact
	}
	var summedInput, summedOutput, summedTotal uint64
	for _, exchange := range result.Exchanges {
		exchangeTotal, exchangeOK := checkedAdd(exchange.Usage.InputTokens, exchange.Usage.OutputTokens)
		if !exchangeOK || exchange.StatusCode < 100 || exchange.StatusCode > 599 || !validDigest(exchange.RequestDigest) || !validDigest(exchange.ResponseDigest) ||
			exchange.Usage.TotalTokens != exchangeTotal {
			return ErrTrialArtifact
		}
		var ok bool
		if summedInput, ok = checkedAdd(summedInput, exchange.Usage.InputTokens); !ok {
			return ErrTrialArtifact
		}
		if summedOutput, ok = checkedAdd(summedOutput, exchange.Usage.OutputTokens); !ok {
			return ErrTrialArtifact
		}
		if summedTotal, ok = checkedAdd(summedTotal, exchange.Usage.TotalTokens); !ok {
			return ErrTrialArtifact
		}
	}
	if summedInput != usage.InputTokens || summedOutput != usage.OutputTokens || summedTotal != usage.TotalTokens {
		return ErrTrialArtifact
	}
	if result.Route != nil {
		if len(result.Exchanges) == 0 || (len(result.Exchanges) == 1 && result.ErrorCode == "") || result.Route.RouterUsage != result.Exchanges[0].Usage ||
			result.Route.RouterUsage.InputTokens+result.Route.ExecutionUsage.InputTokens != usage.InputTokens ||
			result.Route.RouterUsage.OutputTokens+result.Route.ExecutionUsage.OutputTokens != usage.OutputTokens ||
			result.Route.RouterUsage.TotalTokens+result.Route.ExecutionUsage.TotalTokens != usage.TotalTokens {
			return ErrTrialArtifact
		}
	}
	if result.Condition == ConditionDirect {
		if result.PythonAttempts != 0 || result.PythonRuns != 0 || len(result.PythonEvidence) != 0 {
			return ErrTrialArtifact
		}
	} else if result.PythonRuns != uint32(len(result.PythonEvidence)) {
		return ErrTrialArtifact
	}
	previousPythonTurn := -1
	duplicatePythonTurns := 0
	for _, evidence := range result.PythonEvidence {
		if evidence.CapabilityCalls > result.Limits.MaxToolCalls || !validDigest(evidence.RequestDigest) ||
			!validDigest(evidence.ResponseDigest) || !validDigest(evidence.ResultDigest) ||
			evidence.Backend == "" || evidence.ResetMode != "fresh-instance" || len(evidence.Observation) != 0 ||
			evidence.Success != (evidence.ErrorCode == "") || (evidence.Success && evidence.FailureClass != "") ||
			(!evidence.Success && result.Version == "agentic-development-trial/v1" && evidence.FailureClass != "") ||
			(!evidence.Success && result.Version == "agentic-development-trial/v2" && evidence.FailureClass != "" && !evidence.FailureClass.valid()) ||
			(!evidence.Success && result.Version == "agentic-development-trial/v3" && !evidence.FailureClass.valid()) ||
			(result.Version == "agentic-development-trial/v3" && (evidence.Turn < 0 || evidence.Turn >= 32 || evidence.Turn < previousPythonTurn)) {
			return ErrTrialArtifact
		}
		if result.Version == "agentic-development-trial/v3" && evidence.Turn == previousPythonTurn {
			duplicatePythonTurns++
		}
		previousPythonTurn = evidence.Turn
	}
	if result.Version == "agentic-development-trial/v1" {
		if result.FailureDetail != nil {
			return ErrTrialArtifact
		}
	} else if result.ErrorCode == "python_guest_error" && (result.Version == "agentic-development-trial/v3" || result.FailureDetail != nil) {
		if result.FailureDetail == nil || !result.FailureDetail.Class.valid() || result.FailureDetail.Turn < 0 || result.FailureDetail.Turn >= 32 || len(result.PythonEvidence) == 0 {
			return ErrTrialArtifact
		}
		failure := result.PythonEvidence[len(result.PythonEvidence)-1]
		expected := failureDetailForPython(failure)
		if failure.Success || result.FailureDetail.Turn != failure.Turn || result.FailureDetail.Class != failure.FailureClass ||
			result.FailureDetail.CapabilityCallsBefore != failure.CapabilityCalls || result.FailureDetail.RetryEligible != expected.RetryEligible {
			return ErrTrialArtifact
		}
	} else if result.FailureDetail != nil {
		return ErrTrialArtifact
	}
	if result.TreatmentID != TreatmentPythonSafeRepairV1 {
		if result.Repair != nil || duplicatePythonTurns != 0 {
			return ErrTrialArtifact
		}
	} else if result.Repair == nil {
		if duplicatePythonTurns != 0 {
			return ErrTrialArtifact
		}
	} else {
		repair := result.Repair
		if !repair.Offered || repair.Succeeded && !repair.Attempted || repair.Turn < 0 || repair.Turn >= 32 ||
			!repair.OriginalFailureClass.valid() || repair.OriginalFailureClass == FailureClassHostToolError ||
			repair.CapabilityCallsBefore != 0 || !validDigest(repair.OriginalFailureDigest) {
			return ErrTrialArtifact
		}
		originalIndex := -1
		for index, evidence := range result.PythonEvidence {
			encoded, err := json.Marshal(evidence)
			if err == nil && digest(encoded) == repair.OriginalFailureDigest {
				if originalIndex != -1 {
					return ErrTrialArtifact
				}
				originalIndex = index
			}
		}
		if originalIndex < 0 {
			return ErrTrialArtifact
		}
		original := result.PythonEvidence[originalIndex]
		if original.Success || original.Turn != repair.Turn || original.FailureClass != repair.OriginalFailureClass || original.CapabilityCalls != 0 {
			return ErrTrialArtifact
		}
		if repair.Attempted {
			if originalIndex+1 < len(result.PythonEvidence) && result.PythonEvidence[originalIndex+1].Turn == repair.Turn {
				repaired := result.PythonEvidence[originalIndex+1]
				failedAfterGuestSuccess := !repair.Succeeded && repaired.Success && originalIndex+1 == len(result.PythonEvidence)-1 &&
					(result.ErrorCode == "python_trace_mismatch" || result.ErrorCode == "invalid_tool_observation")
				if duplicatePythonTurns != 1 || (repair.Succeeded != repaired.Success && !failedAfterGuestSuccess) {
					return ErrTrialArtifact
				}
			} else if duplicatePythonTurns != 0 || result.ErrorCode != "python_engine_failure" || repair.Succeeded {
				return ErrTrialArtifact
			}
		} else if duplicatePythonTurns != 0 || repair.Succeeded {
			return ErrTrialArtifact
		}
	}
	stateful := result.StatefulScore != nil
	stateless := result.StatelessScore != nil
	if stateful == stateless {
		return ErrTrialArtifact
	}
	scorePassed := false
	if stateful {
		if !validDigest(result.InitialStateDigest) || !validDigest(result.FinalStateDigest) ||
			result.StatefulScore.ExpectedCalls < 0 || result.StatefulScore.ActualCalls != result.ToolCalls ||
			!validDigest(result.StatefulScore.ExpectedStateDigest) || !validDigest(result.StatefulScore.ActualStateDigest) ||
			result.StatefulScore.ActualStateDigest != result.FinalStateDigest ||
			result.StatefulScore.FinalStatePassed != (result.StatefulScore.ExpectedStateDigest == result.StatefulScore.ActualStateDigest) ||
			result.StatefulScore.Passed != (result.StatefulScore.TracePassed && result.StatefulScore.FinalStatePassed) {
			return ErrTrialArtifact
		}
		scorePassed = result.StatefulScore.Passed
	} else {
		if result.InitialStateDigest != "" || result.FinalStateDigest != "" ||
			result.StatelessScore.Passed != (result.StatelessScore.ErrorCode == "") {
			return ErrTrialArtifact
		}
		scorePassed = result.StatelessScore.Passed
	}
	if result.Passed != (result.ErrorCode == "" && scorePassed) || (result.ErrorCode != "" && !providerToolNamePattern.MatchString(result.ErrorCode)) {
		return ErrTrialArtifact
	}
	derivedMetrics, metricsErr := DeriveTrialMetrics(result)
	if metricsErr != nil || (result.Version == "agentic-development-trial/v1" && result.Metrics != nil) ||
		(result.Version != "agentic-development-trial/v1" && (result.Metrics == nil || !trialMetricsEqual(*result.Metrics, derivedMetrics))) {
		return ErrTrialArtifact
	}
	return nil
}

func validExecutionIdentity(identity ExecutionIdentity, condition Condition) bool {
	observedAt, observedErr := time.Parse(time.RFC3339, identity.ProviderCatalogObservedAt)
	if !validLowerHex(identity.RepositoryCommit, 40) || !validDigest(identity.HostArtifactDigest) || !validDigest(identity.DatasetManifestDigest) ||
		!validDigest(identity.ProviderCatalogDigest) || observedErr != nil || observedAt.Location() != time.UTC {
		return false
	}
	if condition == ConditionDirect {
		return identity.GuestArtifactDigest == "" && identity.GuestProfile == ""
	}
	return validDigest(identity.GuestArtifactDigest) && (identity.GuestProfile == "core" || identity.GuestProfile == "numpy-core")
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func WriteTrialArtifact(path string, result TrialResult) (string, error) {
	if path == "" || ValidateTrialResult(result) != nil {
		return "", ErrTrialArtifact
	}
	content, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", ErrTrialArtifact
	}
	content = append(content, '\n')
	parent := filepath.Dir(path)
	if info, err := os.Lstat(parent); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrTrialArtifact
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remove = false
	return digest(content), nil
}
