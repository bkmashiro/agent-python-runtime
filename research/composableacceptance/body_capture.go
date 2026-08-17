package composableacceptance

import "bytes"

const BodyCaptureSchemaVersion = "pysolate.research-body-capture.v2"

const ProviderIONotApplicable = "not_applicable_scripted_fixture"

type CapturedAgentOutput struct {
	AgentID       string `json:"agent_id"`
	Path          string `json:"path"`
	Disposition   string `json:"disposition"`
	Body          string `json:"body"`
	EventSequence uint32 `json:"event_sequence"`
}

type BodyCapture struct {
	SchemaVersion         string                `json:"schema_version"`
	ScenarioID            string                `json:"scenario_id"`
	ScenarioSHA256        string                `json:"scenario_sha256"`
	TraceSHA256           string                `json:"trace_sha256"`
	ProviderIO            string                `json:"provider_io"`
	WorkflowOutput        string                `json:"workflow_output"`
	WorkflowEventSequence uint32                `json:"workflow_event_sequence"`
	SelectedChildID       string                `json:"selected_child_id"`
	SelectedRootSHA256    string                `json:"selected_root_sha256"`
	AgentOutputs          []CapturedAgentOutput `json:"agent_outputs"`
}

func TraceIdentity(events []TraceEvent) (string, error) {
	encoded, err := encodeCanonical(events)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func (capture BodyCapture) Validate() error {
	if capture.SchemaVersion != BodyCaptureSchemaVersion || !idRE.MatchString(capture.ScenarioID) || !digestRE.MatchString(capture.ScenarioSHA256) || !digestRE.MatchString(capture.TraceSHA256) || capture.ProviderIO != ProviderIONotApplicable || capture.WorkflowOutput == "" || len(capture.WorkflowOutput) > 1<<20 || capture.WorkflowEventSequence < 1 || !idRE.MatchString(capture.SelectedChildID) || !digestRE.MatchString(capture.SelectedRootSHA256) || len(capture.AgentOutputs) != 2 {
		return ErrInvalid
	}
	seenAgents := map[string]bool{}
	seenSequences := map[uint32]bool{capture.WorkflowEventSequence: true}
	selected := 0
	for _, output := range capture.AgentOutputs {
		if !idRE.MatchString(output.AgentID) || output.Path == "" || output.Body == "" || len(output.Body) > 1<<20 || output.EventSequence < 1 || seenAgents[output.AgentID] || seenSequences[output.EventSequence] || (output.Disposition != "selected_branch" && output.Disposition != "discarded_branch") {
			return ErrInvalid
		}
		if output.Disposition == "selected_branch" {
			selected++
			if output.AgentID != capture.SelectedChildID {
				return ErrInvalid
			}
		}
		seenAgents[output.AgentID] = true
		seenSequences[output.EventSequence] = true
	}
	if selected != 1 || !seenAgents[capture.SelectedChildID] {
		return ErrInvalid
	}
	return nil
}

func EncodeBodyCapture(capture BodyCapture) ([]byte, string, error) {
	if err := capture.Validate(); err != nil {
		return nil, "", err
	}
	encoded, err := encodeCanonical(capture)
	if err != nil {
		return nil, "", err
	}
	return encoded, digest(encoded), nil
}

func DecodeBodyCapture(data []byte) (BodyCapture, string, error) {
	var capture BodyCapture
	if decodeStrict(data, &capture) != nil || capture.Validate() != nil {
		return BodyCapture{}, "", ErrInvalid
	}
	canonical, err := encodeCanonical(capture)
	if err != nil || !bytes.Equal(data, canonical) {
		return BodyCapture{}, "", ErrInvalid
	}
	return capture, digest(canonical), nil
}
