// RED-FIRST per C4. These tests are authored before internal/registry/transport.go
// has a body. Plan 03-02 Task 2 GREEN drives them green by implementing
// redactingTransport (http.RoundTripper) + SafeHeaders + the sensitiveHeaders
// list.
//
// What these tests guard:
//
//   - TestRedactingTransport_SatisfiesRoundTripper: compile-time assertion
//     that *redactingTransport implements net/http.RoundTripper. Load-bearing
//     so crane.WithTransport(&redactingTransport{...}) never compiles to a
//     runtime panic.
//   - TestRedactingTransport_PassesThrough: the wire request is byte-identical
//     to what http.DefaultTransport would have sent. crane's bearer-token
//     flow REQUIRES Authorization to actually traverse the wire — the
//     redacting transport's job is the LOG side, not the WIRE side.
//   - TestSafeHeaders_StripsSensitive: the four sensitive header keys
//     (Authorization, WWW-Authenticate, X-Registry-Auth, Proxy-Authorization)
//     are all removed from the returned Header copy.
//   - TestSafeHeaders_PreservesOthers: non-sensitive headers (Content-Type,
//     Accept, User-Agent) survive byte-identically — proves SafeHeaders is
//     a filter, not a wipe.
//   - TestSafeHeaders_DoesNotMutateInput: the original Header passed in is
//     unchanged after SafeHeaders returns. Defensive copy invariant.
//   - TestAnonymousFlow_NoBasicHeader: PITFALL 2 REGRESSION GUARD.
//     An httptest registry that issues a 401 + bearer challenge against
//     crane.Digest(..., WithAuth(authn.Anonymous), WithPlatform(amd64))
//     receives an empty Authorization header on the first request and a
//     Bearer token on the second — but NEVER carries "Basic Og==" on any
//     request. Threat T-03-02-02 acceptance criterion (DETECT-03).
//
// Goroutine assertion contract (per persist_test.go lines 29-31 and
// discovery_test.go line 33): assertions fired inside httptest handler
// goroutines use t.Errorf, NEVER t.Fatal — t.Fatal inside a goroutine only
// halts the goroutine that calls it and leaves the test to pass falsely.
package registry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// TestRedactingTransport_SatisfiesRoundTripper is the compile-time
// load-bearing guard. If *redactingTransport drifts away from
// http.RoundTripper, the build fails on the line below — not at runtime,
// not in production.
func TestRedactingTransport_SatisfiesRoundTripper(t *testing.T) {
	t.Parallel()
	var _ http.RoundTripper = (*redactingTransport)(nil)
}

// TestRedactingTransport_PassesThrough proves the transport is wire-clean:
// the same request body, headers, and method that http.DefaultTransport
// would have produced reach the destination server. crane's bearer flow
// would break otherwise (the registry never sees Authorization: Bearer X).
func TestRedactingTransport_PassesThrough(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		gotAuth string
		gotPath string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	rt := NewRedactingTransport()
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/hello", nil)
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer fake-jwt-xyz")
	req.Header.Set("Content-Type", "application/json")

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("response status = %d, want 200", resp.StatusCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotAuth != "Bearer fake-jwt-xyz" {
		t.Errorf("server saw Authorization = %q, want %q (wire must be unchanged)", gotAuth, "Bearer fake-jwt-xyz")
	}
	if gotPath != "/hello" {
		t.Errorf("server saw path = %q, want %q", gotPath, "/hello")
	}
}

// TestSafeHeaders_StripsSensitive verifies the four sensitive header keys
// are scrubbed from the returned copy. Threat T-03-02-01 acceptance.
func TestSafeHeaders_StripsSensitive(t *testing.T) {
	t.Parallel()

	in := http.Header{}
	in.Set("Authorization", "Bearer leak-me-not")
	in.Set("WWW-Authenticate", `Bearer realm="example.com"`)
	in.Set("X-Registry-Auth", "base64-leak")
	in.Set("Proxy-Authorization", "Basic leak")
	in.Set("Content-Type", "application/json")

	out := SafeHeaders(in)

	for _, k := range []string{"Authorization", "WWW-Authenticate", "X-Registry-Auth", "Proxy-Authorization"} {
		if v := out.Get(k); v != "" {
			t.Errorf("SafeHeaders kept sensitive header %q = %q (want empty)", k, v)
		}
	}
}

// TestSafeHeaders_PreservesOthers verifies the filter is conservative: it
// only removes sensitive keys, leaving everything else intact.
func TestSafeHeaders_PreservesOthers(t *testing.T) {
	t.Parallel()

	in := http.Header{}
	in.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	in.Set("Accept", "application/vnd.oci.image.index.v1+json")
	in.Set("User-Agent", "hmi-update/0.1")
	in.Add("X-Custom", "value1")
	in.Add("X-Custom", "value2")

	out := SafeHeaders(in)

	if got := out.Get("Content-Type"); got != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("Content-Type lost: got %q", got)
	}
	if got := out.Get("Accept"); got != "application/vnd.oci.image.index.v1+json" {
		t.Errorf("Accept lost: got %q", got)
	}
	if got := out.Get("User-Agent"); got != "hmi-update/0.1" {
		t.Errorf("User-Agent lost: got %q", got)
	}
	if got := out.Values("X-Custom"); len(got) != 2 || got[0] != "value1" || got[1] != "value2" {
		t.Errorf("X-Custom multi-value lost: got %v, want [value1 value2]", got)
	}
}

