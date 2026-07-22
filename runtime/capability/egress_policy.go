package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

type netIPResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

var nonPublicSpecialPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

// NewPublicHTTPClient returns the production-style transport used by the local
// operator entry point. It ignores ambient proxies and validates DNS results at
// dial time. Tests that require loopback may use an explicit IP-loopback URL.
func NewPublicHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{}
	transport.DialContext = newPublicDialContext(net.DefaultResolver, dialer.DialContext)
	return &http.Client{Transport: transport}
}

func newPublicDialContext(resolver netIPResolver, dial dialContextFunc) dialContextFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid provider address: %w", err)
		}
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			literal = literal.Unmap()
			if literal.Zone() != "" || (!literal.IsLoopback() && !isPublicAddress(literal)) {
				return nil, errors.New("provider address is not public")
			}
			return dial(ctx, network, net.JoinHostPort(literal.String(), port))
		}

		lookupNetwork := "ip"
		switch network {
		case "tcp4":
			lookupNetwork = "ip4"
		case "tcp6":
			lookupNetwork = "ip6"
		case "tcp":
		default:
			return nil, errors.New("provider dial requires TCP")
		}
		addresses, err := resolver.LookupNetIP(ctx, lookupNetwork, host)
		if err != nil {
			return nil, fmt.Errorf("provider DNS lookup failed: %w", err)
		}
		if len(addresses) == 0 {
			return nil, errors.New("provider DNS lookup returned no addresses")
		}
		for _, candidate := range addresses {
			if !isPublicAddress(candidate.Unmap()) {
				return nil, errors.New("provider DNS result is not public")
			}
		}

		var dialErrors []error
		for _, candidate := range addresses {
			connection, dialErr := dial(ctx, network, net.JoinHostPort(candidate.Unmap().String(), port))
			if dialErr == nil {
				return connection, nil
			}
			dialErrors = append(dialErrors, dialErr)
			if ctx.Err() != nil {
				break
			}
		}
		return nil, fmt.Errorf("all validated provider addresses failed: %w", errors.Join(dialErrors...))
	}
}

func isPublicAddress(address netip.Addr) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap()
	if address.Zone() != "" || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicSpecialPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}
