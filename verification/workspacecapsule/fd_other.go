//go:build !darwin && !linux

package workspacecapsule

func openDescriptorCount() (int, bool) {
	return 0, false
}
