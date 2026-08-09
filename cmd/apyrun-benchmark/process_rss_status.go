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
	stateSeen := false
	stateValid := false
	kthreadSeen := false
	kthreadValid := false
	userTask := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 && fields[0] == "State:" {
			if stateSeen {
				stateValid = false
				continue
			}
			stateSeen = true
			if len(fields) < 3 {
				continue
			}
			state = fields[1]
			labels := map[string]string{
				"R": "(running)", "S": "(sleeping)", "D": "(disk sleep)",
				"T": "(stopped)", "t": "(tracing stop)", "X": "(dead)",
				"x": "(dead)", "Z": "(zombie)", "P": "(parked)", "I": "(idle)",
			}
			expected, known := labels[state]
			stateValid = known && strings.Join(fields[2:], " ") == expected
			continue
		}
		if len(fields) > 0 && fields[0] == "Kthread:" {
			if kthreadSeen {
				kthreadValid = false
				continue
			}
			kthreadSeen = true
			if len(fields) != 2 || (fields[1] != "0" && fields[1] != "1") {
				continue
			}
			kthreadValid = true
			userTask = fields[1] == "0"
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
	if !stateSeen || !stateValid {
		return 0, errorsForProcessRSS("VmRSS is missing without valid process state")
	}
	if !kthreadSeen || !kthreadValid || !userTask {
		return 0, errorsForProcessRSS("VmRSS is missing without user-task identity")
	}
	if state == "Z" || state == "X" || state == "x" {
		return 0, fmt.Errorf("%w: state %s", errProcessRSSExited, state)
	}
	return 0, fmt.Errorf("%w: state %s", errProcessRSSMMReleased, state)
}
