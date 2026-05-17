// Package state owns the atomic on-disk JSON state store for docker-update.
//
// The store is a single JSON file at the path passed to NewStore (in
// production, ./docker_update_state.json bind-mounted alongside the compose
// stack — brief §C2 / STATE-01). All persistence in v1 lives here; there
// is no SQLite, Mongo, or Redis anywhere in this package or in internal/.
package state

import "time"

// SchemaVersion is the on-disk schema version. Bumps require a migration
// step (not yet implemented — Phase 1 ships version 1 only). The on-disk
// document carries the same integer in its "version" field; NewStore reads
// it back unchanged.
const SchemaVersion = 1

// Container is the on-disk + wire shape for a watched container.
//
// Field tags intentionally match the shape documented in
// .planning/phases/01-walking-skeleton-test-harness/01-RESEARCH.md
// §"tygo configuration" (lines 420-456) so that the tygo-generated
// TypeScript types in ui/src/lib/types.d.ts stay consistent with what the
// Go server actually writes to disk and serves on the wire. internal/api/
// types.go mirrors this shape verbatim; tygo's source-of-truth contract
// (Makefile `check-types`) catches drift.
//
// Phase 2 plan 02-01 adds ContainerID, Labels, Pinned, Stopped (per
// CONTEXT.md "Per-container enumeration fields"). The four new fields are
// set by the discovery goroutine in plan 02-03 (boot ContainerList +
// docker event handler). Every new field is omitempty so the 95% case
// (running, non-pinned container with no extra labels) does NOT clutter
// the wire payload with default false values.
//
// Phase 3 plan 03-01 adds AvailableDigest, LastPolledAt, Notes — the
// poll-loop observability surface. AvailableDigest is the upstream
// sha256 the resolver most recently fetched; LastPolledAt is the
// wall-clock time of that fetch; Notes is a short ops-readable sentence
// (pinned, invalid pattern, running-tag mismatch). String fields use
// `omitempty`; time.Time fields use Go 1.24+'s `omitzero` (encoding/json
// `omitempty` does not recognize struct zero values, which would
// otherwise leak "0001-01-01T00:00:00Z" into the wire payload for an
// un-polled container — breaking forward-compat with Phase 2 state
// files). Forward-compat verified by
// TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk in
// schema_phase3_test.go.
//
// Phase 4 plan 04-01 adds ActionInFlight, ActionError — the
// orchestrator-driven action lifecycle surface (CONTEXT.md Area 1
// "state.Container extensions"). Both are `omitempty` strings; the
// Notes precedent (single short string, not a structured object) is
// reused per CONTEXT.md Area 1 "Claude's Discretion". Forward-compat
// with Phase 3 on-disk state files is verified by
// TestPhase4SchemaFields_ForwardCompat_Phase3OnDisk in
// schema_phase4_test.go (T-04-01-01 mitigation).
type Container struct {
	Service         string `json:"service"`
	Image           string `json:"image,omitempty"`
	Tag             string `json:"tag,omitempty"`
	CurrentDigest   string `json:"current_digest,omitempty"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`

	// ContainerID is the short (12-char) docker container ID, matching the
	// `docker ps` column. Set by the discovery goroutine on boot
	// ContainerList + every `start` event Inspect. CONTEXT.md "Claude's
	// Discretion" picks short over full ID for parity with operator-visible
	// tooling.
	ContainerID string `json:"container_id,omitempty"`

	// Labels carries the subset of container labels relevant to docker-update —
	// `hmi-update.watch`, `hmi-update.tag-pattern`, `hmi-update.allow-update`,
	// `hmi-update.allow-rollback`. Filtered at the discovery layer (plan
	// 02-03) so the wire payload never echoes unrelated compose labels.
	// Empty/nil when no docker-update labels are set (omitempty skips the
	// field).
	Labels map[string]string `json:"labels,omitempty"`

	// Pinned is true when the container's image reference is digest-pinned
	// (e.g. `image: ghcr.io/foo/bar@sha256:...`). The Phase 3 poll loop
	// filters pinned containers out of the digest-fetch list; the Phase 5
	// UI renders "pinned: opt-out" in the row's status column
	// (DETECT-09 forecast).
	Pinned bool `json:"pinned,omitempty"`

	// Stopped is true when the most recent docker event for this container
	// was `die` (container exited). The row stays in state.Containers so
	// the UI can show a stopped-status badge; the Phase 3 poll loop skips
	// stopped containers (they have no digest to compare). Cleared on the
	// next `start` event.
	Stopped bool `json:"stopped,omitempty"`

	// Phase 3 plan 03-01: poll-loop observability — AvailableDigest,
	// LastPolledAt, Notes.

	// AvailableDigest is the upstream sha256 most recently fetched by the
	// Phase 3 poll loop. Empty until the first successful resolver.Digest()
	// call. Compared against CurrentDigest to compute UpdateAvailable.
	// Set by the poll consumer goroutine (internal/poll/channel.go); never
	// mutated outside state.Store.Update. omitempty so a not-yet-polled
	// row does not clutter the wire payload with "" (DETECT-05/DETECT-07).
	AvailableDigest string `json:"available_digest,omitempty"`

	// CurrentDigestAt is the build/creation timestamp of the IMAGE the
	// running container references (NOT the container's start time). Set
	// from ImageInspect.Created at the same site as CurrentDigest in
	// discovery.upsertFromInspect. Lets the UI surface "running image was
	// built 30d ago" — a more useful signal than the digest hash alone.
	//
	// Tag is `omitzero` (NOT `omitempty`) — encoding/json's omitempty does
	// not recognize struct zero values, so an unresolved time.Time would
	// otherwise serialize "0001-01-01T00:00:00Z" and break the Phase 2
	// forward-compat invariant. Same rationale as LastPolledAt above.
	CurrentDigestAt time.Time `json:"current_digest_at,omitzero"`

	// PreviousDigestAt is the wall-clock time at which PreviousDigest was
	// last written by an Update or Rollback action that actually swapped
	// the running digest. Lets the UI render "last update was 3h ago"
	// alongside the hash. Zero-valued (omitzero) for containers that have
	// never been updated through docker-update (the UI hides the date
	// chip in that case).
	//
	// Phase 9 P9-D fix (post-execute SC-7 smoke): the previous_digest hash
	// was rendered without a date, leaving operators unable to tell whether
	// a recorded rollback target was stale or fresh. Populated only when a
	// real swap occurs (oldDigest != newDigest); see actions/orchestrator.go
	// Step 9.5.
	PreviousDigestAt time.Time `json:"previous_digest_at,omitzero"`

	// PreviousDigestBuiltAt is the image-build time of the PREVIOUS image
	// (the one PreviousDigest points to) — NOT the wall-clock time of the
	// swap. It is the symmetric companion to CurrentDigestAt and
	// AvailableDigestAt: each digest column in the UI gets its sha date.
	// PreviousDigestAt remains the wall-clock-of-swap, surfaced separately
	// as the "last change" column.
	//
	// Populated at swap time by the orchestrator by carrying the OLD
	// snapshot.CurrentDigestAt forward into the new PreviousDigestBuiltAt
	// (the new previous IS the old current, so its build time is already
	// in hand — no extra ImageInspect needed). Empty / omitzero for state
	// files that pre-date the field, in which case the UI hides the date
	// chip under the rollback hash.
	PreviousDigestBuiltAt time.Time `json:"previous_digest_built_at,omitzero"`

	// AvailableDigestAt is the build/creation timestamp of the upstream
	// IMAGE the registry currently resolves to for image:tag. Set by the
	// poll consumer goroutine alongside AvailableDigest, sourced from the
	// registry image's ConfigFile().Created. Lets the UI surface
	// "available image was built 1h ago" so the operator sees what they
	// would update *to*.
	//
	// Tag is `omitzero` (same rationale as CurrentDigestAt + LastPolledAt).
	AvailableDigestAt time.Time `json:"available_digest_at,omitzero"`

	// LastPolledAt is the wall-clock time of the most recent successful
	// resolver.Digest() call for this container. Serialized as
	// time.RFC3339Nano (Go's default JSON encoding for time.Time).
	// Zero-valued (omitted) until first poll. Phase 3 sets this in the
	// poll-consumer goroutine; Phase 5 reads it for the per-row
	// "last polled X ago" tooltip (DETECT-05).
	//
	// Tag note: `omitzero` (NOT `omitempty`) — encoding/json's omitempty
	// does not recognize struct zero values, so an un-polled container
	// would otherwise serialize "last_polled_at":"0001-01-01T00:00:00Z".
	// Go 1.24+'s `omitzero` calls IsZero() on time.Time and omits the
	// key cleanly, preserving the forward-compat invariant
	// (TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk).
	LastPolledAt time.Time `json:"last_polled_at,omitzero"`

	// Notes is a single short ops-readable sentence. Phase 3 writes one
	// of: "pinned: opt-out" (DETECT-09), "invalid tag-pattern label,
	// ignored" (DETECT-08 fallthrough), "running tag does not match
	// tag-pattern label" (DETECT-08 operator-misconfig), or "no amd64
	// manifest in upstream index" (Phase-3-specific Pitfall). At most
	// one note applies at a time; if two would apply, join with "; "
	// (per CONTEXT.md Area 3 "Claude's Discretion" — single string,
	// not []string).
	Notes string `json:"notes,omitempty"`

	// Phase 4 plan 04-01: action lifecycle — ActionInFlight, ActionError.
	// Set by the actions orchestrator (Plan 04-03) via the existing
	// single-consumer channel (DETECT-10), never mutated outside
	// state.Store.Update. See CONTEXT.md Area 1 "state.Container
	// extensions" for the locked semantics.

	// ActionInFlight is the current in-flight per-row action (Phase 4).
	// Values: "" (idle), "updating", "rolling_back", "force_pulling".
	// Set by orchestrator via KindActionStart; cleared via KindActionResult.
	// UI Phase 5 reads this for per-row spinner state. omitempty so an idle
	// container does not clutter the wire payload with "".
	ActionInFlight string `json:"action_in_flight,omitempty"`

	// ActionError is the last action's failure surface (Phase 4). Empty when
	// the most recent action succeeded. Format: "<phase>_failed: <reason>"
	// e.g. "verify_failed: container restarted 3 times in 15s". Cleared on
	// the next successful action of any kind. Matches the Notes precedent
	// (single short string, not a structured object). UI Phase 5 reads this
	// for a toast.
	ActionError string `json:"action_error,omitempty"`
}