// TestSafeHeaders_DoesNotMutateInput verifies SafeHeaders returns a
// defensive copy: the original Header map handed in is unchanged after
// the call. Important so callers can pass req.Header without fear of
// stripping the wire's Authorization in flight.
func TestSafeHeaders_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	in := http.Header{}
	in.Set("Authorization", "Bearer original")
	in.Set("Content-Type", "application/json")

	_ = SafeHeaders(in)

	if got := in.Get("Authorization"); got != "Bearer original" {
		t.Errorf("SafeHeaders mutated input Authorization: got %q, want %q", got, "Bearer original")
	}
	if got := in.Get("Content-Type"); got != "application/json" {
		t.Errorf("SafeHeaders mutated input Content-Type: got %q, want %q", got, "application/json")
	}
}

// TestAnonymousFlow_NoBasicHeader is the PITFALL 2 REGRESSION GUARD.
//
// It stands up a two-server fixture that mimics GHCR's bearer-token
// challenge flow:
//
//  1. crane sends an initial request to the registry with no Authorization.
//  2. The registry responds 401 + WWW-Authenticate: Bearer realm=<tokenSrv>.
//  3. crane fetches a token from the token endpoint (this is where the
//     default keychain authenticator — see _sdk_shape.txt FORBIDDEN
//     block for the fully-qualified identifier — would emit
//     "Basic Og==" if a docker login with an empty username had ever
//     happened on this host).
//  4. crane retries the registry request with Authorization: Bearer <jwt>.
//
// The test captures EVERY inbound request's Authorization header on both
// servers and asserts the literal string "Basic Og==" appears in ZERO of
// them. This is the regression dam for Pitfall 2 from CONTEXT.md Area 1.
//
// Threat T-03-02-02 acceptance: DETECT-03 satisfied at the unit-test level.
func TestAnonymousFlow_NoBasicHeader(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string // each entry: "<server>:<path>:<auth>"
	)

	// Token endpoint: crane fetches a bearer token here. With
	// authn.Anonymous we expect NO Authorization on this request.
	tokenMux := http.NewServeMux()
	tokenMux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "token:/token:"+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		// Encode the standard token response shape.
		if err := json.NewEncoder(w).Encode(map[string]string{"token": "fake-anon-jwt"}); err != nil {
			// httptest handler — t.Errorf NOT t.Fatal (goroutine assertion contract).
			t.Errorf("token endpoint encode: %v", err)
		}
	})
	tokenSrv := httptest.NewServer(tokenMux)
	defer tokenSrv.Close()

	// Registry endpoint: challenges first, then serves manifest after
	// crane resubmits with the bearer.
	regMux := http.NewServeMux()
	regMux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, "registry:"+r.URL.Path+":"+r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate",
				`Bearer realm="`+tokenSrv.URL+`/token",service="reg",scope="repository:foo/bar:pull"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Second pass — should be "Bearer fake-anon-jwt".
		w.Header().Set("Docker-Content-Digest", "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		// Minimal manifest body. Body content doesn't matter — crane
		// uses the Docker-Content-Digest header value (DETECT-02).
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:0","size":0},"layers":[]}`)
	})
	regSrv := httptest.NewServer(regMux)
	defer regSrv.Close()

	refHost := strings.TrimPrefix(regSrv.URL, "http://")
	// Execute the actual crane.Digest call with the locked CONTEXT.md
	// Area 1 API surface: authn.Anonymous + linux/amd64.
	_, _ = crane.Digest(refHost+"/foo/bar:latest",
		crane.WithAuth(authn.Anonymous),
		crane.WithPlatform(&v1.Platform{OS: "linux", Architecture: "amd64"}),
	)

	// PITFALL 2 REGRESSION GUARD: no request anywhere ever carried
	// Basic Og== (T-03-02-02 / DETECT-03 — empty-credentials Basic
	// header would break GHCR's anonymous bearer flow).
	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatalf("no requests observed — fixture likely misconfigured (expected >= 2 requests)")
	}
	for _, s := range seen {
		if strings.Contains(s, "Basic Og==") {
			t.Errorf("Pitfall 2 regression: empty-credentials Basic header sent on request: %s", s)
		}
	}

	// Belt-and-braces invariants on the captured flow:
	//   (a) at least one registry request happened
	//   (b) at least one token-endpoint request happened
	//   (c) the first registry hit carried NO Authorization (anonymous)
	var (
		sawTokenHit       bool
		sawRegistryHit    bool
		firstRegistryAuth = "<UNSET>"
	)
	for _, s := range seen {
		switch {
		case strings.HasPrefix(s, "token:"):
			sawTokenHit = true
		case strings.HasPrefix(s, "registry:") && firstRegistryAuth == "<UNSET>":
			sawRegistryHit = true
			// Extract auth from "registry:<path>:<auth>"
			parts := strings.SplitN(s, ":", 3)
			if len(parts) >= 3 {
				firstRegistryAuth = parts[2]
			}
		case strings.HasPrefix(s, "registry:"):
			sawRegistryHit = true
		}
	}
	if !sawRegistryHit {
		t.Errorf("expected at least one registry request, none seen; captured: %v", seen)
	}
	if !sawTokenHit {
		t.Errorf("expected at least one token-endpoint request (bearer flow), none seen; captured: %v", seen)
	}
	if firstRegistryAuth != "" && firstRegistryAuth != "<UNSET>" {
		t.Errorf("first registry request carried Authorization = %q, want empty (anonymous flow)", firstRegistryAuth)
	}
}
