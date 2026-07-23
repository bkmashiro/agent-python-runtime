//go:build darwin

package main

import (
	"errors"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

func defaultProcessRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	kilobytes, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil || kilobytes > math.MaxUint64/1024 {
		return 0, errors.New("process RSS output is invalid")
	}
	return kilobytes * 1024, nil
}
