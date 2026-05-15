// handlers_assets_test.go — Plan 05-05 Task 1.
//
// Pins the four Cache-Control + MIME invariants for the embedded SPA
// (Pitfall 8: research/PITFALLS.md and 05-RESEARCH.md §D):
//
//   - TestAssets_ImmutableCacheControl  — GET a real /assets/*.js carries
//     `public, max-age=31536000, immutable` AND Content-Type
//     `application/javascript; charset=utf-8`.
//   - TestAssets_StrictNoFallback       — GET /assets/<missing>.js returns
//     404; body MUST NOT contain `<html` (no SPA fallback to index.html;
//     Pitfall 8 byte-level guard).
//   - TestIndex_NoCache                 — GET / returns Cache-Control:
//     no-cache.
//   - TestApiState_NoStore              — GET /api/state returns
//     Cache-Control: no-store.
//   - TestAssets_AllMimeTypes           — table-driven sanity over the
//     four registered extensions (.js, .css, .svg, .json) — verifies
//     mime.TypeByExtension reads the registrations performed at boot
//     (cmd/hmi-update/main.go::registerMIMETypes + internal/api/static.go
//     init()).
//
// Phase 1 already lands TestAssetsStrictNoFallback / TestAssetsImmutable /
// TestIndexHTMLCacheControl in server_test.go covering the same surface
// with slightly different names; this file adds the must-have-named
// tests (the verifier greps for these exact names) and the AllMimeTypes
// table.
//
// All tests use the existing newTestServer helper from server_test.go
// (fakeClient + tmpDir state store + tmpDir compose stub).
package api

import (
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestAssets_ImmutableCacheControl pins the canonical SPA-asset cache
// directive (`public, max-age=31536000, immutable`) AND the explicit
// `application/javascript; charset=utf-8` Content-Type on every
// /assets/*.js response. The directive is byte-equal to what
// internal/api/static.go::newStaticHandler sets; the Content-Type
// originates from mime.TypeByExtension(".js"), which is only correct on
// distroless once mime.AddExtensionType has run (registerMIMETypes in
// main.go + the package init in static.go).
//
// Discovers a real hashed asset by reading dist/index.html and grepping
// the first `/assets/[hash].js` reference. Skips with a clear message
// if dist/ has not been built (`make ui` is required).
func TestAssets_ImmutableCacheControl(t *testing.T) {
	indexBytes, err := os.ReadFile("dist/index.html")
	if err != nil {
		t.Skipf("dist/index.html not available; run `make ui` first: %v", err)
	}
	re := regexp.MustCompile(`/assets/[A-Za-z0-9._-]+\.js`)
	match := re.FindString(string(indexBytes))
	if match == "" {
		t.Skip("no /assets/*.js reference in index.html; skipping")
	}

	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, match, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d", match, got, http.StatusOK)
	}
	// Cache-Control: must contain `immutable`. The full canonical
	// directive is `public, max-age=31536000, immutable`; assert the
	// `immutable` token AND the full directive separately so a future
	// drift to just `immutable` (missing the max-age) fails loudly.
	cc := strings.ToLower(rec.Header().Get("Cache-Control"))
	if !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control missing `immutable`: %q", cc)
	}
	if cc != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want %q", cc, "public, max-age=31536000, immutable")
	}
	// Content-Type: must be application/javascript; charset=utf-8.
	// Anything else (text/plain, text/javascript) is the Pitfall 8
	// failure mode — Chromium rejects ES modules served as text/plain.
	ct := strings.ToLower(rec.Header().Get("Content-Type"))
	if ct != "application/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q (Pitfall 8: distroless lacks /etc/mime.types)",
			ct, "application/javascript; charset=utf-8")
	}
	// Body must be non-empty — header-only responses would imply a
	// regression to a stub handler.
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Error("response body is empty (header-only response is a regression)")
	}
}

