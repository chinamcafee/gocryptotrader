package httpclient

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateBaseURL(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name    string
		raw     string
		opts    Options
		wantErr error
	}{
		{name: "https accepted", raw: "https://api.typesafe.ai"},
		{name: "https with path accepted", raw: "https://api.typesafe.ai/v1/"},
		{name: "empty rejected", raw: "   ", wantErr: ErrNilEndpoint},
		{name: "no host rejected", raw: "https://", wantErr: ErrEmptyHost},
		{name: "http rejected by default", raw: "http://api.example.com", wantErr: ErrInsecureScheme},
		{
			name: "http loopback allowed only when asked for",
			raw:  "http://127.0.0.1:8080", opts: Options{AllowInsecureLoopback: true},
		},
		{
			name: "http public host still rejected with the loopback exception on",
			raw:  "http://api.example.com", opts: Options{AllowInsecureLoopback: true}, wantErr: ErrInsecureScheme,
		},
		{name: "embedded credentials rejected", raw: "https://user:pass@api.example.com", wantErr: ErrEmbeddedCredentials},
		{name: "unknown scheme rejected", raw: "ftp://api.example.com", wantErr: ErrInsecureScheme},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ValidateBaseURL(tc.raw, &tc.opts)
			if tc.wantErr == nil {
				require.NoErrorf(t, err, "%q must be accepted", tc.raw)
				return
			}
			require.Errorf(t, err, "%q must be rejected", tc.raw)
			assert.ErrorIs(t, err, tc.wantErr, "the rejection should be attributable to the expected sentinel")
		})
	}
}

func TestCheckAddress(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name         string
		ip           string
		allowPrivate bool
		blocked      bool
	}{
		{name: "public v4 allowed", ip: "8.8.8.8"},
		{name: "public v6 allowed", ip: "2606:4700:4700::1111"},
		{name: "private v4 blocked by default", ip: "10.1.2.3", blocked: true},
		{name: "private v4 allowed when asked for", ip: "10.1.2.3", allowPrivate: true},
		{name: "router range blocked by default", ip: "192.168.1.10", blocked: true},
		{name: "cgnat blocked by default", ip: "100.100.100.100", blocked: true},
		{name: "unique local v6 blocked by default", ip: "fd00::1", blocked: true},
		{name: "ipv4 loopback blocked by default", ip: "127.0.0.1", blocked: true},
		{name: "ipv6 loopback blocked by default", ip: "::1", blocked: true},
		{name: "ipv6 loopback allowed when asked for", ip: "::1", allowPrivate: true},
		{name: "link-local always blocked", ip: "169.254.10.10", allowPrivate: true, blocked: true},
		{name: "metadata always blocked", ip: "169.254.169.254", allowPrivate: true, blocked: true},
		{name: "ipv6 link-local always blocked", ip: "fe80::1", allowPrivate: true, blocked: true},
		{name: "nil address blocked", ip: "", blocked: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var ip net.IP
			if tc.ip != "" && tc.ip != "nil" {
				ip = net.ParseIP(tc.ip)
				if ip == nil {
					t.Skipf("environment cannot parse %q", tc.ip)
				}
			}
			err := CheckAddress(ip, tc.allowPrivate)
			if tc.blocked {
				require.ErrorIsf(t, err, ErrBlockedAddress, "%s must be refused", tc.ip)
				return
			}
			assert.NoErrorf(t, err, "%s should be allowed", tc.ip)
		})
	}
}

// newLocalClient builds a client that may reach an httptest server.
func newLocalClient(t *testing.T, serverURL string, opts *Options) *http.Client {
	t.Helper()
	opts.BaseURL = serverURL
	opts.AllowInsecureLoopback = true
	opts.AllowPrivateNetwork = true
	c, err := New(opts)
	require.NoError(t, err, "a loopback endpoint with private access must be accepted")
	return c
}

func TestNewRejectsInsecureAndPrivateByDefault(t *testing.T) {
	t.Parallel()

	_, err := New(&Options{BaseURL: "http://127.0.0.1:9"})
	require.ErrorIs(t, err, ErrInsecureScheme, "plain http must be refused before the address policy is reached")

	_, err = New(&Options{BaseURL: "https://10.0.0.1"})
	assert.NoError(t, err, "a private address is only judged at dial time, not at construction")

	_, err = New(&Options{BaseURL: "http://127.0.0.1:9", AllowInsecureLoopback: true})
	require.ErrorIs(t, err, ErrBlockedAddress, "an insecure loopback endpoint still needs private access granted")
}

func TestNewRefusesLoopbackWithoutPrivateAccess(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// A loopback endpoint resolves to a private address, so private access must
	// be granted explicitly. Refusing at construction is stricter than refusing
	// at dial time and gives the operator a clearer failure.
	_, err := New(&Options{BaseURL: server.URL, AllowInsecureLoopback: true})
	require.ErrorIs(t, err, ErrBlockedAddress, "a loopback endpoint without private access must be refused")
}

