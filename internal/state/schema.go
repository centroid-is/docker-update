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
// Go server actually writes to disk and serves on the wire. Plan 01-03
// owns internal/api/types.go and will mirror this shape verbatim there.
type Container struct {
	Service         string `json:"service"`
	Image           string `json:"image,omitempty"`
	Tag             string `json:"tag,omitempty"`
	CurrentDigest   string `json:"current_digest,omitempty"`
	PreviousDigest  string `json:"previous_digest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
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