// State is the root document persisted to ./docker_update_state.json.
//
// Version is the on-disk schema version (currently SchemaVersion = 1).
// Containers is keyed by compose service name; that key is also duplicated
// inside each Container's Service field so that consumers iterating over
// either the map or a flattened slice see the same identifier.
//
// Phase 3 plan 03-01 adds the top-level poll-loop observability fields
// LastPollStart, LastPollEnd, LastPollError. All three are omitempty so
// pre-Phase-3 state files (Phase 2 shape — just version + containers)
// load cleanly with the new fields at zero values. Verified by
// TestPhase3SchemaFields_ForwardCompat_Phase2OnDisk.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`

	// Phase 3 plan 03-01: poll-loop observability — LastPollStart,
	// LastPollEnd, LastPollError. Driven by the poll-consumer goroutine
	// in internal/poll/channel.go.

	// LastPollStart is the wall-clock time the most recent cron tick's
	// sweep STARTED. Reset on every tick to time.Now(). The poll consumer
	// goroutine sets this on a KindPollSweepStart message. Tag is
	// `omitzero` (not `omitempty`) so a pre-first-tick state file does
	// NOT surface a misleading zero timestamp (DETECT-05 / OBS-04 audit
	// surface). See LastPolledAt above for the omitempty-vs-omitzero
	// rationale on time.Time.
	LastPollStart time.Time `json:"last_poll_start,omitzero"`

	// LastPollEnd is the wall-clock time the most recent cron tick's
	// sweep COMPLETED. Reset on every tick to time.Now() after all
	// errgroup workers return. e2e specs poll on this advancing past a
	// captured baseline as the "a poll happened" signal (OBS-04
	// redaction test uses this to know when to capture logs). `omitzero`
	// for the same reason as LastPollStart.
	LastPollEnd time.Time `json:"last_poll_end,omitzero"`

	// LastPollError is the last poll-level error surface (sweep-level
	// failure, not per-container — those go on Container.Notes).
	// Currently empty in v1: errgroup workers swallow per-container
	// errors and the sweep itself does not fail. Reserved for future
	// use (e.g. cron expression dynamic update failure, channel
	// back-pressure detection). Lean string over structured object per
	// CONTEXT.md Area 4 "Claude's Discretion".
	LastPollError string `json:"last_poll_error,omitempty"`
}
