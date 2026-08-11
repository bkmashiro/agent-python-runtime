package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestOperatorConfigOnlyAcceptsPoCResourcePolicy(t *testing.T) {
	config, err := decodeOperatorConfig([]byte(`{"timeout_ms":50,"max_request_bytes":2048}`))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := config.resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Timeout != 50*time.Millisecond || resolved.MaxRequestBytes != 2048 {
		t.Fatalf("unexpected config: %#v", resolved)
	}
	if _, err := decodeOperatorConfig([]byte(`{"prepared_capacity":1}`)); err == nil {
		t.Fatal("removed lifecycle policy was accepted")
	}
	if _, err := decodeOperatorConfig([]byte(`{"transaction_journal_path":"/tmp/journal"}`)); err == nil {
		t.Fatal("removed transaction journal was accepted")
	}
}

func TestExecuteRequiresArtifact(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exit := execute(nil, strings.NewReader("{}"), &stdout, &stderr, dependencies{})
	if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}
