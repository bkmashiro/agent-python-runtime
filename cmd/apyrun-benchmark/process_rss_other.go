//go:build !linux && !darwin

package main

import "errors"

func defaultProcessRSSBytes(int) (uint64, error) {
	return 0, errors.New("process RSS collection is unsupported on this platform")
}
