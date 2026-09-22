//go:build !linux

package perfdiag

import "errors"

func COWStorage() (int, int64, error) {
	return 0, 0, errors.New("COW storage accounting requires Linux")
}
