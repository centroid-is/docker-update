// Package registry resolves the upstream digest of a `:tag` ref against
// the OCI registry it lives in (default GHCR for Centroid HMIs).
//
// Architectural anchor (see PATTERNS.md "facade over external SDK"):
//
//	internal/registry is the package-local facade over
//	github.com/google/go-containerregistry. NO package outside
//	internal/registry/ may import google/go-containerregistry directly —
//	a CI grep guard (Phase 8 / CI-01 lint stage) enforces this. The
//	pattern mirrors internal/docker over moby/moby/client.
//
// Why crane.Digest (not hand-rolled HTTP):
//
//	crane.Digest handles the bearer-token flow, multi-arch index
//	resolution via Descriptor.Image(), and Docker-Content-Digest header
//	extraction in one call. This is the layer WUD 8.2.2's two named
//	bugs (Pitfall 1 single-arch digest extraction, Pitfall 2 anonymous
//	Basic Og==) lived inside; using crane closes both by construction.
//	Pitfall 2 is independently asserted in transport_test.go's
//	TestAnonymousFlow_NoBasicHeader regression guard; Pitfall 1 is
//	independently asserted in resolver_test.go's
//	TestResolver_Digest_MultiArchIndex.
//
// SDK shape recorded on 2026-05-14 — see _sdk_shape.txt for the canonical
// record. The block below is the in-source mirror of
// internal/registry/_sdk_shape.txt (verbatim `go doc` output for
// go-containerregistry v0.20.8). Both files exist on purpose: a reviewer
// reading resolver.go in isolation sees the contract the adapter was
// written against, and CI greps _sdk_shape.txt for mechanical drift
// detection.
//
// On SDK bump: regenerate _sdk_shape.txt AND refresh this comment block
// in the same commit as the resolver.go body edit. The two artifacts
// must stay in lockstep.
//
//	$ go doc github.com/google/go-containerregistry/pkg/crane Digest
//	  func Digest(ref string, opt ...Option) (string, error)
//	    Digest returns the sha256 hash of the remote image at ref.
//
//	$ go doc github.com/google/go-containerregistry/pkg/crane WithContext
//	  func WithContext(ctx context.Context) Option
//	    WithContext is a functional option for setting the context.
//
//	$ go doc github.com/google/go-containerregistry/pkg/crane WithAuth
//	  func WithAuth(auth authn.Authenticator) Option
//	    WithAuth is a functional option for overriding the default
//	    authenticator for remote operations. By default, crane will use
//	    the keychain authenticator. <-- WE OVERRIDE TO authn.Anonymous;
//	                              the default keychain is BANNED — Pitfall 2.
//	                              See _sdk_shape.txt FORBIDDEN block for
//	                              the full identifier (kept out of this
//	                              file to satisfy the CI grep guard).
//
//	$ go doc github.com/google/go-containerregistry/pkg/crane WithPlatform
//	  func WithPlatform(platform *v1.Platform) Option
//	    WithPlatform is an Option to specify the platform.
//
//	$ go doc github.com/google/go-containerregistry/pkg/crane WithTransport
//	  func WithTransport(t http.RoundTripper) Option
//	    WithTransport is a functional option for overriding the default
//	    transport for remote operations.
//
//	$ go doc github.com/google/go-containerregistry/pkg/authn Anonymous
//	  var Anonymous Authenticator = &anonymous{}
//	    Anonymous is a singleton Authenticator for providing anonymous
//	    auth.
//
//	$ go doc github.com/google/go-containerregistry/pkg/v1 Platform
//	  type Platform struct {
//	      Architecture string
//	      OS           string
//	      OSVersion    string
//	      OSFeatures   []string
//	      Variant      string
//	      Features     []string
//	  }
//
// END SDK SHAPE CAPTURE — see _sdk_shape.txt for the canonical record.
//
// Phase 3 plan 03-02 lands the body; Plan 03-04 wires it through main.go
// (the cron poller and single-consumer state-update channel).
package registry

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

// Resolver wraps crane.Digest so plan-03-04's poll loop can ask "what is
// the upstream digest of image:tag?" without depending on
// go-containerregistry directly. The interface narrows to the one
// method any caller actually invokes — no other extension point.
//
// Phase 1 declared this as an empty interface stub (`type Resolver
// interface{}`); plan 03-02 replaces that with the method-bearing
// contract below. Any future caller that imports registry.Resolver
// will now bind to the real Digest method.
type Resolver interface {
	// Digest fetches the upstream sha256 for ref. ref is the full
	// image reference including tag, e.g.
	// "ghcr.io/centroid-is/hmi-update:latest" or
	// "registry-1.docker.io/library/busybox:latest".
	//
	// Returns the sha256 digest string (e.g. "sha256:abc...") on
	// success. On failure, returns ("", err) where err is wrapped
	// with either ErrPermanent (401/403/404 — do not retry) or
	// ErrTransient (5xx/timeout/network — retry once). Callers branch
	// on errors.Is.
	Digest(ctx context.Context, ref string) (string, error)
}

