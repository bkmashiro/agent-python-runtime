package labview

import "testing"

func TestCloneSlicePreservesEmptyListPresence(t *testing.T) {
	cloned := cloneSlice([]string{})
	if cloned == nil || len(cloned) != 0 {
		t.Fatalf("empty semantic projection list was not preserved: %#v", cloned)
	}
}
