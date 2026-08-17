package wazero

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundedGuestDiagnosticWriter(t *testing.T) {
	writer := &boundedDiagnostic{}
	payload := []byte(strings.Repeat("x", guestDiagnosticMax*4))
	if written, err := writer.Write(payload); err != nil || written != len(payload) {
		t.Fatalf("write=(%d,%v)", written, err)
	}
	if got := writer.String(); len(got) > guestDiagnosticMax+64 || !strings.Contains(got, "[guest stderr truncated]") {
		t.Fatalf("unbounded or unmarked diagnostic: len=%d suffix=%q", len(got), got[len(got)-32:])
	}
	writer.Reset()
	if got := writer.String(); got != "" {
		t.Fatalf("reset diagnostic=%q", got)
	}
}

func TestWithGuestDiagnostic(t *testing.T) {
	base := errors.New("runtime init failed")
	if got := withGuestDiagnostic(base, ""); got.Error() != base.Error() {
		t.Fatalf("empty diagnostic changed error: %v", got)
	}
	got := withGuestDiagnostic(base, "python path error\n")
	if !strings.Contains(got.Error(), "python path error") {
		t.Fatalf("diagnostic missing: %v", got)
	}
	long := strings.Repeat("x", guestDiagnosticMax+100)
	got = withGuestDiagnostic(base, long)
	if len(got.Error()) > guestDiagnosticMax+len(base.Error())+64 {
		t.Fatalf("diagnostic was not bounded: %d", len(got.Error()))
	}
}
