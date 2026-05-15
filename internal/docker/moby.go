// SDK shape recorded on 2026-05-13 — see _sdk_shape.txt for the canonical record.
//
// The block below is the in-source mirror of internal/docker/_sdk_shape.txt
// (verbatim `go doc` output for moby/moby/client v0.4.1). Both files exist
// on purpose: a reviewer reading moby.go in isolation sees the contract the
// adapter was written against, and CI greps _sdk_shape.txt for mechanical
// drift detection (every client.Xxx identifier referenced below must appear
// in _sdk_shape.txt — see plan 02-01-PLAN.md must_haves.observables).
//
// On SDK bump: regenerate _sdk_shape.txt AND refresh this comment block in
// the same commit as the moby.go body edit. The two artifacts must stay in
// lockstep — T-02-01-06 (SDK shape drift).
//
// $ go doc github.com/moby/moby/client ContainerListOptions
//   type ContainerListOptions struct {
//       Size    bool
//       All     bool
//       Limit   int
//       Filters Filters
//       // deprecated: Latest / Since / Before
//   }
//
// $ go doc github.com/moby/moby/client ContainerListResult
//   type ContainerListResult struct {
//       Items []container.Summary
//   }
//
// $ go doc github.com/moby/moby/client ContainerInspect
//   func (cli *Client) ContainerInspect(ctx context.Context, containerID string, options ContainerInspectOptions) (ContainerInspectResult, error)
//
// $ go doc github.com/moby/moby/client ContainerInspectOptions
//   type ContainerInspectOptions struct {
//       Size bool
//   }
//
// $ go doc github.com/moby/moby/client ContainerInspectResult
//   type ContainerInspectResult struct {
//       Container container.InspectResponse
//       Raw       json.RawMessage
//   }
//
// $ go doc github.com/moby/moby/client EventsListOptions
//   type EventsListOptions struct {
//       Since   string
//       Until   string
//       Filters Filters
//   }
//
// $ go doc github.com/moby/moby/client Events
//   func (cli *Client) Events(ctx context.Context, options EventsListOptions) EventsResult
//
// $ go doc github.com/moby/moby/client EventsResult
//   type EventsResult struct {
//       Messages <-chan events.Message
//       Err      <-chan error
//   }
//
// $ go doc github.com/moby/moby/client ImageInspect
//   func (cli *Client) ImageInspect(ctx context.Context, imageID string, inspectOpts ...ImageInspectOption) (ImageInspectResult, error)
//
// $ go doc github.com/moby/moby/client ImageInspectResult
//   type ImageInspectResult struct {
//       image.InspectResponse
//   }
//
// $ go doc github.com/moby/moby/client ImagePull
//   func (cli *Client) ImagePull(ctx context.Context, refStr string, options ImagePullOptions) (ImagePullResponse, error)
//
// $ go doc github.com/moby/moby/client ImagePullOptions
//   type ImagePullOptions struct {
//       All          bool
//       RegistryAuth string
//       PrivilegeFunc func(context.Context) (string, error)
//       Platforms    []ocispec.Platform
//   }
//
// $ go doc github.com/moby/moby/client ImagePullResponse
//   type ImagePullResponse interface {
//       io.ReadCloser
//       JSONMessages(ctx context.Context) iter.Seq2[jsonstream.Message, error]
//       Wait(ctx context.Context) error
//   }
//
// $ go doc github.com/moby/moby/client ImageTag
//   func (cli *Client) ImageTag(ctx context.Context, options ImageTagOptions) (ImageTagResult, error)
//
// $ go doc github.com/moby/moby/client ImageTagOptions
//   type ImageTagOptions struct {
//       Source string
//       Target string
//   }
//
// $ go doc github.com/moby/moby/client ImageTagResult
//   type ImageTagResult struct{}
//
// $ go doc github.com/moby/moby/client Ping
//   func (cli *Client) Ping(ctx context.Context, options PingOptions) (PingResult, error)
//
// $ go doc github.com/moby/moby/client PingOptions
//   type PingOptions struct {
//       NegotiateAPIVersion bool
//       ForceNegotiate      bool
//   }
//
// $ go doc github.com/moby/moby/client PingResult
//   type PingResult struct {
//       APIVersion     string
//       OSType         string
//       Experimental   bool
//       BuilderVersion build.BuilderVersion
//       SwarmStatus    *SwarmStatus
//   }
//
// END SDK SHAPE CAPTURE — see _sdk_shape.txt for the canonical record.

package docker

import (
	"context"
	"fmt"
	"io"

	"github.com/moby/moby/client"
)

// mobyClient adapts *client.Client to the internal/docker.Client interface.
//
// It is safe for concurrent use — the embedded *client.Client documents its
// own goroutine-safety contract (see moby/moby/client package doc and
// CONTEXT.md "### Concurrency Invariants"). No extra locking is needed in
// this struct.
type mobyClient struct {
	c *client.Client
}

