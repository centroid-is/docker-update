// Package docker wraps the moby/moby Docker daemon client used to enumerate
// watched containers and pull fresh images.
//
// Phase 2 fills the interface body below (DOCK-01..04). The interface is
// intentionally narrow — seven operations cover discovery (Ping, ContainerList,
// ContainerInspect, Events, ImageInspect) plus the two actions Phase 4 needs
// (ImagePull, ImageTag). Adding an eighth method requires a coordinated edit
// of the reflect-based method-count guard in moby_test.go
// (TestClient_InterfaceMethodCount).
package docker

import (
	"context"
	"io"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"
)

// Re-exported moby SDK option/result types.
//
// Rationale: every package outside internal/docker imports these aliases —
// never github.com/moby/moby/* directly. A CI grep (plan 02-01-PLAN.md
// must_haves.observables) enforces the boundary. If a future developer
// reaches for the SDK type from a sibling package, the grep fails and the
// abstraction stays intact.
//
// Type-source notes (verified against _sdk_shape.txt — moby/moby/client v0.4.1):
//   - ContainerListOptions / EventsListOptions / ImagePullOptions /
//     PingOptions / Filters live in the top-level client package.
//   - ContainerSummary is github.com/moby/moby/api/types/container.Summary
//     (NOT client.ContainerSummary — the SDK reorganized result types into
//     api/types subpackages around the docker/docker → moby/moby rename).
//   - ContainerInspect is client.ContainerInspectResult — a wrapper struct
//     {Container container.InspectResponse, Raw json.RawMessage}. The
//     facade exposes the wrapper as-is so callers can choose between
//     typed access (.Container.*) and the raw JSON for forward-compat
//     fields the SDK hasn't typed yet.
//   - EventMessage is github.com/moby/moby/api/types/events.Message.
//   - Events returns EventsResult{Messages <-chan events.Message, Err <-chan error}
//     — already channel-shaped, so the mobyClient adapter unpacks it
//     directly rather than translating an iterator.
type (
	ContainerListOptions    = client.ContainerListOptions
	ContainerSummary        = container.Summary
	ContainerInspect        = client.ContainerInspectResult
	ContainerInspectOptions = client.ContainerInspectOptions
	EventsListOptions       = client.EventsListOptions
	EventMessage            = events.Message
	ImageInspect            = client.ImageInspectResult
	ImagePullOptions        = client.ImagePullOptions
	PingOptions             = client.PingOptions
	Filters                 = client.Filters
)

// Client is the narrow abstraction over github.com/moby/moby/client v0.4.1
// that the rest of hmi-update depends on. The method set is intentionally
// small: seven operations cover Phase 2 (Ping, ContainerList,
// ContainerInspect, Events), Plan quick-260515-mu0 BUG-1 (ImageInspect)
// and Phase 4 (ImagePull, ImageTag). Adding methods later requires
// coordinated edits to TestClient_InterfaceMethodCount in moby_test.go and
// to the doc comment on this type.
//
// Client is safe for concurrent use — the moby SDK contract is documented
// at github.com/moby/moby/client.Client (the underlying HTTP client is
// goroutine-safe).
type Client interface {
	// Ping verifies the docker daemon is reachable. Used by the /healthz
	// handler in internal/api (DOCK-03 / OBS-02). The SDK's PingResult
	// (API version, OS type, swarm status) is intentionally discarded —
	// hmi-update only cares about reachability.
	Ping(ctx context.Context) error

	// ContainerList returns the subset of containers matching opts.Filters.
	// Plan 02-03 calls this once at boot with the hmi-update.watch=true
	// label filter (DOCK-04). The SDK wraps results in a
	// ContainerListResult{Items []Summary} — the facade unwraps to a flat
	// slice so callers don't need to know about the wrapper.
	ContainerList(ctx context.Context, opts ContainerListOptions) ([]ContainerSummary, error)

	// ContainerInspect returns the full descriptor for one container id.
	// Plan 02-03 calls this after every docker event so labels and image
	// refs are authoritative (event payload alone is not enough — see
	// CONTEXT.md "Claude's Discretion": "lean toward inspect — payload
	// missing some fields we need like labels").
	ContainerInspect(ctx context.Context, id string) (ContainerInspect, error)

	// Events streams docker daemon events. The returned channels close
	// when ctx is cancelled or the daemon connection drops. Plan 02-03
	// owns the single consumer goroutine; reconnect on error is the
	// consumer's responsibility (exponential backoff per CONTEXT.md
	// <specifics>: 1s, 2s, 4s, up to 30s).
	//
	// SDK shape: client.Events returns EventsResult{Messages, Err}, which
	// is already channel-shaped — the facade just unpacks the fields.
	Events(ctx context.Context, opts EventsListOptions) (<-chan EventMessage, <-chan error)

	// ImagePull pulls an image. Reserved for Phase 4 (ACT-01). Lands as a
	// real method here so the interface is stable across the docker →
	// registry → actions buildout. The returned ReadCloser carries the
	// pull-progress JSON stream; callers can either Wait/ReadAll or
	// iterate JSONMessages (see SDK ImagePullResponse).
	ImagePull(ctx context.Context, ref string, opts ImagePullOptions) (io.ReadCloser, error)

	// ImageInspect resolves an image reference (image ID, name:tag, or digest)
	// against the local docker daemon and returns the SDK's image inspect
	// wrapper. discovery.upsertFromInspect calls this AFTER ContainerInspect
	// to resolve the registry manifest digest (ImageInspect.RepoDigests[0]) —
	// the value the Phase 3 poller's flip-rule compares against the upstream
	// registry digest. ContainerInspect.Image alone is insufficient (it is
	// the local content-addressable image ID, NOT the registry manifest
	// digest).
	//
	// SDK shape (verified against _sdk_shape.txt — moby/moby/client v0.4.1):
	// client.ImageInspect returns ImageInspectResult{image.InspectResponse}.
	// The facade exposes the wrapper as-is so callers reach for res.RepoDigests
	// and res.ID via the embedded InspectResponse.
	ImageInspect(ctx context.Context, ref string) (ImageInspect, error)

	// ImageTag retags a local image. Reserved for Phase 4 (ACT-03
	// rollback path: docker tag <image>@<previous_digest> <image>:<tag>).
	// The SDK call signature is ImageTag(ctx, ImageTagOptions{Source, Target})
	// — the facade flattens to (src, dst) so callers don't construct an
	// options struct for a two-argument operation.
	ImageTag(ctx context.Context, src, dst string) error
}
