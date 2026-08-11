package runtime

// BindAgentSource turns ordinary Agent-authored Python into a Host-authored
// compatibility declaration. The Agent never has to maintain import metadata.
func BindAgentSource(request RunRequest, profile *ExecutionProfile) (RunRequest, error) {
	imports, err := InferStaticImportRoots(request.Code)
	if err != nil {
		return RunRequest{}, err
	}
	bound := request
	bound.Requirements = append([]RequiredFeature(nil), request.Requirements...)
	if len(imports) == 0 {
		bound.Compatibility = nil
		return bound, nil
	}
	if profile == nil {
		return RunRequest{}, ErrExecutionProfileUnsupported
	}
	bound.Compatibility = &CompatibilityDeclaration{Profile: profile.ID(), Imports: imports}
	if err := EvaluateRunCompatibility(bound, profile); err != nil {
		return RunRequest{}, err
	}
	return bound, nil
}
