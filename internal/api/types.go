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
type Container struct {
	Service         string `json:"service"`
	Image           string `json:"image,omitempty"`
	Tag             string `json:"tag,omitempty"`
	CurrentDigest   string `json:"current_digest,omitempty"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
}

// State is the top-level wire schema served at GET /api/state.
//
// Version is the on-disk schema version (currently 1 — see brief §F4).
// Containers is keyed by compose service name.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`
}
