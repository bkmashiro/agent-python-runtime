// Package sourcebinding provides an internal Host-TCB bridge for source
// provenance resolvers. The Go internal-package boundary prevents external
// plugins from minting source-bound receipt evidence.
package sourcebinding

import (
	"encoding/json"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

type Request struct {
	CallID         string
	ParentCallID   string
	Capability     string
	OperationIndex uint32
	Arguments      json.RawMessage
	Programmatic   bool
}

type Authority struct {
	resolve func(Request) (receipt.SourceBinding, bool)
}

func New(resolve func(Request) (receipt.SourceBinding, bool)) Authority {
	return Authority{resolve: resolve}
}

func (authority Authority) Valid() bool { return authority.resolve != nil }

func (authority Authority) Resolve(request Request) (receipt.SourceBinding, bool) {
	if authority.resolve == nil {
		return receipt.SourceBinding{}, false
	}
	request.Arguments = append(json.RawMessage(nil), request.Arguments...)
	return authority.resolve(request)
}
