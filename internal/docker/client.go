// Package docker wraps the moby/moby Docker daemon client used to enumerate
// watched containers, pull fresh images, and (Phase 9 (a)) recreate
// containers via the socket-only path.
//
// The interface grew incrementally with each phase: 8 methods through
// Phase 4, 13 after Phase 9 (a) added the recreate primitives
// (ContainerCreate, ContainerRemove, ContainerStart, ContainerStop,
// NetworkConnect). Any change to the method count requires a coordinated
// edit of the reflect-based guard in moby_test.go
// (TestClient_InterfaceMethodCount).
package docker

import (
	"context"
	"io"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/image"
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
	ImageListOptions        = client.ImageListOptions
	ImageSummary            = image.Summary
	ImagePullOptions        = client.ImagePullOptions
	PingOptions             = client.PingOptions
	Filters                 = client.Filters

	// Phase 9 (a) — socket-only recreate. Five option/result types
	// from the moby SDK consumed by internal/recreate. Verified
	// against `go doc github.com/moby/moby/client` 2026-05-16 — all
	// five live on the top-level `client` package (NOT
	// container.StopOptions as one might expect from older SDK
	// vintages; v0.4.1 collected the stop options on the client
	// package alongside ContainerStop itself).
	ContainerCreateOptions = client.ContainerCreateOptions
	ContainerCreateResult  = client.ContainerCreateResult
	ContainerRemoveOptions = client.ContainerRemoveOptions
	ContainerStartOptions  = client.ContainerStartOptions
	ContainerStopOptions   = client.ContainerStopOptions
	NetworkConnectOptions  = client.NetworkConnectOptions
)

// Client is the narrow abstraction over github.com/moby/moby/client v0.4.1
// that the rest of docker-update depends on. The method set covers:
//
//   - Phase 2 discovery + healthz: Ping, ContainerList, ContainerInspect, Events.
//   - quick-260515-mu0 BUG-1: ImageInspect.
//   - Phase 4 actions: ImagePull, ImageTag, ImageList.
//   - Phase 9 (a) socket-only recreate: ContainerCreate, ContainerRemove,
//     ContainerStart, ContainerStop, NetworkConnect.
//
// Total: 13 methods. Adding a 14th requires coordinated edits to
// TestClient_InterfaceMethodCount in moby_test.go and to the doc comment
// on this type.
//
// Client is safe for concurrent use — the moby SDK contract is documented
// at github.com/moby/moby/client.Client (the underlying HTTP client is
// goroutine-safe).
type Client interface {
	// Ping verifies the docker daemon is reachable. Used by the /healthz
	// handler in internal/api (DOCK-03 / OBS-02). The SDK's PingResult
	// (API version, OS type, swarm status) is intentionally discarded —
	// docker-update only cares about reachability.
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

	// ImageList returns the local docker daemon's image cache, optionally
	// filtered by reference. Added to support Rollback's fallback target
	// discovery (BUG-7c, post-2026-05-16): when state.PreviousDigest is
	// empty (state never recorded a target because docker-update was
	// restarted between Update and Rollback, or because the original
	// Update predated the BUG-7b fix), the orchestrator scans the local
	// daemon for previously-pulled-but-now-untagged images of the same
	// repo and uses the most recent one as the rollback target. This
	// lets the operator recover from "current container is broken, no
	// state.previous_digest recorded" without hand-editing state.json.
	//
	// SDK shape:
	//   client.ImageList(ctx, ImageListOptions{All, Filters, ...}) (ImageListResult, error)
	//
	// The facade unwraps the result to a flat []ImageSummary slice for
	// symmetry with ContainerList. Each ImageSummary carries .ID,
	// .Created (Unix timestamp), .RepoTags, .RepoDigests — the fields
	// the fallback heuristic needs.
	ImageList(ctx context.Context, opts ImageListOptions) ([]ImageSummary, error)

	// ----------------------------------------------------------------
	// Phase 9 (a) — socket-only recreate (5 methods).
	//
	// internal/recreate/recreate.Service composes these four daemon
	// calls into a Stop → Remove → Create → NetworkConnect (extras) →
	// Start sequence that replaces the deleted
	// compose.Runner.UpdateService subprocess. The first four are the
	// recreate primitives; NetworkConnect is needed for the
	// second-and-later networks on multi-network containers (Pitfall
	// 3 — create-then-connect-extras pattern per RESEARCH.md Pattern
	// 1).
	//
	// Method-count guard: TestClient_InterfaceMethodCount in
	// moby_test.go is pinned to 13 (= 8 prior methods + 5 new). Adding
	// a 14th method requires updating the guard in lockstep.
	// ----------------------------------------------------------------

	// ContainerCreate creates a new container. ContainerCreateOptions
	// carries Config / HostConfig / NetworkingConfig / Name; the
	// returned ContainerCreateResult.ID is the new container's daemon
	// id.
	ContainerCreate(ctx context.Context, opts ContainerCreateOptions) (ContainerCreateResult, error)

	// ContainerRemove removes a container. Used by recreate.Service
	// with Force=true so a stopped-but-still-attached container does
	// not block. RemoveVolumes=false because compose-managed volumes
	// belong to the compose project, not to the individual container
	// being recreated.
	ContainerRemove(ctx context.Context, id string, opts ContainerRemoveOptions) error

	// ContainerStart starts a created container. ContainerStartOptions
	// is typically the zero value for compose-recreate callers
	// (checkpoint fields are unused).
	ContainerStart(ctx context.Context, id string, opts ContainerStartOptions) error

	// ContainerStop sends SIGTERM with a grace timeout, then SIGKILL.
	// recreate.Service passes a 10s timeout (matches compose's default
	// stop_grace_period). The SDK contract treats nil Timeout as
	// "engine default" (also 10s).
	ContainerStop(ctx context.Context, id string, opts ContainerStopOptions) error

	// NetworkConnect attaches a container to an additional network.
	// Used by recreate.Service to wire up the second-and-later
	// networks after ContainerCreate (which only accepts the first
	// network in NetworkingConfig). networkID can be a network name
	// or ID per the SDK.
	NetworkConnect(ctx context.Context, networkID string, opts NetworkConnectOptions) error
}