// NewClient constructs a Client (concretely *mobyClient) using FromEnv (so
// DOCKER_HOST works for tests and the e2e stack) and
// WithAPIVersionNegotiation (so we don't pin a specific Engine API
// version — the daemon and client agree at first call).
//
// WR-04: the return type is the Client interface, NOT *mobyClient. The
// mobyClient struct is unexported and exposing a pointer to it across the
// package boundary forces callers into "ill-formed import" territory if
// they ever want to assign to a typed variable. Returning the interface
// keeps the call site (cmd/hmi-update/main.go) trivially substitutable
// with a test fake or a future second implementation.
//
// Failure modes are wrapped with the "docker.NewClient" prefix so operators
// can grep boot logs for the construction-failure surface. The wrapping is
// also the contract the moby_test.go tests pin (TestNewClient_FromEnv_DefaultSocket).
//
// Note on threat T-02-01-02: we deliberately do NOT echo DOCKER_HOST into
// the error message — the moby SDK's own error (e.g. "Cannot connect to
// the Docker daemon at unix:///var/run/docker.sock") is documented user
// advice (Pitfall 9), not an internal-path leak. The /healthz handler in
// plan 02-04 is the layer that scrubs error surfaces for HTTP responses.
//
// Note on ctx: the moby SDK's NewClientWithOpts does not currently take a
// context. We accept one anyway so the constructor can grow cancellation
// support without a signature break — and so the call site in main.go
// reads naturally alongside the other context-taking constructors.
func NewClient(ctx context.Context) (Client, error) {
	_ = ctx // reserved for future cancellation-aware construction
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker.NewClient: %w", err)
	}
	return &mobyClient{c: c}, nil
}

// Ping calls client.Ping and discards the PingResult — hmi-update only
// cares about daemon reachability. The /healthz handler (plan 02-04) is
// the sole caller.
func (m *mobyClient) Ping(ctx context.Context) error {
	_, err := m.c.Ping(ctx, client.PingOptions{})
	return err
}

// ContainerList unwraps the SDK's ContainerListResult{Items} into a flat
// []ContainerSummary slice. The unwrap happens here so callers don't need
// to know the SDK's wrapper shape — they iterate the slice as documented
// in the Client interface contract.
func (m *mobyClient) ContainerList(ctx context.Context, opts ContainerListOptions) ([]ContainerSummary, error) {
	res, err := m.c.ContainerList(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("docker.ContainerList: %w", err)
	}
	return res.Items, nil
}

// ContainerInspect passes the wrapper struct through unchanged. The
// Client interface aliases it as ContainerInspect so callers reach for
// res.Container.* for typed access (or res.Raw for forward-compat fields
// the SDK hasn't typed yet).
func (m *mobyClient) ContainerInspect(ctx context.Context, id string) (ContainerInspect, error) {
	res, err := m.c.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return ContainerInspect{}, fmt.Errorf("docker.ContainerInspect %s: %w", id, err)
	}
	return res, nil
}

// Events unpacks the SDK's EventsResult{Messages, Err} into the two
// channels the Client.Events contract promises. Both channels are owned
// by the SDK — closing semantics are documented in the SDK (Events
// closes Err with io.EOF when the stream ends; cancelling ctx is the
// caller's signal to stop). Plan 02-03's discovery goroutine treats
// either signal as "reconnect with exponential backoff."
//
// The SDK returns channels of concrete events.Message and concrete
// error, which already satisfy the EventMessage / error alias contract
// in client.go — no re-channelling goroutine is needed (vs. the
// iterator-adapter pattern hinted at in the plan skeleton).
func (m *mobyClient) Events(ctx context.Context, opts EventsListOptions) (<-chan EventMessage, <-chan error) {
	res := m.c.Events(ctx, opts)
	return res.Messages, res.Err
}

// ImagePull returns the SDK's ImagePullResponse — which is itself an
// io.ReadCloser carrying the pull-progress JSON stream. Plan 04 callers
// can either io.Copy to drain (the "pull and forget progress" path) or
// iterate JSONMessages on the typed interface (the "stream progress to
// the UI" path). The facade keeps the return type as io.ReadCloser so
// callers aren't forced to import the SDK to satisfy the interface.
func (m *mobyClient) ImagePull(ctx context.Context, ref string, opts ImagePullOptions) (io.ReadCloser, error) {
	res, err := m.c.ImagePull(ctx, ref, opts)
	if err != nil {
		return nil, fmt.Errorf("docker.ImagePull %s: %w", ref, err)
	}
	return res, nil
}

// ImageInspect passes the wrapper struct through unchanged. discovery.upsertFromInspect
// reads res.RepoDigests[0] via the embedded image.InspectResponse to derive
// Container.CurrentDigest (the registry manifest digest the Phase 3 poller's
// flip-rule compares against). The variadic ImageInspectOption slot is left
// empty — defaults (no manifests, no platform pin, no raw response capture)
// match what discovery needs.
func (m *mobyClient) ImageInspect(ctx context.Context, ref string) (ImageInspect, error) {
	res, err := m.c.ImageInspect(ctx, ref)
	if err != nil {
		return ImageInspect{}, fmt.Errorf("docker.ImageInspect %s: %w", ref, err)
	}
	return res, nil
}

// ImageTag flattens the SDK's (ctx, ImageTagOptions{Source, Target})
// signature into (ctx, src, dst) for ergonomic call sites in Phase 4's
// rollback path. The SDK's ImageTagResult is an empty struct — there is
// nothing useful to return.
func (m *mobyClient) ImageTag(ctx context.Context, src, dst string) error {
	if _, err := m.c.ImageTag(ctx, client.ImageTagOptions{Source: src, Target: dst}); err != nil {
		return fmt.Errorf("docker.ImageTag %s -> %s: %w", src, dst, err)
	}
	return nil
}
