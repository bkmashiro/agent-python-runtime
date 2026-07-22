package engine

import (
	"errors"
	"strings"
	"testing"
)

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
