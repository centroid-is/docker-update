// Package registry (continued). transport.go defines the
// redactingTransport http.RoundTripper that crane.WithTransport receives,
// plus the SafeHeaders helper that any future debug-log call site MUST
// route through.
//
// INVARIANT (load-bearing — OBS-04 / threat T-03-02-01):
//
//	The wire request itself is UNCHANGED. crane's bearer-token flow
//	needs Authorization: Bearer <jwt> to function. The redacting
//	transport's job is the OUTPUT side: if anything inside this package
//	later adds debug logging of req.Header (e.g. an slog.Debug with
//	"req.headers", req.Header), it MUST route through SafeHeaders()
//	which strips the four sensitive keys. Test
//	TestRedactingTransport_PassesThrough pins the wire-unchanged
//	invariant; TestSafeHeaders_StripsSensitive +
//	TestSafeHeaders_PreservesOthers pin the filter behaviour.
//
//	The output-side defense (slog ReplaceAttr regex `^Bearer ` /
//	`^Basic `) lives in cmd/docker-update/main.go (Plan 03-05). Both
//	defenses together — transport-strip + slog-regex — survive a
//	future careless logger call.
//
// sensitiveHeaders is the canonical list. Future additions need a
// threat-model review (extend STRIDE table in 03-02-PLAN.md or
// successor plan's threat_model section).
package registry

import "net/http"

// sensitiveHeaders are the canonical four HTTP header keys that carry
// authentication material in the crane bearer flow. SafeHeaders strips
// these (and only these) from any header set the package logs.
//
//   - Authorization: the JWT bearer token (or, in the Pitfall 2 failure
//     mode, the "Basic Og==" empty-credentials Basic auth).
//   - WWW-Authenticate: the 401 response challenge from the registry.
//     Contains the realm URL the bearer-token flow points at, which is
//     low-sensitivity on its own but pairs with Authorization in logs.
//   - X-Registry-Auth: the legacy Docker SDK header (base64-encoded
//     auth JSON). Not used in crane's bearer flow but plausible if a
//     future image-pull path goes through moby/moby/client.
//   - Proxy-Authorization: a corporate proxy could inject this; redact
//     defensively to avoid surfacing internal proxy credentials.
var sensitiveHeaders = []string{
	"Authorization",
	"WWW-Authenticate",
	"X-Registry-Auth",
	"Proxy-Authorization",
}

// redactingTransport is the package-local http.RoundTripper that crane
// uses. It is a thin passthrough wrapper around http.DefaultTransport;
// the redaction work is gated to log call sites via SafeHeaders, NOT
// applied to the wire request (which crane needs intact).
//
// The struct itself is unexported and exposed only via
// NewRedactingTransport — callers cannot accidentally construct one
// without the wrapped transport set.
type redactingTransport struct {
	wrapped http.RoundTripper
}

// NewRedactingTransport returns an http.RoundTripper that wraps
// http.DefaultTransport. crane.WithTransport accepts this directly:
//
//	resolver := registry.NewResolver(registry.NewRedactingTransport())
//
// The constructor returns the interface (NOT the concrete struct
// pointer) so callers cannot reach into the struct's internals. WR-04
// pattern from internal/docker/moby.go.
func NewRedactingTransport() http.RoundTripper {
	return &redactingTransport{wrapped: http.DefaultTransport}
}

// RoundTrip delegates to the wrapped http.RoundTripper without
// modification. The "redacting" half is in SafeHeaders, applied at log
// call sites. See package doc for the wire-unchanged invariant.
func (t *redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.wrapped.RoundTrip(req)
}

// SafeHeaders returns a shallow copy of h with sensitive keys removed.
// Use this at every slog.Debug / slog.Info / slog.Warn / slog.Error
// call site that surfaces an http.Header value (today: none in
// internal/registry/; future-proofing OBS-04).
//
// The input header is unmodified — h.Clone() returns a fresh map so
// callers can pass req.Header without disturbing the wire request that
// is about to be sent.
func SafeHeaders(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range sensitiveHeaders {
		out.Del(k)
	}
	return out
}