func TestNewAllowsLoopbackWhenPrivateAccessGranted(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL, &Options{})
	payload, err := PostJSON(context.Background(), client, server.URL, "", []byte(`{}`), 1024)
	require.NoError(t, err, "an explicitly granted loopback endpoint must work")
	assert.Equal(t, `{"ok":true}`, string(payload), "the body should be returned")
}

func TestPostJSONSendsBearerAndReadsBody(t *testing.T) {
	t.Parallel()

	var gotAuth, gotCT, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		buf := make([]byte, 64)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte(`{"model":"jev-1.13.0"}`))
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL, &Options{})
	payload, err := PostJSON(context.Background(), client, server.URL, "secret-token", []byte(`{"a":1}`), 1024)
	require.NoError(t, err, "a local call must succeed")
	assert.JSONEq(t, `{"model":"jev-1.13.0"}`, string(payload), "the body should be returned verbatim")
	assert.Equal(t, "Bearer secret-token", gotAuth, "the bearer token should be sent")
	assert.Equal(t, "application/json", gotCT, "the content type should be JSON")
	assert.Equal(t, `{"a":1}`, gotBody, "the request body should be forwarded")
}

func TestPostJSONRefusesOversizedBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL, &Options{})
	_, err := PostJSON(context.Background(), client, server.URL, "", []byte(`{}`), 512)
	require.ErrorIs(t, err, ErrResponseTooLarge, "a body over the cap must be refused")
}

func TestPostJSONReportsStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		status   int
		wantAuth bool
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, wantAuth: true},
		{name: "forbidden", status: http.StatusForbidden, wantAuth: true},
		{name: "rate limited", status: http.StatusTooManyRequests},
		{name: "server error", status: http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":"nope"}`))
			}))
			defer server.Close()

			client := newLocalClient(t, server.URL, &Options{})
			_, err := PostJSON(context.Background(), client, server.URL, "", []byte(`{}`), 512)
			var status *StatusError
			require.ErrorAs(t, err, &status, "the failure must be a status error")
			assert.Equal(t, tc.status, status.StatusCode, "the status should be preserved")
			assert.Equal(t, tc.wantAuth, status.IsAuthentication(), "authentication failures should be identifiable")
		})
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"leaked":true}`))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := newLocalClient(t, redirector.URL, &Options{})
	_, err := PostJSON(context.Background(), client, redirector.URL, "secret-token", []byte(`{}`), 512)
	require.Error(t, err, "a redirect must not be followed, because it would forward the Authorization header")

	var status *StatusError
	require.ErrorAs(t, err, &status, "the redirect must surface as a status error on the original host")
	assert.Equal(t, http.StatusFound, status.StatusCode, "the redirect itself should be reported")
}

func TestClientIgnoresEnvironmentProxy(t *testing.T) {
	// No t.Parallel: this test reads and writes process environment, which the
	// testing package forbids alongside parallel subtests.
	// A machine-wide proxy would silently receive model credentials, so the
	// transport must be built with Proxy left nil.
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := newLocalClient(t, server.URL, &Options{})
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "the hardened client must use an explicit transport")
	assert.Nil(t, transport.Proxy, "the transport should not consult the environment for a proxy")

	payload, err := PostJSON(context.Background(), client, server.URL, "", []byte(`{}`), 512)
	require.NoError(t, err, "the call must reach the server directly despite the environment proxy")
	assert.Equal(t, `{"ok":true}`, string(payload), "the body should come from the direct connection")
}

func TestBasePath(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ base, want string }{
		{base: "https://api.typesafe.ai", want: "https://api.typesafe.ai/v1/systemone"},
		{base: "https://api.typesafe.ai/", want: "https://api.typesafe.ai/v1/systemone"},
		{base: "https://api.typesafe.ai/v1", want: "https://api.typesafe.ai/v1/v1/systemone"},
	} {
		u, err := ValidateBaseURL(tc.base, &Options{})
		require.NoErrorf(t, err, "%s must parse", tc.base)
		assert.Equal(t, tc.want, BasePath(u, "/v1/systemone"), "the path should be appended to the base")
	}
}

func TestNewResolvesAndDialsTheValidatedAddress(t *testing.T) {
	t.Parallel()

	calls := 0
	resolver := func(_ context.Context, _ string) ([]net.IP, error) {
		calls++
		// The first resolution returns a blocked address; the client must refuse
		// rather than re-resolving, which is the DNS rebinding window.
		return []net.IP{net.ParseIP("169.254.169.254")}, nil
	}
	client, err := New(&Options{
		// Timeout is a Duration, so it must be scaled explicitly: a bare 2 is two
		// nanoseconds and would time out before the dial is attempted.
		BaseURL: "https://metadata.example.com", Resolver: resolver, Timeout: 2 * time.Second,
	})
	require.NoError(t, err, "construction must succeed")
	_, err = PostJSON(context.Background(), client, "https://metadata.example.com", "", []byte(`{}`), 64)
	require.ErrorIs(t, err, ErrBlockedAddress, "a name resolving to the metadata address must be refused")
	assert.Equal(t, 1, calls, "the address should be resolved exactly once, so it cannot change before connecting")
}
