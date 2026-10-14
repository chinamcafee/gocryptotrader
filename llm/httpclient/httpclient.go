// Package httpclient builds hardened HTTP clients for the model adapters.
//
// The hardening rules are the same for every model endpoint
// (docs/llm-dev/05-LLM接入层-OpenAI兼容.md §10 and
// docs/llm-dev/10-安全与风控.md §4.1), so they live here rather than being
// reimplemented per adapter. Two copies of a security rule drift; one does not.
//
// The rules:
//
//  1. HTTPS is required, except for an explicitly allowed loopback address
//     during local development.
//  2. Credentials may not be embedded in the URL.
//  3. Redirects are never followed: a redirect to an attacker-controlled host
//     would carry the Authorization header with it.
//  4. The hostname is resolved before connecting and every resolved address is
//     checked, so a name that resolves to a link-local or metadata address is
//     refused. The connection is then made to the validated address rather than
//     re-resolving, which closes the DNS rebinding window.
//  5. Response bodies are read through a cap, so one hostile or broken endpoint
//     cannot exhaust memory.
//  6. Environment proxy variables are ignored. A machine-wide proxy would
//     silently receive model credentials; a proxy must be configured explicitly.
package httpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Defaults for the hardened client.
const (
	// DefaultTimeout bounds one request when the caller does not choose.
	DefaultTimeout = 30 * time.Second
	// DefaultMaxResponseBytes bounds a response body.
	DefaultMaxResponseBytes = 256 * 1024
	// DefaultDialTimeout bounds the TCP connect.
	DefaultDialTimeout = 10 * time.Second
	// DefaultUserAgent identifies the caller to the endpoint.
	DefaultUserAgent = "gocryptotrader-llm/1"
)

// ErrInsecureScheme marks a non-HTTPS endpoint outside the loopback exception.
var ErrInsecureScheme = errors.New("endpoint must use https")

// ErrEmbeddedCredentials marks a URL carrying a userinfo component.
var ErrEmbeddedCredentials = errors.New("endpoint must not embed credentials")

// ErrBlockedAddress marks an address the client refuses to connect to.
var ErrBlockedAddress = errors.New("address is blocked")

// ErrResponseTooLarge marks a body over the configured cap.
var ErrResponseTooLarge = errors.New("response exceeds the byte limit")

// ErrNilEndpoint marks an empty base URL.
var ErrNilEndpoint = errors.New("endpoint is empty")

// ErrEmptyHost marks a URL without a host.
var ErrEmptyHost = errors.New("endpoint has no host")

// ErrNilSecret marks a nil secret callback.
var ErrNilSecret = errors.New("secret callback is nil")

// SecretFn resolves a bearer token at call time.
//
// The token is fetched per request rather than stored on a client, so a
// decrypted secret lives only for the duration of one call and no long-lived
// copy sits in a heap that may later be dumped. Both adapters use this type so a
// caller can wire one secret source to either of them.
type SecretFn func(ctx context.Context) (string, error)

// Options configures a hardened client.
type Options struct {
	// BaseURL is the model endpoint. HTTPS is required unless the host is
	// loopback and AllowInsecureLoopback is set.
	BaseURL string
	// Timeout bounds one request.
	Timeout time.Duration
	// MaxResponseBytes bounds a response body.
	MaxResponseBytes int64
	// UserAgent is sent with every request.
	UserAgent string
	// ProxyURL is an explicit proxy. Environment proxy variables are never
	// consulted, whatever this is set to.
	ProxyURL string
	// AllowInsecureLoopback permits http to a loopback host. Intended for local
	// development against a sidecar endpoint, never for a public host.
	AllowInsecureLoopback bool
	// AllowPrivateNetwork permits connecting to private ranges. Required for
	// loopback endpoints, including the local test servers.
	AllowPrivateNetwork bool
	// Resolver overrides name resolution. Tests may supply one; production
	// leaves it nil to use the system resolver.
	Resolver func(ctx context.Context, host string) ([]net.IP, error)
}

var (
	// cloudMetadata is the best known instance-metadata address. It is refused
	// unconditionally: no configuration makes it a model endpoint.
	cloudMetadata = net.ParseIP("169.254.169.254")
	// blockedNets are refused regardless of AllowPrivateNetwork.
	blockedNets = []*net.IPNet{
		mustCIDR("169.254.0.0/16"), // link-local, including metadata services
		mustCIDR("fe80::/10"),      // IPv6 link-local
	}
	// privateNets are refused unless AllowPrivateNetwork is set.
	privateNets = []*net.IPNet{
		mustCIDR("10.0.0.0/8"),
		mustCIDR("172.16.0.0/12"),
		mustCIDR("192.168.0.0/16"),
		mustCIDR("100.64.0.0/10"), // carrier-grade NAT
		mustCIDR("fc00::/7"),      // IPv6 unique local
	}
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("httpclient: invalid CIDR " + s)
	}
	return n
}

