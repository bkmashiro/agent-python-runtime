//go:build darwin || linux

package workspacecapsule

import "testing"

func TestOpenDescriptorCountIsAvailable(t *testing.T) {
	count, available := openDescriptorCount()
	if !available || count < 3 {
		t.Fatalf("descriptor count=%d available=%v", count, available)
	}
}
