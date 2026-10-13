package versionfs

import "testing"

func TestDirectoryVersionCoversEntryInfo(t *testing.T) {
	s, _ := New(DefaultLimits())
	_ = s.Mkdir("rules")
	_ = s.WriteFile("rules/a", []byte("x"))
	old := s.View()
	before, _ := old.Identity("rules")
	_ = s.WriteFile("rules/a", []byte("longer"))
	current := s.View()
	after, _ := current.Identity("rules")
	if before == after {
		t.Fatal("directory version reused although ReadDir entry Info changed")
	}
	entries, _ := old.ReadDir("rules")
	info, _ := entries[0].Info()
	if info.Size() != 1 {
		t.Fatal("old directory entries changed")
	}
}
