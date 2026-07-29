package scheduler

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var (
	ErrInvalidLiveMemoryConfig   = errors.New("invalid live memory config")
	ErrInvalidLiveMemorySnapshot = errors.New("invalid live memory snapshot")
)

type MemoryEvents struct {
	High    uint64
	Max     uint64
	OOM     uint64
	OOMKill uint64
}

type PSIRecord struct {
	Avg10BPS    uint32
	Avg60BPS    uint32
	Avg300BPS   uint32
	TotalMicros uint64
}

type MemoryPSI struct {
	Some PSIRecord
	Full PSIRecord
}

type LiveMemorySnapshot struct {
	SampledAt    time.Time
	CurrentBytes uint64
	MaximumBytes uint64
	Events       MemoryEvents
	Pressure     MemoryPSI
}

func (snapshot LiveMemorySnapshot) Validate() error {
	if snapshot.SampledAt.IsZero() || snapshot.MaximumBytes == 0 ||
		snapshot.Pressure.Some.Avg10BPS > 10000 || snapshot.Pressure.Some.Avg60BPS > 10000 || snapshot.Pressure.Some.Avg300BPS > 10000 ||
		snapshot.Pressure.Full.Avg10BPS > 10000 || snapshot.Pressure.Full.Avg60BPS > 10000 || snapshot.Pressure.Full.Avg300BPS > 10000 {
		return ErrInvalidLiveMemorySnapshot
	}
	return nil
}

func (snapshot LiveMemorySnapshot) UtilizationBPS() uint32 {
	if snapshot.MaximumBytes == 0 {
		return 0
	}
	if snapshot.CurrentBytes >= snapshot.MaximumBytes {
		return 10000
	}
	high, low := bits.Mul64(snapshot.CurrentBytes, 10000)
	quotient, _ := bits.Div64(high, low, snapshot.MaximumBytes)
	return uint32(quotient)
}

type CgroupV2MemoryReaderConfig struct {
	Root     string
	ReadFile func(string) ([]byte, error)
	Clock    func() time.Time
}

type CgroupV2MemoryReader struct {
	root     string
	readFile func(string) ([]byte, error)
	clock    func() time.Time
}

func NewCgroupV2MemoryReader(config CgroupV2MemoryReaderConfig) (*CgroupV2MemoryReader, error) {
	if config.Root == "" || !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root {
		return nil, ErrInvalidLiveMemoryConfig
	}
	if config.ReadFile == nil {
		config.ReadFile = os.ReadFile
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &CgroupV2MemoryReader{root: config.Root, readFile: config.ReadFile, clock: config.Clock}, nil
}

func (reader *CgroupV2MemoryReader) Read() (LiveMemorySnapshot, error) {
	if reader == nil || reader.readFile == nil || reader.clock == nil {
		return LiveMemorySnapshot{}, ErrInvalidLiveMemoryConfig
	}
	read := func(name string) ([]byte, error) {
		value, err := reader.readFile(filepath.Join(reader.root, name))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		return value, nil
	}
	current, err := read("memory.current")
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	maximum, err := read("memory.max")
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	events, err := read("memory.events")
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	pressure, err := read("memory.pressure")
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	return ParseCgroupV2MemorySnapshot(current, maximum, events, pressure, reader.clock().UTC())
}

func ParseCgroupV2MemorySnapshot(current, maximum, events, pressure []byte, sampledAt time.Time) (LiveMemorySnapshot, error) {
	currentBytes, err := parseUnsignedFile(current)
	if err != nil {
		return LiveMemorySnapshot{}, ErrInvalidLiveMemorySnapshot
	}
	if strings.TrimSpace(string(maximum)) == "max" {
		return LiveMemorySnapshot{}, ErrInvalidLiveMemorySnapshot
	}
	maximumBytes, err := parseUnsignedFile(maximum)
	if err != nil || maximumBytes == 0 {
		return LiveMemorySnapshot{}, ErrInvalidLiveMemorySnapshot
	}
	parsedEvents, err := parseMemoryEvents(events)
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	parsedPressure, err := parseMemoryPSI(pressure)
	if err != nil {
		return LiveMemorySnapshot{}, err
	}
	snapshot := LiveMemorySnapshot{SampledAt: sampledAt.UTC(), CurrentBytes: currentBytes, MaximumBytes: maximumBytes, Events: parsedEvents, Pressure: parsedPressure}
	if err := snapshot.Validate(); err != nil {
		return LiveMemorySnapshot{}, err
	}
	return snapshot, nil
}

func parseUnsignedFile(payload []byte) (uint64, error) {
	value := strings.TrimSpace(string(payload))
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, ErrInvalidLiveMemorySnapshot
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, ErrInvalidLiveMemorySnapshot
	}
	return parsed, nil
}

