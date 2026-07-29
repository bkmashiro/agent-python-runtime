package wazero

import (
	"errors"
	"strings"
	"testing"
)

const splitSMAPSFixture = `00001000-00002000 rw-p 00000000 00:01 1 /memfd:apyrun-cow-image
Size:                  4 kB
Rss:                   4 kB
Pss:                   3 kB
Private_Clean:         1 kB
Private_Dirty:         3 kB
Anonymous:             3 kB
Swap:                  0 kB
VmFlags: rd wr mr mw me ac sd
00002000-00003000 rw-p 00001000 00:01 1 /memfd:apyrun-cow-image
Size:                  4 kB
Rss:                   3 kB
Pss:                   2 kB
Private_Clean:         0 kB
Private_Dirty:         3 kB
Anonymous:             3 kB
Swap:                  1 kB
VmFlags: rd wr mr mw me ac sd
00003000-00004000 r--p 00000000 00:00 0 /other
Size:                  4 kB
Rss:                   1 kB
Pss:                   1 kB
Private_Clean:         1 kB
Private_Dirty:         0 kB
Anonymous:             0 kB
Swap:                  0 kB
`

func TestParseSMAPSAggregatesExactSplitMapping(t *testing.T) {
	footprint, err := parseSMAPSMemory(strings.NewReader(splitSMAPSFixture), 0x1000, 0x3000)
	if err != nil {
		t.Fatal(err)
	}
	if footprint.MappingCount != 2 || footprint.VirtualBytes != 8<<10 || footprint.RSSBytes != 7<<10 ||
		footprint.PSSBytes != 5<<10 || footprint.PrivateCleanBytes != 1<<10 || footprint.PrivateDirtyBytes != 6<<10 ||
		footprint.AnonymousBytes != 6<<10 || footprint.SwapBytes != 1<<10 {
		t.Fatalf("footprint = %#v", footprint)
	}
}

func TestParseSMAPSRejectsGapPartialOverlapAndMissingMetrics(t *testing.T) {
	gap := strings.Replace(splitSMAPSFixture, "00002000-00003000", "00003000-00004000", 1)
	if _, err := parseSMAPSMemory(strings.NewReader(gap), 0x1000, 0x4000); !errors.Is(err, errSMAPSIncompleteCoverage) {
		t.Fatalf("gap error = %v", err)
	}
	if _, err := parseSMAPSMemory(strings.NewReader(splitSMAPSFixture), 0x1800, 0x2800); !errors.Is(err, errSMAPSPartialOverlap) {
		t.Fatalf("partial overlap error = %v", err)
	}
	missing := strings.Replace(splitSMAPSFixture, "Anonymous:             3 kB\n", "", 1)
	if _, err := parseSMAPSMemory(strings.NewReader(missing), 0x1000, 0x3000); !errors.Is(err, errSMAPSMalformed) {
		t.Fatalf("missing metric error = %v", err)
	}
	if _, err := parseSMAPSMemory(strings.NewReader(splitSMAPSFixture), 0x5000, 0x6000); !errors.Is(err, errSMAPSMappingNotFound) {
		t.Fatalf("missing mapping error = %v", err)
	}
}

func TestParseSMAPSReportsReaderFailureSeparately(t *testing.T) {
	if _, err := parseSMAPSMemory(failingSMAPSReader{}, 0x1000, 0x2000); !errors.Is(err, errFootprintReadFailed) {
		t.Fatalf("reader failure = %v", err)
	}
}

type failingSMAPSReader struct{}

func (failingSMAPSReader) Read([]byte) (int, error) { return 0, errors.New("synthetic read failure") }
