package agentic

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func TestSupportedResponsesProtocolIncludesCodexCLIWithoutWeakeningUnknownProtocols(t *testing.T) {
	if !supportedResponsesProtocol(provider.LinkAPIResponsesProtocol) || !supportedResponsesProtocol(provider.CodexCLIProtocol) {
		t.Fatal("expected normalized Responses transports")
	}
	if supportedResponsesProtocol("unknown") || supportedResponsesProtocol("") {
		t.Fatal("unknown protocol admitted")
	}
}
