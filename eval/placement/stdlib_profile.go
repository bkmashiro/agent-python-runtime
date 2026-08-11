package placement

import "sort"

// AgentStdlibWorkspacePolicyID names the model-facing compatibility contract.
// The underlying verified distribution artifact remains the runtime "base"
// profile; this policy is the Host-curated, authority-bounded import subset used
// for agent-generated programs.
const AgentStdlibWorkspacePolicyID = "stdlib-workspace-v1"

var agentStdlibWorkspaceImports = []string{
	"agent_runtime",
	"base64",
	"collections",
	"csv",
	"datetime",
	"decimal",
	"fractions",
	"functools",
	"hashlib",
	"itertools",
	"json",
	"math",
	"pathlib",
	"re",
	"statistics",
	"sys",
	"xml",
}

func AgentStdlibWorkspaceImports() []string {
	return append([]string(nil), agentStdlibWorkspaceImports...)
}

func AgentStdlibWorkspaceAllows(root string) bool {
	index := sort.SearchStrings(agentStdlibWorkspaceImports, root)
	return index < len(agentStdlibWorkspaceImports) && agentStdlibWorkspaceImports[index] == root
}