// CheckAddress reports whether the client may connect to ip.
//
// The cloud metadata address and link-local ranges are refused even when private
// access is allowed, because a redirect or a crafted hostname pointing there
// would hand out instance credentials.
func CheckAddress(ip net.IP, allowPrivate bool) error {
	if ip == nil {
		return fmt.Errorf("%w: unparsable address", ErrBlockedAddress)
	}
	if cloudMetadata.Equal(ip) {
		return fmt.Errorf("%w: cloud metadata address %s", ErrBlockedAddress, ip)
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return fmt.Errorf("%w: link-local %s", ErrBlockedAddress, ip)
		}
	}
	// Loopback follows the private-network switch rather than a rule of its own:
	// "localhost" commonly resolves to ::1 before 127.0.0.1, so treating one of
	// them differently would make the loopback exception unreliable.
	if ip.IsLoopback() {
		if !allowPrivate {
			return fmt.Errorf("%w: loopback %s", ErrBlockedAddress, ip)
		}
		return nil
	}
	if !allowPrivate {
		for _, n := range privateNets {
			if n.Contains(ip) {
				return fmt.Errorf("%w: private range %s", ErrBlockedAddress, ip)
			}
		}
	}
	return nil
}

// ValidateBaseURL parses and checks an endpoint.
func ValidateBaseURL(raw string, opts *Options) (*url.URL, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, ErrNilEndpoint
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("parsing endpoint: %w", err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrEmptyHost, raw)
	}
	if u.User != nil {
		return nil, ErrEmbeddedCredentials
	}
	if strings.EqualFold(u.Scheme, "https") {
		return u, nil
	}
	if strings.EqualFold(u.Scheme, "http") && opts.AllowInsecureLoopback && isLoopbackHost(u.Hostname()) {
		return u, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrInsecureScheme, raw)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// New builds a hardened client for the endpoint in opts.
func New(opts *Options) (*http.Client, error) {
	base, err := ValidateBaseURL(opts.BaseURL, opts)
	if err != nil {
		return nil, err
	}
	// A loopback endpoint resolves to a private address, so private access must
	// be on for it; granting it silently would be surprising, so require the
	// caller to have asked for it by setting the flag.
	if isLoopbackHost(base.Hostname()) && !opts.AllowPrivateNetwork {
		return nil, fmt.Errorf("%w: loopback endpoint %q requires AllowPrivateNetwork", ErrBlockedAddress, base.Hostname())
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	resolve := opts.Resolver
	if resolve == nil {
		var r net.Resolver
		resolve = func(ctx context.Context, host string) ([]net.IP, error) {
			addrs, err := r.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			ips := make([]net.IP, 0, len(addrs))
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
			return ips, nil
		}
	}

	dialer := &net.Dialer{Timeout: DefaultDialTimeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Proxy is deliberately nil: environment proxy variables are not read.
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   DefaultDialTimeout,
		ExpectContinueTimeout: time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := resolve(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("resolving %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("%w: %q resolved to no addresses", ErrBlockedAddress, host)
			}
			for _, ip := range ips {
				if err = CheckAddress(ip, opts.AllowPrivateNetwork); err != nil {
					return nil, fmt.Errorf("endpoint host %q: %w", host, err)
				}
			}
			// Dial the address that was validated rather than the name, so the
			// address cannot change between validation and connection.
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	}
	if opts.ProxyURL != "" {
		proxy, err := url.Parse(opts.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("parsing proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxy)
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		// A redirect would resend the Authorization header to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// BasePath joins a validated base URL with a path.
func BasePath(base *url.URL, path string) string {
	return strings.TrimRight(base.String(), "/") + "/" + strings.TrimLeft(path, "/")
}

// PostJSON sends a JSON body with an optional bearer token and returns the
// response body, refusing anything over maxBytes.
//
// The body is read through a cap rather than after the fact: reading first would
// already have allocated the payload.
func PostJSON(ctx context.Context, client *http.Client, endpoint, bearer string, body []byte, maxBytes int64) ([]byte, error) {
	if client == nil {
		return nil, errors.New("nil HTTP client")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxResponseBytes
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", DefaultUserAgent)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting to endpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	limited := io.LimitReader(resp.Body, maxBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if int64(len(payload)) > maxBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrResponseTooLarge, maxBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &StatusError{StatusCode: resp.StatusCode, Body: truncate(payload, 512)}
	}
	return payload, nil
}

// StatusError reports a non-2xx response.
type StatusError struct {
	StatusCode int
	Body       string
}

// Error implements error.
func (e *StatusError) Error() string {
	return fmt.Sprintf("endpoint returned HTTP %d: %s", e.StatusCode, e.Body)
}

// IsAuthentication reports whether the failure was an authentication failure, so
// a caller can produce a stable probe code without string matching.
func (e *StatusError) IsAuthentication() bool {
	return e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
