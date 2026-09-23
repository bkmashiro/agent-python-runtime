package main

import (
	"net/url"
	"strings"
)

func recordingLimits() map[string]int64 {
	return map[string]int64{"host_calls": maxHostCalls, "code_timeout_ns": int64(codeExecutionTimeout), "episode_timeout_ns": int64(episodeTimeout), "model_body_bytes": maxResponseBodyBytes, "context_bytes": maxContextBytes, "source_bytes": maxSourceBytes, "tool_result_bytes": maxToolResultBytes}
}

// Transport credentials are not part of the persisted endpoint binding.
func recordingEndpoint(base string) string {
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/chat/completions")
	if err != nil {
		return "invalid endpoint"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
