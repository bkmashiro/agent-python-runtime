package pysolate

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// RunPrefix receives append-only UTF-8 source fragments. Closing chunks commits
// the source for one execution in the same Guest; ctx cancels an unfinished stream.
func (r *Runner) RunPrefix(ctx context.Context, chunks <-chan string, inputs any) (Output, error) {
	if chunks == nil {
		return Output{}, errors.New("nil source stream")
	}
	for _, name := range []string{"prefix_begin", "prefix_feed"} {
		if r.code.ExportedFunctions()[name] == nil {
			return Output{}, fmt.Errorf("missing prefix Guest export: %s", name)
		}
	}
	return r.run(ctx, "", inputs, true, chunks, nil)
}

func receiveSource(ctx context.Context, m api.Module, initial []byte, chunks <-chan string) error {
	status, err := callWithBytes(ctx, m, "prefix_begin", initial)
	if err != nil {
		return err
	}
	if status[0] != 0 {
		return errors.New("prefix initialization failed: ")
	}
	received := len(initial)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				return ctx.Err()
			}
			received += len(chunk)
			if received > maxMessage {
				return errors.New("prefix input exceeds 1 MiB")
			}
			if chunk == "" {
				continue
			}
			status, err = callWithBytes(ctx, m, "prefix_feed", []byte(chunk))
			if err != nil {
				return err
			}
			if status[0] != 0 {
				return errors.New("prefix feed failed: ")
			}
		}
	}
}

// Request memory is temporary for this call; returned Guest response memory, if any,
// is owned by its caller. Shared by execute and the two prefix setup calls.
func callWithBytes(ctx context.Context, m api.Module, name string, data []byte) ([]uint64, error) {
	ptr, err := m.ExportedFunction("alloc").Call(ctx, uint64(len(data)))
	if err != nil {
		return nil, err
	}
	if ptr[0] == 0 || !m.Memory().Write(uint32(ptr[0]), data) {
		return nil, errors.New("Guest request allocation failed")
	}
	defer m.ExportedFunction("release").Call(ctx, ptr[0])
	return m.ExportedFunction(name).Call(ctx, ptr[0], uint64(len(data)))
}
