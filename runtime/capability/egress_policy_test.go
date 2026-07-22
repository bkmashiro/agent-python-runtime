package capability

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"testing"
)

type resolverResult struct {
	addresses []netip.Addr
	err       error
}

type sequenceResolver struct {
	results []resolverResult
	lookups []string
}

func (resolver *sequenceResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	resolver.lookups = append(resolver.lookups, host)
	if len(resolver.results) == 0 {
		return nil, errors.New("unexpected lookup")
	}
	result := resolver.results[0]
	resolver.results = resolver.results[1:]
	return result.addresses, result.err
}

type recordingDialer struct {
	addresses []string
}

func (dialer *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	dialer.addresses = append(dialer.addresses, address)
	return nil, errors.New("fixture dial stopped")
}

func TestPublicDialPolicyPinsValidatedDNSAddress(t *testing.T) {
	resolver := &sequenceResolver{results: []resolverResult{{addresses: []netip.Addr{
		netip.MustParseAddr("93.184.216.34"),
	}}}}
	dialer := &recordingDialer{}
	dial := newPublicDialContext(resolver, dialer.DialContext)
	_, _ = dial(context.Background(), "tcp", "example.test:443")
	if !reflect.DeepEqual(resolver.lookups, []string{"example.test"}) {
		t.Fatalf("lookups=%v", resolver.lookups)
	}
	if !reflect.DeepEqual(dialer.addresses, []string{"93.184.216.34:443"}) {
		t.Fatalf("dial did not pin resolved public IP: %v", dialer.addresses)
	}
}

func TestPublicDialPolicyRejectsMixedOrNonPublicDNSResults(t *testing.T) {
	cases := map[string][]netip.Addr{
		"mixed":       {netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.1")},
		"loopback":    {netip.MustParseAddr("127.0.0.1")},
		"private-v6":  {netip.MustParseAddr("fd00::1")},
		"link-local":  {netip.MustParseAddr("169.254.1.1")},
		"unspecified": {netip.MustParseAddr("0.0.0.0")},
		"multicast":   {netip.MustParseAddr("224.0.0.1")},
		"mapped":      {netip.MustParseAddr("::ffff:192.168.1.1")},
	}
	for name, addresses := range cases {
		t.Run(name, func(t *testing.T) {
			resolver := &sequenceResolver{results: []resolverResult{{addresses: addresses}}}
			dialer := &recordingDialer{}
			dial := newPublicDialContext(resolver, dialer.DialContext)
			if _, err := dial(context.Background(), "tcp", "provider.example:443"); err == nil {
				t.Fatal("non-public DNS result was accepted")
			}
			if len(dialer.addresses) != 0 {
				t.Fatalf("non-public DNS result reached dialer: %v", dialer.addresses)
			}
		})
	}
}

func TestPublicDialPolicyAllowsOnlyExplicitLoopbackLiteralFixture(t *testing.T) {
	resolver := &sequenceResolver{}
	dialer := &recordingDialer{}
	dial := newPublicDialContext(resolver, dialer.DialContext)
	_, _ = dial(context.Background(), "tcp", "127.0.0.1:8080")
	_, _ = dial(context.Background(), "tcp", "[::1]:8081")
	if len(resolver.lookups) != 0 {
		t.Fatalf("literal loopback unexpectedly resolved: %v", resolver.lookups)
	}
	if !reflect.DeepEqual(dialer.addresses, []string{"127.0.0.1:8080", "[::1]:8081"}) {
		t.Fatalf("explicit loopback fixture was not preserved: %v", dialer.addresses)
	}
}

func TestPublicDialPolicyRevalidatesEveryDNSResolution(t *testing.T) {
	resolver := &sequenceResolver{results: []resolverResult{
		{addresses: []netip.Addr{netip.MustParseAddr("93.184.216.34")}},
		{addresses: []netip.Addr{netip.MustParseAddr("10.0.0.9")}},
	}}
	dialer := &recordingDialer{}
	dial := newPublicDialContext(resolver, dialer.DialContext)
	_, _ = dial(context.Background(), "tcp", "provider.example:443")
	if _, err := dial(context.Background(), "tcp", "provider.example:443"); err == nil {
		t.Fatal("rebinding to private address was accepted")
	}
	if !reflect.DeepEqual(dialer.addresses, []string{"93.184.216.34:443"}) {
		t.Fatalf("rebinding reached dialer: %v", dialer.addresses)
	}
}

func TestPublicHTTPFetcherRejectsPrivateLiteralBeforeNetwork(t *testing.T) {
	fetcher := NewHTTPFetcher(NewPublicHTTPClient())
	if _, err := fetcher.Fetch(context.Background(), ResolvedRequest{URL: "https://10.0.0.1/private"}, 1024); err == nil {
		t.Fatal("private literal reached production transport")
	}
}

func TestPublicHTTPClientDoesNotUseAmbientProxy(t *testing.T) {
	client := NewPublicHTTPClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("public client may use ambient proxy: %#v", client.Transport)
	}
}
