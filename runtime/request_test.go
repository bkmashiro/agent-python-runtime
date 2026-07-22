package runtime_test

import (
	"testing"
	"time"

	runtime "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestDecodeRunRequestStrict(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		req, err := runtime.DecodeRunRequest([]byte(`{"run_id":"run-1","code":"result = inputs","inputs":{"x":1}}`))
		if err != nil {
			t.Fatal(err)
		}
		if req.RunID != "run-1" || string(req.Inputs) != `{"x":1}` {
			t.Fatalf("unexpected request: %#v", req)
		}
	})

	for name, body := range map[string]string{
		"capability": `{"run_id":"run-1","code":"result=1","inputs":{},"capabilities":["fetch_many"]}`,
		"budget":     `{"run_id":"run-1","code":"result=1","inputs":{},"timeout_ms":999999}`,
		"trailing":   `{"run_id":"run-1","code":"result=1","inputs":{}} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.DecodeRunRequest([]byte(body)); err == nil {
				t.Fatal("expected strict request rejection")
			}
		})
	}
}

func TestRunConfigValidation(t *testing.T) {
	config := runtime.DefaultRunConfig()
	if err := config.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if config.Timeout <= 0 || config.MaxRequestBytes <= 0 || config.MaxResponseBytes <= 0 {
		t.Fatalf("defaults must be bounded: %#v", config)
	}

	for name, mutate := range map[string]func(*runtime.RunConfig){
		"timeout":  func(c *runtime.RunConfig) { c.Timeout = 0 },
		"request":  func(c *runtime.RunConfig) { c.MaxRequestBytes = 0 },
		"response": func(c *runtime.RunConfig) { c.MaxResponseBytes = 0 },
		"memory":   func(c *runtime.RunConfig) { c.MemoryLimitPages = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := config
			mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatal("expected invalid config")
			}
		})
	}

	tooLong := config
	tooLong.Timeout = 10 * time.Minute
	if err := tooLong.Validate(); err == nil {
		t.Fatal("timeout above hard ceiling must fail")
	}
}
