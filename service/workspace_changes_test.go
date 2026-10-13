package service

import (
	"net/http"
	"testing"
)

func TestHTTPWorkspaceChangeSummaryIsOptional(t *testing.T) {
	_, server := newGuestHTTPService(t, nil, 1, Options{})
	client := server.Client()
	status, created := requestJSON(t, client, http.MethodPost, server.URL+"/v1/workspaces", map[string]any{
		"files": []map[string]any{{"path": "note.txt", "data": []byte("old")}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %#v", status, created)
	}
	ref := created["workspace"].(string)
	url := server.URL + "/v1/workspaces/" + ref

	for _, tc := range []struct {
		name        string
		flag        any
		set         bool
		wantSummary bool
	}{
		{"omitted", nil, false, true},
		{"explicit", true, true, true},
		{"disabled", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"source": "with open('note.txt', 'w') as f:\n    f.write(inputs['text'])\nresult = 42", "inputs": map[string]any{"text": tc.name}}
			if tc.set {
				body["changes"] = tc.flag
			}
			status, result := requestJSON(t, client, http.MethodPost, url+"/run", body)
			if status != http.StatusOK || result["value"] != float64(42) {
				t.Fatalf("run: %d %#v", status, result)
			}
			for _, field := range []string{"before", "after", "changes"} {
				if _, ok := result[field]; ok != tc.wantSummary {
					t.Fatalf("%s present=%t want=%t: %#v", field, ok, tc.wantSummary, result)
				}
			}
			if tc.wantSummary && result["changes"].(map[string]any)["modified"] != float64(1) {
				t.Fatalf("missing modification: %#v", result)
			}
		})
	}

	status, result := requestJSON(t, client, http.MethodPost, url+"/run", map[string]any{
		"source": "with open('note.txt','w') as f:\n    f.write('kept')\nraise ValueError('expected')", "changes": false,
	})
	if status != http.StatusUnprocessableEntity || result["error"] == nil {
		t.Fatalf("Python failure: %d %#v", status, result)
	}
	if _, ok := result["changes"]; ok {
		t.Fatalf("unexpected summary: %#v", result)
	}
	status, result = requestJSON(t, client, http.MethodPost, url+"/run", map[string]any{
		"source": "result = open('note.txt').read()", "changes": false,
	})
	if status != http.StatusOK || result["value"] != "kept" {
		t.Fatalf("private write/lease lost: %d %#v", status, result)
	}
	status, result = requestJSON(t, client, http.MethodGet, url+"/snapshot", nil)
	if status != http.StatusOK || result["revision"] == nil {
		t.Fatalf("explicit snapshot: %d %#v", status, result)
	}
}
