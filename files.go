package pysolate

import (
	"context"
	"errors"
	"fmt"

	"github.com/bkmashiro/agent-python-runtime/internal/versionmount"
	"github.com/bkmashiro/agent-python-runtime/runtime/fsrecord"
	"github.com/bkmashiro/agent-python-runtime/runtime/versionfs"
)

// FileRunOptions optionally records Host FS adapter operations. The recorder is
// caller-created for this exact View and must not be shared between attempts.
type FileRunOptions struct{ Recorder fsrecord.Sink }

// RunFiles grants a read-only immutable input view at /workspace. Host updates
// published after acquiring view do not change this attempt's files. Use New or
// a workspace-prepared constructor; a plain prepared image has different preopens.
// RunFiles finalizes a supplied recorder, including on validation/execution errors.
// FS recording is independent of tool recording and is not an offline replay API.
func (r *Runner) RunFiles(ctx context.Context, source string, inputs any, view *versionfs.View, options ...FileRunOptions) (out Output, err error) {
	if len(options) > 1 {
		return Output{}, errors.New("RunFiles accepts at most one FileRunOptions")
	}
	var sink fsrecord.Sink
	if len(options) == 1 {
		sink = options[0].Recorder
	}
	if sink != nil {
		defer func() {
			if e := sink.Finish(err); e != nil {
				out.Value = nil
				err = errors.Join(err, fmt.Errorf("filesystem recording: %w", e))
			}
		}()
	}
	if (r.image != nil || r.cow != nil) && !r.workspaceImage {
		return Output{}, errors.New("prepared runner was not captured for workspace mounts")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	mount, e := versionmount.New(view, sink, cancel)
	if e != nil {
		return Output{}, e
	}
	config, e := workspaceFSConfig(mount)
	if e != nil {
		return Output{}, e
	}
	out, err = r.run(runCtx, source, inputs, runOptions{fsConfig: config})
	// The Guest and its handles have closed before checking and finalizing the log.
	// A caught OSError must never turn a recording failure into a successful result.
	if e = mount.Err(); e != nil {
		out.Value = nil
		err = errors.Join(err, fmt.Errorf("filesystem recording: %w", e))
	}
	return out, err
}