// amd64Platform is the hardcoded target platform for Phase 3. CLAUDE.md
// pins amd64-only for v1 (current Centroid elevator-HMI hardware); the
// buildx flip to also produce arm64 images is a CI concern, not an
// in-code one. The TODO marker is greppable so a future ARM-HMI plan
// can find this single source.
//
// TODO(V2-ARM64): wire from runtime.GOARCH when arm64 lands. CLAUDE.md
// "Platform: amd64 only for v1 (matches current HMI hardware); arm64
// is a CI buildx flip later."
var amd64Platform = &v1.Platform{OS: "linux", Architecture: "amd64"}

// craneResolver is the package-local concrete Resolver. It is unexported
// (NewResolver returns the interface — WR-04 pattern from
// internal/docker/moby.go) so callers cannot reach into its internals
// and rebind the transport or auth at the call site.
type craneResolver struct {
	transport http.RoundTripper
	// insecure flips crane to plain-HTTP for every registry reference.
	// Read at construction time from HMI_UPDATE_REGISTRY_INSECURE (any
	// non-empty value enables it). Used EXCLUSIVELY by the e2e harness
	// where zot serves plain-HTTP on the compose network — production
	// HMIs MUST leave this unset so all registry traffic uses HTTPS
	// against ghcr.io / Docker Hub. The transport-side OBS-04 defense
	// is unchanged; this knob affects only the URL scheme crane sends
	// requests to.
	insecure bool
}

// NewResolver returns a Resolver backed by crane.Digest. transport is
// injected so tests can substitute httptest.NewServer's client; the
// production wiring (main.go plan 03-04) passes
// NewRedactingTransport() (transport.go), which wraps
// http.DefaultTransport.
//
// No error return: construction cannot fail since transport is
// injected and no I/O happens at construction time. The interface
// return type (NOT *craneResolver) follows the WR-04 pattern so
// callers can swap in a fake Resolver in tests (the future
// poll/poller_test.go does exactly this).
//
// HMI_UPDATE_REGISTRY_INSECURE env var (Plan 03-05 e2e knob): when set
// to any non-empty value, crane.Insecure is added to every Digest call
// so traffic uses plain HTTP. This is required by the e2e harness
// where zot listens on plain HTTP at zot:5000 (compose network);
// production deployments MUST leave the variable unset so HTTPS is
// enforced against the real registries (ghcr.io, Docker Hub).
func NewResolver(transport http.RoundTripper) Resolver {
	return &craneResolver{
		transport: transport,
		insecure:  os.Getenv("HMI_UPDATE_REGISTRY_INSECURE") != "",
	}
}

// Digest fetches the upstream sha256 for ref. See Resolver.Digest for
// the contract.
//
// Implementation notes (load-bearing — CONTEXT.md Area 1):
//
//   - crane.WithAuth(authn.Anonymous) is mandatory. The default
//     keychain authenticator (see _sdk_shape.txt FORBIDDEN block for
//     the fully-qualified identifier) is the Pitfall 2 footgun: it
//     reads ~/.docker/config.json which on a host that ran `docker
//     login` with an empty username emits Authorization: Basic Og==
//     and breaks GHCR's anonymous bearer flow. The Pitfall 2 regression
//     guard test (transport_test.go TestAnonymousFlow_NoBasicHeader)
//     asserts no inbound request ever carries "Basic Og==". The CI
//     grep guard separately ensures the forbidden identifier never
//     appears as a literal string anywhere in this package's
//     production code.
//
//   - crane.WithPlatform(amd64Platform) is mandatory for multi-arch
//     index handling. Without it, crane.Digest returns the INDEX digest
//     (which doesn't match what the docker daemon reports as
//     RepoDigests[0] after a pull) and the poller misclassifies every
//     multi-arch image as "update available" on every tick. (Pitfall 1.)
//
//   - crane.WithContext(ctx) propagates cancellation. Per-call timeouts
//     layered atop via context.WithTimeout in the poller (Plan 03-03);
//     SIGTERM cancellation unblocks the sweep immediately. Threat
//     T-03-02-04 (slow-loris DoS) mitigated this way.
//
//   - crane.WithTransport(r.transport) routes through the package's
//     redactingTransport in production. Tests pass
//     http.DefaultTransport for direct httptest interaction.
//
// Error handling: any error returned by crane is fed through classify(),
// which wraps it with either ErrPermanent (401/403/404) or
// ErrTransient (5xx, timeout, network, default). The classify() result
// is then wrapped one more time with the "registry.Digest" prefix so
// operators can grep logs for the failing call site.
func (r *craneResolver) Digest(ctx context.Context, ref string) (string, error) {
	opts := []crane.Option{
		crane.WithContext(ctx),
		crane.WithAuth(authn.Anonymous),
		crane.WithPlatform(amd64Platform),
		crane.WithTransport(r.transport),
	}
	if r.insecure {
		// HMI_UPDATE_REGISTRY_INSECURE was set at NewResolver time —
		// e2e harness only. crane.Insecure flips the URL scheme to
		// HTTP for every registry. See type doc-comment.
		opts = append(opts, crane.Insecure)
	}
	digest, err := crane.Digest(ref, opts...)
	if err != nil {
		return "", fmt.Errorf("registry.Digest %s: %w", ref, classify(err))
	}
	return digest, nil
}
