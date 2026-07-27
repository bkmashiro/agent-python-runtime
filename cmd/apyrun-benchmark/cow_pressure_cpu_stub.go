//go:build !linux

package main

import "errors"

type pressureCPUPoint struct {
	userNS   uint64
	systemNS uint64
}

func collectPressureCPU() (pressureCPUPoint, error) {
	return pressureCPUPoint{}, errors.New("process CPU usage is only supported for Linux cow-pressure evidence")
}
