package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func validSecretRequest() capability.SecretRequest {
	return capability.SecretRequest{Reference: "cloudflare.dns", RunIdentity: "run:1", TaskIdentity: "task:1", Tenant: "tenant:1", ToolID: "cloudflare.dns.apply", Operation: "apply", ResourceAlias: "zone:production", PolicyVersion: "policy:v1", RequestedLease: time.Minute}
}

func TestStaticSecretResolverLeaseIsScopedRedactedAndReleased(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	var audits []capability.SecretAudit
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{"cloudflare.dns": []byte("super-secret-token")}, func() time.Time { return now }, func(audit capability.SecretAudit) { audits = append(audits, audit) })
	if err != nil {
		t.Fatal(err)
	}
	defer resolver.Close()
	lease, err := resolver.Resolve(context.Background(), validSecretRequest())
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%v %#v", lease, lease); strings.Contains(got, "super-secret") || got != "[REDACTED] [REDACTED]" {
		t.Fatalf("lease formatting leaked or drifted: %q", got)
	}
	encoded, err := json.Marshal(lease)
	if err != nil || strings.Contains(string(encoded), "secret") {
		t.Fatalf("lease JSON leaked: %s err=%v", encoded, err)
	}
	if err := lease.WithValue(func(value []byte) error {
		if string(value) != "super-secret-token" {
			t.Fatalf("resolved value=%q", value)
		}
		value[0] = 'X'
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := lease.WithValue(func(value []byte) error {
		if string(value) != "super-secret-token" {
			t.Fatal("callback mutated lease-owned bytes")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease.Release()
	if err := lease.WithValue(func([]byte) error { return nil }); !errors.Is(err, capability.ErrSecretReleased) {
		t.Fatalf("released lease err=%v", err)
	}
	if len(audits) != 1 || audits[0].Outcome != "resolved" || audits[0].ToolID != "cloudflare.dns.apply" {
		t.Fatalf("audits=%+v", audits)
	}
}

func TestStaticSecretResolverFailsClosedAndExpires(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{"cloudflare.dns": []byte("token")}, func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	request := validSecretRequest()
	lease, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := lease.WithValue(func([]byte) error { return nil }); !errors.Is(err, capability.ErrSecretExpired) {
		t.Fatalf("expired lease err=%v", err)
	}
	request.Reference = "missing.secret"
	if _, err := resolver.Resolve(context.Background(), request); !errors.Is(err, capability.ErrSecretDenied) {
		t.Fatalf("missing secret err=%v", err)
	}
	request = validSecretRequest()
	request.RequestedLease = 25 * time.Hour
	if _, err := resolver.Resolve(context.Background(), request); !errors.Is(err, capability.ErrSecretDenied) {
		t.Fatalf("oversized lease err=%v", err)
	}
	resolver.Close()
	if _, err := resolver.Resolve(context.Background(), validSecretRequest()); !errors.Is(err, capability.ErrSecretDenied) {
		t.Fatalf("closed resolver err=%v", err)
	}
}
