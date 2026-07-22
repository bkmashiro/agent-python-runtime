package capability

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const FetchManyCapability = "fetch_many"

const (
	hardMaxCalls           = 64
	hardMaxRequestsPerCall = 64
	hardMaxTotalRequests   = 256
	hardMaxResponseBytes   = 16 * 1024 * 1024
	hardMaxRequestTimeout  = 30 * time.Second
)

type TargetGrant struct {
	BaseURL string
	Headers map[string]string
}

type Grant struct {
	Name               string
	Targets            map[string]TargetGrant
	MaxCalls           uint32
	MaxRequestsPerCall uint32
	MaxTotalRequests   uint32
	MaxResponseBytes   uint32
	PerRequestTimeout  time.Duration
}

func (grant Grant) Validate() error {
	if grant.Name != FetchManyCapability {
		return fmt.Errorf("unsupported capability grant %q", grant.Name)
	}
	if grant.MaxCalls == 0 || grant.MaxCalls > hardMaxCalls {
		return errors.New("max calls is outside the hard bound")
	}
	if grant.MaxRequestsPerCall == 0 || grant.MaxRequestsPerCall > hardMaxRequestsPerCall {
		return errors.New("max requests per call is outside the hard bound")
	}
	if grant.MaxTotalRequests == 0 || grant.MaxTotalRequests > hardMaxTotalRequests {
		return errors.New("max total requests is outside the hard bound")
	}
	if grant.MaxResponseBytes == 0 || grant.MaxResponseBytes > hardMaxResponseBytes {
		return errors.New("max response bytes is outside the hard bound")
	}
	if grant.PerRequestTimeout <= 0 || grant.PerRequestTimeout > hardMaxRequestTimeout {
		return errors.New("per-request timeout is outside the hard bound")
	}
	if len(grant.Targets) == 0 {
		return errors.New("fetch_many grant has no targets")
	}
	for name, target := range grant.Targets {
		if err := validateTarget(name, target); err != nil {
			return err
		}
	}
	return nil
}

func validateTarget(name string, target TargetGrant) error {
	if name == "" || strings.ContainsAny(name, " /\\") {
		return fmt.Errorf("invalid target name %q", name)
	}
	parsed, err := url.Parse(target.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("target %q has invalid base URL", name)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("target %q base URL must be an origin", name)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		host := parsed.Hostname()
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("target %q permits plaintext HTTP only for loopback fixtures", name)
		}
	default:
		return fmt.Errorf("target %q uses unsupported scheme %q", name, parsed.Scheme)
	}
	for header, value := range target.Headers {
		if strings.TrimSpace(header) == "" || strings.ContainsAny(header, "\r\n") || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("target %q has invalid Host header", name)
		}
	}
	return nil
}
