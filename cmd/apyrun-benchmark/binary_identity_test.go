package main

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestBinarySourceIdentityFromSettingsIsFailClosed(t *testing.T) {
	revision := strings.Repeat("a", 40)
	identity, err := binarySourceIdentityFromSettings([]debug.BuildSetting{
		{Key: "vcs.revision", Value: revision},
		{Key: "vcs.modified", Value: "false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if identity.Revision != revision || identity.Modified {
		t.Fatalf("identity = %#v", identity)
	}

	cases := map[string][]debug.BuildSetting{
		"missing revision": {{Key: "vcs.modified", Value: "false"}},
		"missing modified": {{Key: "vcs.revision", Value: revision}},
		"dirty": {
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "true"},
		},
		"uppercase": {
			{Key: "vcs.revision", Value: strings.Repeat("A", 40)},
			{Key: "vcs.modified", Value: "false"},
		},
		"duplicate revision": {
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	for name, settings := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := binarySourceIdentityFromSettings(settings); err == nil {
				t.Fatal("invalid build identity accepted")
			}
		})
	}
}
