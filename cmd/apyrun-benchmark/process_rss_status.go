package main

import (
	"bufio"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func errorsForProcessRSS(message string) error { return fmt.Errorf("process RSS: %s", message) }

func parseLinuxProcessRSSStatus(content []byte) (uint64, error) {
	scanner := bufio.NewScanner(strings.NewReader(string(content)))
	state := ""
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "State:" {
			state = fields[1]
			continue
		}
		if len(fields) == 0 || fields[0] != "VmRSS:" {
			continue
		}
		if len(fields) != 3 || fields[2] != "kB" {
			return 0, errorsForProcessRSS("invalid VmRSS")
		}
		kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || kilobytes > math.MaxUint64/1024 {
			return 0, errorsForProcessRSS("invalid VmRSS")
		}
		return kilobytes * 1024, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if state == "Z" || state == "X" || state == "x" {
		return 0, fmt.Errorf("%w: state %s", errProcessRSSExited, state)
	}
	return 0, errorsForProcessRSS("VmRSS is missing")
}
