package semantictrusted

import enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"

// Authority is an in-process Host-TCB wrapper. The internal package boundary keeps
// third-party Runner implementations from minting verified semantic authority.
type Authority struct {
	runner enginecontract.Runner
}

func New(runner enginecontract.Runner) Authority {
	return Authority{runner: runner}
}

func (authority Authority) Runner() enginecontract.Runner {
	return authority.runner
}
