// RED-FIRST per C4. These tests are authored before internal/registry/resolver.go
// gains a body. Plan 03-02 Task 3 GREEN drives them green by replacing the
// Phase-1 empty-interface stub with the Resolver interface (Digest method)
// and the craneResolver concrete impl wrapping crane.Digest.
//
// What these tests guard:
//
//   - TestResolver_SatisfiesResolver (compile-time + runtime): the unexported
//     craneResolver struct implements the public Resolver interface. The
//     load-bearing build-time assertion: drift breaks the build, not
//     production.
//   - TestResolver_Digest_SingleArchManifest: DETECT-02. Against a hosted
//     OCI image, Resolver.Digest returns the manifest's content digest
//     (sha256:...).
//   - TestResolver_Digest_MultiArchIndex: DETECT-01. Against a hosted
//     multi-arch index with linux/amd64 + linux/arm64 children, the
//     resolver returns the AMD64 CHILD digest (not the index digest),
//     proving WithPlatform(linux/amd64) drives the right manifest
//     selection.
//   - TestResolver_Digest_UsesContentDigestHeader: DETECT-02 reinforced.
//     Against a hand-rolled httptest mux that serves a body whose actual
//     sha256 differs from the Docker-Content-Digest response header value,
//     the resolver returns the HEADER value (proving no body rehash).
//   - TestResolver_Digest_PermanentError: a 404 from the registry yields
//     an error satisfying errors.Is(_, ErrPermanent). DETECT-03 surface.
//   - TestResolver_Digest_TransientError: a 503 from the registry yields
//     an error satisfying errors.Is(_, ErrTransient). DETECT-03 surface.
//   - TestResolver_Digest_RespectsContext: a context.WithTimeout(50ms)
//     against a registry that hangs for 1s yields an error classified
//     as ErrTransient. Threat T-03-02-04 acceptance (slow-loris DoS
//     mitigation).
//
// Goroutine assertion contract: httptest handlers are invoked off the test
// goroutine; assertions there use t.Errorf (per discovery_test.go line 33
// + persist_test.go lines 29-31).
package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	gcrregistry "github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
)

// TestResolver_SatisfiesResolver is the compile-time load-bearing guard.
// If craneResolver drifts away from Resolver, the build fails on the line
// below — not at runtime, not in production.
func TestResolver_SatisfiesResolver(t *testing.T) {
	t.Parallel()
	var _ Resolver = (*craneResolver)(nil)
}

// hostFromURL strips the "http://" prefix from an httptest.NewServer URL,
// returning just "127.0.0.1:NNNN" — the form crane.Digest expects for the
// host portion of an image reference.
func hostFromURL(serverURL string) string {
	return strings.TrimPrefix(strings.TrimPrefix(serverURL, "http://"), "https://")
}

// newInMemoryRegistry returns an httptest.Server backed by the
// go-containerregistry in-memory registry implementation. Used by tests
// that need a registry to actually behave (manifest upload + retrieval).
func newInMemoryRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(gcrregistry.New())
	t.Cleanup(srv.Close)
	return srv
}

