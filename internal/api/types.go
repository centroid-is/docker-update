// Package api defines the HTTP wire types for docker-update.
//
// This file is the source of truth that tygo reads to regenerate
// ui/src/lib/types.d.ts (see tygo.yaml at repo root, and `make types`).
//
// The field tags here must mirror internal/state/Container so that what the
// store persists to ./docker_update_state.json deserializes cleanly into the
// same wire shape served by GET /api/state. Plan 04 will reconcile by
// re-using or aliasing one in terms of the other; field tags being
// identical is the load-bearing invariant.
//
// Do NOT import internal/state from this file — tygo's package read should
// be self-contained, and we want this file to remain editable without
// pulling in runtime concerns.
package api

import "time"

// Container is the on-the-wire representation of a watched Docker container.
//
// Field tags mirror internal/state/Container verbatim. `omitempty` on
// optional string fields prevents zero-value pollution of the JSON payload
// served to the UI.
//
// Phase 2 plan 02-01 adds ContainerID, Labels, Pinned, Stopped — see
// internal/state/schema.go for the field-by-field rationale. The tags
// here are byte-identical to state.Container; tygo regenerates the
// TypeScript Container interface from this file (tygo.yaml include_files
// limits the scan to types.go).
//
// Phase 3 plan 03-01 adds AvailableDigest, LastPolledAt, Notes mirroring
// state.Container. Time fields use `omitzero` (Go 1.24+) because
// encoding/json's `omitempty` does not recognize the time.Time struct
// zero value — without omitzero, an un-polled container would serialize
// "last_polled_at":"0001-01-01T00:00:00Z" and break the Phase 2
// forward-compat invariant. See state.Container.LastPolledAt godoc.
//
// Phase 4 plan 04-01 adds ActionInFlight, ActionError mirroring
// state.Container. Both are `omitempty` strings; tags byte-identical
// to state.Container; verified by TestPhase4Types_StateApiTagParity.
type Container struct {
	Service         string `json:"service"`
	Image           string `json:"image,omitempty"`
	Tag             string `json:"tag,omitempty"`
	CurrentDigest   string `json:"current_digest,omitempty"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`

	// ContainerID is the short 12-char docker container id (matches
	// `docker ps`). Discovery goroutine (plan 02-03) sets this.
	ContainerID string `json:"container_id,omitempty"`

	// Labels carries the hmi-update.* labels relevant to update/rollback
	// policy (watch, tag-pattern, allow-update, allow-rollback). Filtered
	// at the discovery layer.
	Labels map[string]string `json:"labels,omitempty"`

	// Pinned is true for digest-pinned image references (image: ...@sha256:...).
	// The Phase 5 UI renders these as "pinned: opt-out"; Phase 3's poller
	// skips them.
	Pinned bool `json:"pinned,omitempty"`

	// Stopped is true when the container's most recent docker event was
	// `die`. The Phase 5 UI shows a stopped-status badge; Phase 3's
	// poller skips these (no digest to compare).
	Stopped bool `json:"stopped,omitempty"`

	// AvailableDigest is the upstream sha256 most recently fetched by the
	// poll loop. See internal/state.Container.AvailableDigest for the
	// semantic rationale (DETECT-05/DETECT-07).
	AvailableDigest string `json:"available_digest,omitempty"`

	// CurrentDigestAt / AvailableDigestAt — build timestamps of the
	// running image and the upstream image respectively. Surfaced in the
	// UI so the operator sees "running built 30d ago, available built 1h
	// ago" without having to interpret raw sha256 hashes. See
	// state.Container.{CurrentDigestAt,AvailableDigestAt} for the field-
	// by-field rationale; `omitzero` for the same omitempty-vs-omitzero
	// reason as LastPolledAt below.
	CurrentDigestAt   time.Time `json:"current_digest_at,omitzero"`
	AvailableDigestAt time.Time `json:"available_digest_at,omitzero"`

	// PreviousDigestAt is the wall-clock time PreviousDigest was last
	// written by a real Update/Rollback swap. UI surfaces "last update
	// was 3h ago" next to the previous digest. omitzero so containers
	// that have never been updated through docker-update don't render
	// a placeholder date. Phase 9 P9-D fix.
	PreviousDigestAt time.Time `json:"previous_digest_at,omitzero"`

	// PreviousDigestBuiltAt is the image-build time of the PREVIOUS image
	// (the one PreviousDigest references). Symmetric to CurrentDigestAt +
	// AvailableDigestAt so the UI's three digest columns each have their
	// "sha date" — when the image was built. The wall-clock-of-swap time
	// is PreviousDigestAt above, surfaced separately as the "last change"
	// column. See state.Container.PreviousDigestBuiltAt.
	PreviousDigestBuiltAt time.Time `json:"previous_digest_built_at,omitzero"`

	// LastPolledAt is RFC3339Nano-encoded wall-clock time of the most
	// recent successful resolver.Digest call. See state.Container.
	// Tag is `omitzero` (not `omitempty`) — see file-level godoc.
	LastPolledAt time.Time `json:"last_polled_at,omitzero"`

	// Notes is a single short ops-readable sentence (pinned, invalid
	// pattern, etc.). See state.Container.Notes for the full set.
	Notes string `json:"notes,omitempty"`

	// ActionInFlight is the current in-flight per-row action (Phase 4).
	// See internal/state.Container.ActionInFlight for full semantics.
	ActionInFlight string `json:"action_in_flight,omitempty"`

	// ActionError is the last action's failure surface (Phase 4). See
	// internal/state.Container.ActionError for the full format.
	ActionError string `json:"action_error,omitempty"`
}

// State is the top-level wire schema served at GET /api/state.
//
// Version is the on-disk schema version (currently 1 — see brief §F4).
// Containers is keyed by compose service name.
//
// Phase 3 plan 03-01 adds LastPollStart, LastPollEnd, LastPollError —
// the poll-loop observability surface. See internal/state.State for the
// full semantics. Surfaced in /api/state for the Phase 5 UI's "last
// polled" indicator. Time fields use `omitzero` (NOT `omitempty`) so a
// pre-first-tick payload omits the keys cleanly.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`

	// LastPollStart / LastPollEnd / LastPollError — see internal/state.State
	// for full semantics. Surfaced in /api/state for the Phase 5 UI's
	// "last polled" indicator.
	LastPollStart time.Time `json:"last_poll_start,omitzero"`
	LastPollEnd   time.Time `json:"last_poll_end,omitzero"`
	LastPollError string    `json:"last_poll_error,omitempty"`
}
