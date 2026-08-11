package wazero_test

import (
	"context"
	"encoding/base64"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

func TestFactoryIsFreshOnly(t *testing.T) {
	runner, err := (wazeroengine.Factory{}).New(context.Background(), []byte{0, 97, 115, 109, 1, 0, 0, 0}, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	if properties := runner.Properties(); properties.Backend != "wazero" || properties.ExecutionProfileID != "" {
		t.Fatalf("unexpected properties: %#v", properties)
	}
}

func TestLegacyGuestFailsSourceValidationBeforeBroker(t *testing.T) {
	wasm, err := base64.StdEncoding.DecodeString("AGFzbQEAAAABEwRgAABgAn9/AX9gAX8Bf2ABfwADBwYAAQECAwEFAwEAAQdVBwZtZW1vcnkCAAtfaW5pdGlhbGl6ZQAADHJ1bnRpbWVfaW5pdAABD3J1bnRpbWVfcHJlcGFyZQACBWFsbG9jAAMHZGVhbGxvYwAEB2V4ZWN1dGUABQoaBgIACwQAQQALBABBAAsEAEEICwIACwMAAAs=")
	if err != nil {
		t.Fatal(err)
	}
	brokerCalled := false
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		brokerCalled = true
		return nil, nil
	}}).New(context.Background(), wasm, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	_, err = runner.Run(context.Background(), []byte(`{"run_id":"r","code":"result = 1","inputs":{}}`), "")
	if err == nil || brokerCalled {
		t.Fatalf("legacy guest did not fail closed: err=%v broker_called=%v", err, brokerCalled)
	}
}
