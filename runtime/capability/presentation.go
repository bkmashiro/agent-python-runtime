package capability

import "errors"

// ProgramSurfaceMode controls how one Host capability Plan is presented to a
// model. It is presentation only: every invocation still re-enters the same
// Broker and Plan.
type ProgramSurfaceMode string

const (
	ProgramSurfaceDirect       ProgramSurfaceMode = "direct"
	ProgramSurfaceProgrammatic ProgramSurfaceMode = "programmatic"
	ProgramSurfaceBoth         ProgramSurfaceMode = "both"
)

var ErrInvalidProgramSurface = errors.New("invalid program surface")

// Presentation is a detached projection of one sealed Plan. Direct tools and
// the programmatic Python prelude never contain handlers or grant policy bytes.
type Presentation struct {
	Mode          ProgramSurfaceMode
	Tools         []ToolSchema
	PythonPrelude string
	ParentCallID  string
}

func (plan *Plan) Present(mode ProgramSurfaceMode, parentCallID string) (Presentation, error) {
	if plan == nil || plan.Identity() == "" {
		return Presentation{}, ErrInvalidProgramSurface
	}
	presentation := Presentation{Mode: mode}
	switch mode {
	case ProgramSurfaceDirect:
		if parentCallID != "" {
			return Presentation{}, ErrInvalidProgramSurface
		}
		presentation.Tools = plan.ToolSchemas()
	case ProgramSurfaceProgrammatic, ProgramSurfaceBoth:
		if !validProgrammaticParentCallID(parentCallID) {
			return Presentation{}, ErrInvalidProgramSurface
		}
		presentation.ParentCallID = parentCallID
		presentation.PythonPrelude = generateProgrammaticPythonPrelude(plan.specs, parentCallID)
		if mode == ProgramSurfaceBoth {
			presentation.Tools = plan.ToolSchemas()
		}
	default:
		return Presentation{}, ErrInvalidProgramSurface
	}
	return presentation, nil
}

func validProgrammaticParentCallID(value string) bool {
	return len(value) > 0 && len(value) <= 96 && validIdentity(value)
}
