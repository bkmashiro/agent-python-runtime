package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type modelFunc func(context.Context, []Message, int) (Message, error)

func (f modelFunc) Complete(c context.Context, m []Message, n int) (Message, error) {
	return f(c, m, n)
}
func testConfig(t *testing.T, model ChatModel) AgentConfig {
	return AgentConfig{GuestPath: testGuest(t), OutputPath: filepath.Join(t.TempDir(), "report.md"), Model: model, MaxTurns: 3, Deadline: time.Minute, ExecutionTimeout: 10 * time.Second, MaxResponseBytes: 64 << 10, MaxContextBytes: 1 << 20, MaxOutputBytes: 64 << 10}
}
func TestIntermediateOutputAndPrivateReasoning(t *testing.T) {
	turns := 0
	var trace bytes.Buffer
	config := testConfig(t, modelFunc(func(_ context.Context, m []Message, _ int) (Message, error) {
		turns++
		switch turns {
		case 1:
			reply := toolAnswer("inspect", "print(open('sales.csv').read())")
			reply.ReasoningContent = "private-reasoning"
			return reply, nil
		case 2:
			if !strings.Contains(m[len(m)-1].Content, "sku,amount") {
				t.Fatal("inspection output lost")
			}
			if m[len(m)-2].ReasoningContent != "private-reasoning" {
				t.Fatal("provider continuation lost reasoning")
			}
			return toolAnswer("write", "open('REPORT.md','w').write('rows=3 total=35 widgets=2 gadgets=1')"), nil
		default:
			return Message{Role: "assistant", Content: "Done"}, nil
		}
	}))
	config.Trace = &trace
	result, err := RunAgent(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExecutionAttempts != 2 || result.Turns != 3 {
		t.Fatalf("counts=%+v", result)
	}
	if strings.Contains(trace.String(), "private-reasoning") {
		t.Fatal("reasoning leaked into trace")
	}
}
func TestFailedAttemptCannotPublishEarlierReport(t *testing.T) {
	turn := 0
	config := testConfig(t, modelFunc(func(context.Context, []Message, int) (Message, error) {
		turn++
		switch turn {
		case 1:
			return toolAnswer("ok", "open('REPORT.md','w').write('old report')"), nil
		case 2:
			return toolAnswer("bad", "raise ValueError('failed update')"), nil
		default:
			return Message{Role: "assistant", Content: "Done"}, nil
		}
	}))
	if _, err := RunAgent(context.Background(), config); err == nil {
		t.Fatal("published after failed update")
	}
	if _, err := os.Stat(config.OutputPath); !os.IsNotExist(err) {
		t.Fatal("unexpected report export")
	}
}
func TestConfiguredDeadlineCannotBeExtendedByCaller(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	config := testConfig(t, modelFunc(func(context.Context, []Message, int) (Message, error) {
		t.Error("model called after expired budget")
		return Message{}, errors.New("unexpected model call")
	}))
	config.Deadline = time.Nanosecond
	if _, err := RunAgent(parent, config); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
}