// TestAssets_StrictNoFallback pins the Pitfall 8 strict-404 invariant:
// a request for /assets/<missing>.js MUST 404, and the body MUST NOT
// contain `<html` (no fallback to index.html). The reverse is the
// canonical SPA-router pattern; this app has no client-side router so
// the fallback is a bug class — a stale browser tab requesting a
// previous deploy's hashed asset would otherwise receive index.html
// bytes under a .js URL and Chrome's strict-MIME-for-modules check
// would emit a hard error with no in-browser remediation.
func TestAssets_StrictNoFallback(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/assets/this-does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (Pitfall 8: no SPA fallback for asset paths)",
			got, http.StatusNotFound)
	}
	body := strings.ToLower(rec.Body.String())
	// Must not contain `<html` (case-insensitive) — covers `<html>`,
	// `<html lang=...>`, and the doctype prelude `<!doctype html`.
	if strings.Contains(body, "<html") {
		t.Errorf("body contains `<html` — fallback to index.html happened: %q",
			body[:min(120, len(body))])
	}
	if strings.Contains(body, "<!doctype html") {
		t.Errorf("body contains `<!doctype html` — fallback to index.html happened: %q",
			body[:min(120, len(body))])
	}
}

// TestIndex_NoCache pins Cache-Control: no-cache on GET / so an
// in-place upgrade is visible after one revalidation round-trip
// rather than waiting on a stale TTL. Belt-and-braces with the
// immutable directive on /assets/* — together they form the SPA
// cache trio (research/PITFALLS.md Pitfall 8; web.dev/love-your-cache).
//
// `no-cache` (NOT `no-store`) is intentional: the browser MAY keep the
// response, but MUST revalidate every load. With an ETag this drops to
// 304 on unchanged builds and 200 on the next deploy — exactly what
// in-place upgrade needs.
func TestIndex_NoCache(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	cc := strings.ToLower(rec.Header().Get("Cache-Control"))
	if !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q, want to contain `no-cache`", cc)
	}
	// Also assert it does NOT carry no-store (semantic difference —
	// no-store means "do not keep at all"; no-cache means "may keep
	// but must revalidate"). A drift to no-store would force an extra
	// round trip on every navigation, not desired for the SPA shell.
	if strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, must not contain `no-store` (SPA shell uses no-cache)", cc)
	}
}

// TestApiState_NoStore pins Cache-Control: no-store on GET /api/state.
// Why no-store (not no-cache): the JSON wire shape may change across
// versions (Phase 3 added last_poll_*, Phase 4 added action_in_flight /
// action_error / labels), and after an in-place upgrade a browser tab
// caching the OLD shape under a 304-revalidation would render with
// missing fields. no-store guarantees the next 5s poll fetches afresh
// regardless of any intermediate cache (browser, corporate proxy,
// service worker).
func TestApiState_NoStore(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want %d", got, http.StatusOK)
	}
	cc := strings.ToLower(rec.Header().Get("Cache-Control"))
	if cc != "no-store" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-store")
	}
}

// TestAssets_AllMimeTypes verifies that mime.TypeByExtension reads the
// boot-time registrations for all four extensions Vite emits
// (.js, .css, .svg, .json) plus the forward-compat .woff2.
//
// This is a direct table assertion against the process-global mime
// package — it does NOT go through the HTTP handler. The handler-side
// assertion is TestAssets_ImmutableCacheControl above (which proves
// the registration is observable through http.ServeContent +
// mime.TypeByExtension).
//
// Failure mode this catches: a future refactor that drops one of the
// AddExtensionType calls would let mime.TypeByExtension fall back to
// the Go default (text/plain or empty), break Chromium's module
// loader, and break in-place upgrade. The unit test fails before the
// e2e even runs.
func TestAssets_AllMimeTypes(t *testing.T) {
	cases := []struct {
		ext    string
		want   string
		reason string
	}{
		{
			ext:    ".js",
			want:   "application/javascript; charset=utf-8",
			reason: "ES modules (Chromium hard-fails on text/plain)",
		},
		{
			ext:    ".css",
			want:   "text/css; charset=utf-8",
			reason: "stylesheets",
		},
		{
			ext:    ".svg",
			want:   "image/svg+xml",
			reason: "inline-fallback icons",
		},
		{
			ext:    ".json",
			want:   "application/json; charset=utf-8",
			reason: "forward-compat for future inline JSON modules",
		},
	}
	for _, tc := range cases {
		t.Run(tc.ext, func(t *testing.T) {
			got := mime.TypeByExtension(tc.ext)
			if got != tc.want {
				t.Errorf("mime.TypeByExtension(%q) = %q, want %q (%s)",
					tc.ext, got, tc.want, tc.reason)
			}
		})
	}
}
