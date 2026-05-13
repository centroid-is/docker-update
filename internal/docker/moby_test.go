// RED-FIRST per C4. These tests are authored before internal/docker/moby.go
// exists. Plan 02-01 (Wave 1) drives them green by implementing NewClient,
// the mobyClient adapter, and the six-method Client interface.
//
// What these tests guard:
//   - TestMobyClient_SatisfiesClient (compile-time): if anyone ever drifts the
//     mobyClient method set away from the Client interface, the build breaks
//     at this var declaration — the single most important guard in this file.
//   - TestNewClient_FromEnv_DefaultSocket: NewClient with no DOCKER_HOST env
//     either returns a *mobyClient with nil error, or returns an error that
//     wraps with the "docker.NewClient" prefix. No real daemon call.
//   - TestNewClient_BadDockerHost: an invalid DOCKER_HOST string EITHER
//     returns a non-nil client (the moby SDK defers connection until first
//     call) OR returns an error wrapped with the "docker.NewClient" prefix.
//     Documents the actual SDK behaviour found in v0.4.1.
//   - TestClient_InterfaceMethodCount: reflect-based guard that the Client
//     interface has exactly six methods. Trips if anyone silently adds a
//     seventh method without a coordinated decision (CONTEXT.md "Claude's
//     Discretion" allows the six listed; growth requires a deliberate edit
//     of this test).
package docker

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestMobyClient_SatisfiesClient is the load-bearing compile-time assertion.
// If the Client interface or the mobyClient method set ever drift, the build
// fails on the line below — not at runtime, not in production.
//
// This is the single most important test in the package: it prevents a class
// of regression where someone adds a method to Client without implementing
// it on mobyClient, or removes a method from mobyClient that Client still
// requires.
func TestMobyClient_SatisfiesClient(t *testing.T) {
	t.Parallel()
	var _ Client = (*mobyClient)(nil)
}

// TestNewClient_FromEnv_DefaultSocket verifies the constructor's return
// shape with no DOCKER_HOST set. The test does NOT actually contact the
// docker daemon; it only asserts the (client, error) tuple is well-formed
// and any error is wrapped with the documented "docker.NewClient" prefix
// (threat T-02-01-02: error wrapping must not leak DOCKER_HOST values).
func TestNewClient_FromEnv_DefaultSocket(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	c, err := NewClient(context.Background())
	if err != nil {
		// Acceptable: NewClient surfaced a construction failure. The
		// wrap prefix is mandatory so operators can grep boot logs.
		if !strings.Contains(err.Error(), "docker.NewClient") {
			t.Errorf("NewClient error: want prefix 'docker.NewClient', got: %v", err)
		}
		return
	}
	// Acceptable: NewClient returned a real client. The moby SDK defers
	// daemon contact until the first method call, so construction can
	// succeed even when no daemon is running.
	if c == nil {
		t.Errorf("NewClient: returned nil client with nil error — pick one")
	}
}

// TestNewClient_BadDockerHost documents the moby/moby/client v0.4.1
// behaviour for a syntactically valid but unresolvable DOCKER_HOST: the
// SDK accepts the URL at construction time and defers connection until
// the first method call. Either outcome is acceptable for the constructor
// itself — what the test pins down is that IF an error fires, the wrap
// prefix is in place.
//
// Captured behaviour (2026-05-13, client v0.4.1): NewClient returns a
// non-nil *mobyClient with nil error for this DOCKER_HOST. The first
// daemon call (Ping/ContainerList/etc.) would surface the connection
// failure. We don't make any such call here — that's healthz's job
// (plan 02-04).
func TestNewClient_BadDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://invalid-host-name-that-does-not-resolve:9999")
	c, err := NewClient(context.Background())
	if err != nil {
		if !strings.Contains(err.Error(), "docker.NewClient") {
			t.Errorf("NewClient error on bad DOCKER_HOST: want prefix 'docker.NewClient', got: %v", err)
		}
		return
	}
	if c == nil {
		t.Errorf("NewClient: nil client with nil error on bad DOCKER_HOST")
	}
}

// TestClient_InterfaceMethodCount is a reflection-based guard that the
// Client interface has exactly six methods. The six are (in declaration
// order): Ping, ContainerList, ContainerInspect, Events, ImagePull,
// ImageTag.
//
// If a future plan adds a seventh method, this test fails — that's the
// signal to update the interface's doc comment, the threat register
// (T-02-01-04), and this constant in the same PR.
func TestClient_InterfaceMethodCount(t *testing.T) {
	t.Parallel()
	const want = 6
	got := reflect.TypeOf((*Client)(nil)).Elem().NumMethod()
	if got != want {
		t.Errorf("Client interface method count: want %d, got %d — coordinate the change (see threat T-02-01-04)", want, got)
	}
}
