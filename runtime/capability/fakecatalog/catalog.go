// Package fakecatalog assembles deterministic fake adapter metadata for tests.
// It does not construct providers or resolve credentials.
package fakecatalog

import (
	"errors"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakecloudflare"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

func Build(revision uint64, maxCalls uint32, grantVersion string) (toolcatalog.Snapshot, error) {
	if revision == 0 {
		return toolcatalog.Snapshot{}, errors.New("fake catalog revision must be positive")
	}
	var discovered []toolcatalog.DiscoveredTool
	grants := map[string]toolcatalog.Grant{}
	appendCatalog := func(tools []toolcatalog.DiscoveredTool, values map[string]toolcatalog.Grant, err error) error {
		if err != nil {
			return err
		}
		discovered = append(discovered, tools...)
		for id, grant := range values {
			if _, duplicate := grants[id]; duplicate {
				return errors.New("duplicate fake catalog tool")
			}
			grants[id] = grant
		}
		return nil
	}
	if err := appendCatalog(fakeworkspace.CatalogTools(maxCalls, grantVersion)); err != nil {
		return toolcatalog.Snapshot{}, err
	}
	if err := appendCatalog(fakecloudflare.CatalogTools(maxCalls, grantVersion)); err != nil {
		return toolcatalog.Snapshot{}, err
	}
	if err := appendCatalog(fakemail.CatalogTools(maxCalls, grantVersion)); err != nil {
		return toolcatalog.Snapshot{}, err
	}
	return toolcatalog.BuildSnapshot(discovered, grants, toolcatalog.BuildOptions{Revision: revision, DiscoveredAt: time.Unix(0, 0).UTC()})
}
