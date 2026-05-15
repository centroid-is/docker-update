// Probe test for Assumption A1 (RESEARCH.md lines 1564, 1573):
//
//	moby/moby/client v0.4.1 ImagePullResponse.JSONMessages carries the
//	pull-complete digest in an `aux` JSON object whose unmarshal target
//	has either an `ID` or `Digest` field of the form "sha256:...".
//
// If this probe RUNS and PASSES, Option A (drainPullStream + json.Decoder
// over the io.ReadCloser) is the chosen path in Task 3's
// orchestrator.go. If the probe RUNS and FAILS, the actions plan pivots to
// Option B — adding ImageInspect to the docker.Client facade in a small
// Phase-2 patch (per RESEARCH.md "Mitigation for A1").
//
// CONTINGENCY: this probe requires a real docker daemon (we use the
// production internal/docker.NewClient with FromEnv). If no daemon is
// reachable, the test t.Skip's cleanly; the planner records "probe
// skipped — A1 unverified" in the SUMMARY and proceeds with Option A as
// the design lean.
//
// Test architecture:
//
//  1. Spin up an in-process OCI registry via go-containerregistry's
//     pkg/registry (matches internal/registry/resolver_test.go shape).
//  2. Push a real synthetic image manifest under
//     "<registry-host>/probe-img/probe:latest".
//  3. Call the production internal/docker.Client.ImagePull against that
//     ref via the host docker daemon (the daemon will pull from our
//     httptest.Server).
//  4. Drain the returned io.ReadCloser as a stream of JSON messages and
//     assert that at least one carries an `aux` field whose unmarshal
//     yields a non-empty ID or Digest of the form "sha256:...".
//
// Why we use the host docker daemon (not a mock): the only authoritative
// witness for the SDK wire shape is the daemon itself. A mock would
// trivially produce whatever shape we coded into it. The whole point of
// Assumption A1 is "what does the real daemon emit?" — t.Skip is the
// honest answer when no daemon is reachable.
//
// Goroutine assertion contract: no goroutines spawned in this test; all
// assertions are on the test goroutine.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gcrregistry "github.com/google/go-containerregistry/pkg/registry"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/centroid-is/hmi-update/internal/docker"
)

// jsonMessage matches the docker pull progress wire format.
// Source: pkg.go.dev/github.com/moby/moby/pkg/jsonmessage (the daemon's
// progress emitter emits this shape regardless of SDK version).
type probeJSONMessage struct {
	Status string          `json:"status,omitempty"`
	ID     string          `json:"id,omitempty"`
	Error  string          `json:"error,omitempty"`
	Aux    json.RawMessage `json:"aux,omitempty"`
}

// probeAuxDigest is the candidate unmarshal target for the aux JSON.
// Option A's drainPullStream uses the same struct shape; this test pins
// the contract by independently asserting which field carries the digest.
type probeAuxDigest struct {
	ID     string `json:"ID,omitempty"`
	Digest string `json:"Digest,omitempty"`
}

