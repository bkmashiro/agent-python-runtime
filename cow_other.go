//go:build !linux

package pysolate

import "errors"

func newCOWRuntime() (cowRuntime, error) {
	return nil, errors.New("prepared COW requires Linux")
}
