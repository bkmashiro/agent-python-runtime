//go:build !linux

package cowmem

import "errors"

func New() (Runtime, error) {
	return nil, errors.New("prepared COW requires Linux")
}