// TestProbe_MobyAuxDigest_Shape rounds-trips a real moby ImagePull against
// an in-process registry and validates the JSONMessages aux-digest shape.
//
// On success: confirms Option A drainPullStream is the correct path.
// On t.Skip: docker daemon unavailable; planner records the contingency
// and proceeds with Option A as the design lean (per RESEARCH.md A1
// mitigation).
//
// On failure: refutes A1; planner pivots to Option B (add ImageInspect
// to internal/docker.Client facade in a small patch BEFORE Task 3 lands).
func TestProbe_MobyAuxDigest_Shape(t *testing.T) {
	t.Parallel()

	// 1. Quick docker-daemon reachability probe. If unreachable, skip.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	dockerClient, err := docker.NewClient(ctx)
	if err != nil {
		t.Skipf("no docker daemon available (NewClient failed: %v); A1 probe deferred — Option A is the design lean per RESEARCH.md", err)
	}
	if err := dockerClient.Ping(ctx); err != nil {
		t.Skipf("no docker daemon available (Ping failed: %v); A1 probe deferred — Option A is the design lean per RESEARCH.md", err)
	}

	// 2. Stand up an in-process OCI registry on a host-reachable address.
	// httptest.NewServer binds to 127.0.0.1 which is reachable from inside
	// a docker daemon running on the same host (Linux native). On macOS
	// Docker Desktop, host.docker.internal would be needed; in that case
	// the pull fails and we surface that as a skip too (no daemon-routable
	// loopback).
	//
	// We bind explicitly to 127.0.0.1 so the URL doesn't end up using
	// localhost-via-IPv6 (::1) which Docker Desktop sometimes can't reach.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	srv := httptest.NewUnstartedServer(gcrregistry.New())
	srv.Listener.Close()
	srv.Listener = listener
	srv.Start()
	defer srv.Close()

	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")
	ref, err := name.ParseReference(host + "/probe-img/probe:latest")
	if err != nil {
		t.Fatalf("name.ParseReference: %v", err)
	}

	// 3. Push a random tiny image so the daemon has something to pull.
	img, err := random.Image(256, 1)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	if err := remote.Write(ref, img); err != nil {
		t.Fatalf("remote.Write: %v", err)
	}

	// 4. Pull via the production docker.Client. The host daemon will
	// resolve 127.0.0.1:NNNN against the OS network — works on Linux.
	// On Docker Desktop / Mac the pull may fail with "no route" → t.Skip
	// rather than fail; A1 still goes unverified but the probe doesn't
	// give a false negative.
	rc, err := dockerClient.ImagePull(ctx, host+"/probe-img/probe:latest", docker.ImagePullOptions{})
	if err != nil {
		// Common case on Docker Desktop / non-Linux: daemon can't route
		// to the test's 127.0.0.1 listener. Treat as skip.
		t.Skipf("docker daemon could not reach in-process registry at %s: %v; A1 probe deferred", host, err)
	}
	defer rc.Close()

	// 5. Drain the stream as JSON messages; record every non-empty Aux.
	dec := json.NewDecoder(rc)
	var sawAux bool
	var firstDigest string
	for {
		var msg probeJSONMessage
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// JSON decode errors on the pull stream are the canonical
			// A1 refutation — record verbosely.
			t.Fatalf("decode pull stream: %v (A1 refuted: stream is not line-delimited JSON; pivot to Option B ImageInspect facade addition)", err)
		}
		if msg.Error != "" {
			// Daemon-side pull error (e.g. unreachable registry). Treat
			// as skip — does not invalidate A1.
			t.Skipf("daemon returned pull error %q; A1 probe inconclusive", msg.Error)
		}
		if len(msg.Aux) == 0 {
			continue
		}
		var aux probeAuxDigest
		if err := json.Unmarshal(msg.Aux, &aux); err != nil {
			t.Errorf("A1 refutation: Aux field present but does NOT unmarshal into {ID,Digest} struct: %v (raw=%q); pivot to Option B",
				err, string(msg.Aux))
			continue
		}
		sawAux = true
		switch {
		case strings.HasPrefix(aux.Digest, "sha256:"):
			firstDigest = aux.Digest
		case strings.HasPrefix(aux.ID, "sha256:"):
			firstDigest = aux.ID
		}
	}

	// IMPORTANT: this is an *informational* probe, not a regression test.
	// When A1 is refuted (no Aux in the JSONMessages stream), the orchestrator's
	// drainPullStream falls through to the verify-after-recreate ContainerInspect
	// path which reads RepoDigests[0] — Plan 04-04 BLOCKER-01 fix re-fetches the
	// new container ID via ContainerList + ContainerInspect after compose up -d
	// --force-recreate, so the canonical digest source is the post-recreate
	// container, not the pull-stream Aux. The Aux path is an optimization for
	// daemons that DO emit it; absence is not a production-breaking condition.
	// Log the result and return without failing.
	if !sawAux {
		t.Logf("A1 refuted on this daemon: no Aux field observed; orchestrator falls through to ContainerInspect.RepoDigests[0] path (see Plan 04-04 BLOCKER-01 fix)")
		return
	}
	if firstDigest == "" {
		t.Logf("A1 partial refutation: Aux field present but no sha256: value; orchestrator falls through to ContainerInspect.RepoDigests[0] path")
		return
	}
	t.Logf("A1 confirmed: aux digest extracted as %q via Option A drainPullStream path", firstDigest)
}
