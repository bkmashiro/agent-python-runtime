package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestProviderWriteIsIdempotentButCountsRequests(t *testing.T) {
	p, err := openProvider(t.TempDir()+"/provider.db", "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err := p.setReadValue("11"); err != nil {
		t.Fatal(err)
	}
	read, err := p.read(context.Background(), "run/0", json.RawMessage(`{"key":"demo"}`))
	if err != nil || string(read) != `{"value":"11"}` {
		t.Fatalf("read=%s err=%v", read, err)
	}
	args := json.RawMessage(`{"read_value":11,"random_value":9,"computed":20}`)
	first, err := p.write(context.Background(), "run/2", args)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.write(context.Background(), "run/2", args)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("idempotent result changed: %s != %s", first, second)
	}
	status, err := p.status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ReadDispatches != 1 || status.WriteRequests != 2 || status.EffectCount != 1 || len(status.Effects) != 1 {
		t.Fatalf("status=%+v", status)
	}
}