func parseMemoryEvents(payload []byte) (MemoryEvents, error) {
	values := make(map[string]uint64)
	for _, line := range strings.Split(strings.TrimSpace(string(payload)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return MemoryEvents{}, ErrInvalidLiveMemorySnapshot
		}
		if _, duplicate := values[fields[0]]; duplicate {
			return MemoryEvents{}, ErrInvalidLiveMemorySnapshot
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return MemoryEvents{}, ErrInvalidLiveMemorySnapshot
		}
		values[fields[0]] = value
	}
	for _, required := range []string{"high", "max", "oom", "oom_kill"} {
		if _, ok := values[required]; !ok {
			return MemoryEvents{}, ErrInvalidLiveMemorySnapshot
		}
	}
	return MemoryEvents{High: values["high"], Max: values["max"], OOM: values["oom"], OOMKill: values["oom_kill"]}, nil
}

func parseMemoryPSI(payload []byte) (MemoryPSI, error) {
	var result MemoryPSI
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSpace(string(payload)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || seen[fields[0]] || fields[0] != "some" && fields[0] != "full" {
			return MemoryPSI{}, ErrInvalidLiveMemorySnapshot
		}
		seen[fields[0]] = true
		values := make(map[string]string)
		for _, field := range fields[1:] {
			parts := strings.SplitN(field, "=", 2)
			if len(parts) != 2 || values[parts[0]] != "" {
				return MemoryPSI{}, ErrInvalidLiveMemorySnapshot
			}
			values[parts[0]] = parts[1]
		}
		record, err := parsePSIRecord(values)
		if err != nil {
			return MemoryPSI{}, err
		}
		if fields[0] == "some" {
			result.Some = record
		} else {
			result.Full = record
		}
	}
	if !seen["some"] || !seen["full"] {
		return MemoryPSI{}, ErrInvalidLiveMemorySnapshot
	}
	return result, nil
}

func parsePSIRecord(values map[string]string) (PSIRecord, error) {
	avg10, err := parsePercentBPS(values["avg10"])
	if err != nil {
		return PSIRecord{}, err
	}
	avg60, err := parsePercentBPS(values["avg60"])
	if err != nil {
		return PSIRecord{}, err
	}
	avg300, err := parsePercentBPS(values["avg300"])
	if err != nil {
		return PSIRecord{}, err
	}
	total, err := strconv.ParseUint(values["total"], 10, 64)
	if err != nil {
		return PSIRecord{}, ErrInvalidLiveMemorySnapshot
	}
	return PSIRecord{Avg10BPS: avg10, Avg60BPS: avg60, Avg300BPS: avg300, TotalMicros: total}, nil
}

func parsePercentBPS(value string) (uint32, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" {
		return 0, ErrInvalidLiveMemorySnapshot
	}
	whole, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil || whole > 100 {
		return 0, ErrInvalidLiveMemorySnapshot
	}
	fraction := uint64(0)
	if len(parts) == 2 {
		if len(parts[1]) == 0 || len(parts[1]) > 2 {
			return 0, ErrInvalidLiveMemorySnapshot
		}
		fraction, err = strconv.ParseUint(parts[1], 10, 32)
		if err != nil {
			return 0, ErrInvalidLiveMemorySnapshot
		}
		if len(parts[1]) == 1 {
			fraction *= 10
		}
	}
	if whole == 100 && fraction != 0 {
		return 0, ErrInvalidLiveMemorySnapshot
	}
	return uint32(whole*100 + fraction), nil
}

func DiscoverCgroupV2MemoryRoot(procSelfCgroup []byte, sysfsRoot string) (string, error) {
	if !filepath.IsAbs(sysfsRoot) || filepath.Clean(sysfsRoot) != sysfsRoot {
		return "", ErrInvalidLiveMemoryConfig
	}
	var unified string
	for _, line := range bytes.Split(bytes.TrimSpace(procSelfCgroup), []byte{'\n'}) {
		parts := strings.SplitN(string(line), ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			if unified != "" {
				return "", ErrInvalidLiveMemoryConfig
			}
			unified = parts[2]
		}
	}
	if unified == "" || !strings.HasPrefix(unified, "/") {
		return "", ErrInvalidLiveMemoryConfig
	}
	for _, segment := range strings.Split(unified, "/") {
		if segment == ".." || segment == "." {
			return "", ErrInvalidLiveMemoryConfig
		}
	}
	joined := filepath.Join(sysfsRoot, strings.TrimPrefix(unified, "/"))
	relative, err := filepath.Rel(sysfsRoot, joined)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", ErrInvalidLiveMemoryConfig
	}
	return joined, nil
}
