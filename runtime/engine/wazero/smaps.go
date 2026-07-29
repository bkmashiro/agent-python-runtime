package wazero

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	errSMAPSMappingNotFound    = errors.New("smaps target mapping not found")
	errSMAPSIncompleteCoverage = errors.New("smaps target mapping coverage is incomplete")
	errSMAPSPartialOverlap     = errors.New("smaps mapping partially overlaps target")
	errSMAPSMalformed          = errors.New("smaps data is malformed")
)

const maxSMAPSSegments = 1 << 16

type smapsSegment struct {
	start  uint64
	end    uint64
	values map[string]uint64
	seen   map[string]bool
}

var requiredSMAPSMetrics = []string{"Size", "Rss", "Pss", "Private_Clean", "Private_Dirty", "Anonymous", "Swap"}

func parseSMAPSMemory(reader io.Reader, targetStart, targetEnd uint64) (enginecontract.MemoryFootprint, error) {
	if reader == nil || targetStart >= targetEnd {
		return enginecontract.MemoryFootprint{}, errSMAPSMalformed
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	segments := make([]smapsSegment, 0, 1)
	var current *smapsSegment
	flush := func() error {
		if current == nil {
			return nil
		}
		for _, name := range requiredSMAPSMetrics {
			if !current.seen[name] {
				return fmt.Errorf("%w: missing %s", errSMAPSMalformed, name)
			}
		}
		if current.values["Size"] != current.end-current.start {
			return fmt.Errorf("%w: Size does not match mapping range", errSMAPSMalformed)
		}
		if len(segments) >= maxSMAPSSegments {
			return fmt.Errorf("%w: too many target segments", errSMAPSMalformed)
		}
		segments = append(segments, *current)
		current = nil
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		if start, end, ok := parseSMAPSHeader(line); ok {
			if err := flush(); err != nil {
				return enginecontract.MemoryFootprint{}, err
			}
			if start < targetEnd && end > targetStart {
				if start < targetStart || end > targetEnd {
					return enginecontract.MemoryFootprint{}, errSMAPSPartialOverlap
				}
				current = &smapsSegment{start: start, end: end, values: make(map[string]uint64), seen: make(map[string]bool)}
			}
			continue
		}
		if current == nil {
			continue
		}
		name, value, recognized, err := parseSMAPSMetric(line)
		if err != nil {
			return enginecontract.MemoryFootprint{}, err
		}
		if !recognized {
			continue
		}
		if current.seen[name] {
			return enginecontract.MemoryFootprint{}, fmt.Errorf("%w: duplicate %s", errSMAPSMalformed, name)
		}
		current.seen[name] = true
		current.values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return enginecontract.MemoryFootprint{}, fmt.Errorf("%w: scan", errFootprintReadFailed)
	}
	if err := flush(); err != nil {
		return enginecontract.MemoryFootprint{}, err
	}
	if len(segments) == 0 {
		return enginecontract.MemoryFootprint{}, errSMAPSMappingNotFound
	}
	cursor := targetStart
	result := enginecontract.MemoryFootprint{}
	for _, segment := range segments {
		if segment.start != cursor {
			return enginecontract.MemoryFootprint{}, errSMAPSIncompleteCoverage
		}
		cursor = segment.end
		if result.MappingCount == ^uint32(0) {
			return enginecontract.MemoryFootprint{}, errSMAPSMalformed
		}
		result.MappingCount++
		var err error
		result.VirtualBytes, err = addSMAPSValue(result.VirtualBytes, segment.values["Size"])
		if err != nil {
			return enginecontract.MemoryFootprint{}, err
		}
		for name, destination := range map[string]*uint64{
			"Rss": &result.RSSBytes, "Pss": &result.PSSBytes, "Private_Clean": &result.PrivateCleanBytes,
			"Private_Dirty": &result.PrivateDirtyBytes, "Anonymous": &result.AnonymousBytes, "Swap": &result.SwapBytes,
		} {
			*destination, err = addSMAPSValue(*destination, segment.values[name])
			if err != nil {
				return enginecontract.MemoryFootprint{}, err
			}
		}
	}
	if cursor != targetEnd || result.VirtualBytes != targetEnd-targetStart {
		return enginecontract.MemoryFootprint{}, errSMAPSIncompleteCoverage
	}
	return result, nil
}

func parseSMAPSHeader(line string) (uint64, uint64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return 0, 0, false
	}
	rangeParts := strings.Split(fields[0], "-")
	if len(rangeParts) != 2 || len(fields[1]) != 4 {
		return 0, 0, false
	}
	start, startErr := strconv.ParseUint(rangeParts[0], 16, 64)
	end, endErr := strconv.ParseUint(rangeParts[1], 16, 64)
	if startErr != nil || endErr != nil || start >= end {
		return 0, 0, false
	}
	return start, end, true
}

func parseSMAPSMetric(line string) (string, uint64, bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", 0, false, nil
	}
	name := strings.TrimSuffix(fields[0], ":")
	recognized := false
	for _, candidate := range requiredSMAPSMetrics {
		if name == candidate {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", 0, false, nil
	}
	if len(fields) != 3 || fields[2] != "kB" {
		return "", 0, false, fmt.Errorf("%w: invalid %s metric", errSMAPSMalformed, name)
	}
	kilobytes, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || kilobytes > ^uint64(0)/1024 {
		return "", 0, false, fmt.Errorf("%w: invalid %s value", errSMAPSMalformed, name)
	}
	return name, kilobytes * 1024, true, nil
}

func addSMAPSValue(left, right uint64) (uint64, error) {
	if right > ^uint64(0)-left {
		return 0, fmt.Errorf("%w: metric overflow", errSMAPSMalformed)
	}
	return left + right, nil
}
