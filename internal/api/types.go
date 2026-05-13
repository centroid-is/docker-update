// Package api defines the HTTP wire types for hmi-update.
//
// This file is the source of truth that tygo reads to regenerate
// ui/src/lib/types.d.ts (see tygo.yaml at repo root, and `make types`).
//
// The field tags here must mirror internal/state/Container so that what the
// store persists to ./hmi_update_state.json deserializes cleanly into the
// same wire shape served by GET /api/state. Plan 04 will reconcile by
// re-using or aliasing one in terms of the other; field tags being
// identical is the load-bearing invariant.
//
// Do NOT import internal/state from this file — tygo's package read should
// be self-contained, and we want this file to remain editable without
// pulling in runtime concerns.
package api

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
}

// State is the top-level wire schema served at GET /api/state.
//
// Version is the on-disk schema version (currently 1 — see brief §F4).
// Containers is keyed by compose service name.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`
}
