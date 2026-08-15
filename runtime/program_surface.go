package runtime

import "github.com/bkmashiro/agent-python-runtime/runtime/capability"

// ProgramSurfaceMode is a Host-owned presentation choice. It does not select a
// physical execution backend and does not grant authority.
type ProgramSurfaceMode = capability.ProgramSurfaceMode

const (
	ProgramSurfaceDirect       = capability.ProgramSurfaceDirect
	ProgramSurfaceProgrammatic = capability.ProgramSurfaceProgrammatic
	ProgramSurfaceBoth         = capability.ProgramSurfaceBoth
)
