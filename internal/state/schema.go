// Package state owns the atomic on-disk JSON state store for hmi-update.
//
// The store is a single JSON file at the path passed to NewStore (in
// production, ./hmi_update_state.json bind-mounted alongside the compose
// stack — brief §C2 / STATE-01). All persistence in v1 lives here; there
// is no SQLite, Mongo, or Redis anywhere in this package or in internal/.
package state

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

	// Labels carries the subset of container labels relevant to hmi-update —
	// `hmi-update.watch`, `hmi-update.tag-pattern`, `hmi-update.allow-update`,
	// `hmi-update.allow-rollback`. Filtered at the discovery layer (plan
	// 02-03) so the wire payload never echoes unrelated compose labels.
	// Empty/nil when no hmi-update labels are set (omitempty skips the
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
}

// State is the root document persisted to ./hmi_update_state.json.
//
// Version is the on-disk schema version (currently SchemaVersion = 1).
// Containers is keyed by compose service name; that key is also duplicated
// inside each Container's Service field so that consumers iterating over
// either the map or a flattened slice see the same identifier.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`
}