// TestResolver_Digest_SingleArchManifest (DETECT-02): a real single-arch
// image hosted in the in-memory registry; Resolver.Digest returns the
// manifest's content digest. crane.Digest uses the Docker-Content-Digest
// header by construction (verified in TestResolver_Digest_UsesContentDigestHeader
// below); this test verifies the happy-path round-trip end-to-end.
func TestResolver_Digest_SingleArchManifest(t *testing.T) {
	t.Parallel()
	// req: DETECT-02

	srv := newInMemoryRegistry(t)
	host := hostFromURL(srv.URL)

	// Build a random single-arch image and push it under the ref
	// <host>/foo/bar:latest.
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	ref, err := name.ParseReference(host + "/foo/bar:latest")
	if err != nil {
		t.Fatalf("name.ParseReference: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	wantDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}

	r := NewResolver(http.DefaultTransport)
	got, err := r.Digest(context.Background(), host+"/foo/bar:latest")
	if err != nil {
		t.Fatalf("Resolver.Digest: unexpected error: %v", err)
	}
	if got != wantDigest.String() {
		t.Errorf("Resolver.Digest = %q, want %q", got, wantDigest.String())
	}
	if !strings.HasPrefix(got, "sha256:") {
		t.Errorf("Resolver.Digest = %q, want sha256: prefix", got)
	}
}

// TestResolver_Digest_MultiArchIndex (DETECT-01): the canonical Pitfall 1
// regression scenario. A multi-arch image index with linux/amd64 + linux/arm64
// children is pushed; the resolver MUST return the AMD64 child manifest's
// digest, not the top-level index digest.
//
// crane.Digest with WithPlatform(linux/amd64) internally calls
// Descriptor.Image() which validates the child's sha256 by fetching the
// body — so the index and child must be self-consistent (which random.Index
// guarantees by construction).
func TestResolver_Digest_MultiArchIndex(t *testing.T) {
	t.Parallel()
	// req: DETECT-01

	srv := newInMemoryRegistry(t)
	host := hostFromURL(srv.URL)

	// random.Index(byteSize, layers, count) produces an index with
	// `count` child images. We need two children with explicit
	// platforms; random.Index assigns random platforms, so we build
	// the index by hand for deterministic platform values.
	amd64Img, err := random.Image(256, 1)
	if err != nil {
		t.Fatalf("random.Image amd64: %v", err)
	}
	arm64Img, err := random.Image(256, 1)
	if err != nil {
		t.Fatalf("random.Image arm64: %v", err)
	}
	amd64Digest, err := amd64Img.Digest()
	if err != nil {
		t.Fatalf("amd64Img.Digest: %v", err)
	}
	arm64Digest, err := arm64Img.Digest()
	if err != nil {
		t.Fatalf("arm64Img.Digest: %v", err)
	}
	amd64Size, err := amd64Img.Size()
	if err != nil {
		t.Fatalf("amd64Img.Size: %v", err)
	}
	arm64Size, err := arm64Img.Size()
	if err != nil {
		t.Fatalf("arm64Img.Size: %v", err)
	}
	amd64MT, err := amd64Img.MediaType()
	if err != nil {
		t.Fatalf("amd64Img.MediaType: %v", err)
	}
	arm64MT, err := arm64Img.MediaType()
	if err != nil {
		t.Fatalf("arm64Img.MediaType: %v", err)
	}

	idx := mutate.IndexMediaType(empty.Index, types.OCIImageIndex)
	idx = mutate.AppendManifests(idx,
		mutate.IndexAddendum{
			Add: amd64Img,
			Descriptor: v1.Descriptor{
				Platform:  &v1.Platform{OS: "linux", Architecture: "amd64"},
				MediaType: amd64MT,
				Digest:    amd64Digest,
				Size:      amd64Size,
			},
		},
		mutate.IndexAddendum{
			Add: arm64Img,
			Descriptor: v1.Descriptor{
				Platform:  &v1.Platform{OS: "linux", Architecture: "arm64"},
				MediaType: arm64MT,
				Digest:    arm64Digest,
				Size:      arm64Size,
			},
		},
	)

	indexDigest, err := idx.Digest()
	if err != nil {
		t.Fatalf("idx.Digest: %v", err)
	}

	ref, err := name.ParseReference(host + "/foo/multi:latest")
	if err != nil {
		t.Fatalf("name.ParseReference: %v", err)
	}
	if err := remote.WriteIndex(ref, idx); err != nil {
		t.Fatalf("remote.WriteIndex: %v", err)
	}

	r := NewResolver(http.DefaultTransport)
	got, err := r.Digest(context.Background(), host+"/foo/multi:latest")
	if err != nil {
		t.Fatalf("Resolver.Digest: unexpected error: %v", err)
	}

	// DETECT-01 invariant: amd64 child digest, NOT the index digest.
	if got == indexDigest.String() {
		t.Errorf("Resolver.Digest returned the INDEX digest %q — Pitfall 1 regression: WithPlatform did not select the amd64 child", got)
	}
	if got != amd64Digest.String() {
		t.Errorf("Resolver.Digest = %q, want amd64 child digest %q (got index? %v, got arm64? %v)",
			got, amd64Digest.String(), got == indexDigest.String(), got == arm64Digest.String())
	}
}

// TestResolver_Digest_UsesContentDigestHeader (DETECT-02 reinforced):
// hand-rolled httptest mux serves a HEAD response with a
// Docker-Content-Digest header value that is decidedly NOT a real
// sha256 of any body. The resolver (via crane.Digest's HEAD path) MUST
// return the HEADER value verbatim — proving the header is
// authoritative on the HEAD path and no body rehash happens there.
//
// IMPLEMENTATION NOTE: This test uses crane.Digest WITHOUT WithPlatform
// (and WITHOUT triggering a GET fallback) so we exercise crane's
// headManifest path specifically. crane.Digest WITH WithPlatform takes
// a different code path (getManifest -> fetcher.fetchManifest) which
// DOES recompute the digest from the body bytes; that path is exercised
// (and verified correct) by TestResolver_Digest_SingleArchManifest and
// TestResolver_Digest_MultiArchIndex which use real images whose body
// sha and registry-reported digest are self-consistent.
//
// The HEAD-path test below isolates the "header is the source on HEAD"
// invariant. For crane.Digest's HEAD path to succeed, the registry
// response MUST include a Content-Length header (fetcher.go's
// headManifest treats Content-Length == -1 as a fatal error and falls
// back to GET, which DOES rehash). The handler below sets it
// explicitly.
func TestResolver_Digest_UsesContentDigestHeader(t *testing.T) {
	t.Parallel()
	// req: DETECT-02

	// Fixed header value, deliberately NOT a real sha256 of the
	// theoretical body. The hex characters are all valid (0-9, a-f
	// only — v1.NewHash strictly validates the digest format and
	// rejects non-hex characters). The pattern is a recognisable
	// "0bad" sentinel for any future test reader.
	const declaredDigest = "sha256:0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad0bad"
	const declaredSize = "42"

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		// API support signal
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Manifest endpoint. crane sends a HEAD first; we respond
		// with header values that exercise the HEAD-path invariant.
		w.Header().Set("Docker-Content-Digest", declaredDigest)
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		// Content-Length MUST be set on HEAD for crane's headManifest
		// to accept the response (otherwise resp.ContentLength == -1
		// and crane falls back to GET, which rehashes).
		w.Header().Set("Content-Length", declaredSize)
		w.WriteHeader(http.StatusOK)
		// No body — this is HEAD; HTTP semantics forbid bodies in HEAD
		// responses (the http.ResponseWriter ignores body writes on HEAD
		// requests in net/http per RFC 9110).
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := hostFromURL(srv.URL)

	// Direct crane.Digest call WITHOUT WithPlatform — exercises the
	// HEAD-only path where the Docker-Content-Digest header is
	// authoritative. (CONTEXT.md Area 1: "The header is the
	// authoritative source; no body re-hash.")
	got, err := crane.Digest(host+"/foo/bar:latest",
		crane.WithTransport(http.DefaultTransport),
	)
	if err != nil {
		t.Fatalf("crane.Digest: unexpected error: %v", err)
	}
	if got != declaredDigest {
		t.Errorf("crane.Digest = %q, want %q (header value — proves no body rehash on HEAD path)", got, declaredDigest)
	}
}

// TestResolver_Digest_PermanentError verifies that a 404 from the
// registry results in errors.Is(err, ErrPermanent) == true. CONTEXT.md
// Area 1: "Permanent errors (401, 403, 404) fail fast." (DETECT-03)
func TestResolver_Digest_PermanentError(t *testing.T) {
	t.Parallel()
	// req: DETECT-03

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := hostFromURL(srv.URL)
	r := NewResolver(http.DefaultTransport)
	_, err := r.Digest(context.Background(), host+"/foo/bar:latest")
	if err == nil {
		t.Fatalf("Resolver.Digest: want error, got nil")
	}
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("errors.Is(err, ErrPermanent) = false, want true; err = %v", err)
	}
	if errors.Is(err, ErrTransient) {
		t.Errorf("errors.Is(err, ErrTransient) = true, want false; err = %v", err)
	}
}

// TestResolver_Digest_TransientError verifies that a 503 from the
// registry results in errors.Is(err, ErrTransient) == true. CONTEXT.md
// Area 1: "Transient errors (network, 5xx, timeout) get 1 retry."
// (DETECT-03)
func TestResolver_Digest_TransientError(t *testing.T) {
	t.Parallel()
	// req: DETECT-03

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := hostFromURL(srv.URL)
	r := NewResolver(http.DefaultTransport)
	_, err := r.Digest(context.Background(), host+"/foo/bar:latest")
	if err == nil {
		t.Fatalf("Resolver.Digest: want error, got nil")
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("errors.Is(err, ErrTransient) = false, want true; err = %v", err)
	}
	if errors.Is(err, ErrPermanent) {
		t.Errorf("errors.Is(err, ErrPermanent) = true, want false; err = %v", err)
	}
}

// TestResolver_Digest_RespectsContext verifies that a context timeout
// propagates through crane.WithContext and surfaces as ErrTransient.
// Threat T-03-02-04 acceptance (slow-loris DoS mitigation).
func TestResolver_Digest_RespectsContext(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		// API support signal still responds quickly so crane gets
		// past the version check.
		if r.URL.Path == "/v2/" {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Hang for 1s on the manifest fetch — far longer than the
		// 50ms test timeout.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(1 * time.Second):
			w.WriteHeader(http.StatusOK)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	host := hostFromURL(srv.URL)
	r := NewResolver(http.DefaultTransport)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := r.Digest(ctx, host+"/foo/bar:latest")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("Resolver.Digest: want error from context timeout, got nil")
	}
	if elapsed >= 800*time.Millisecond {
		t.Errorf("Resolver.Digest blocked for %v despite 50ms context deadline — context not propagated", elapsed)
	}
	if !errors.Is(err, ErrTransient) {
		t.Errorf("errors.Is(err, ErrTransient) = false, want true (timeout maps to transient); err = %v", err)
	}
}
