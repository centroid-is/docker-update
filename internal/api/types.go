// Package api defines the HTTP wire types for hmi-update.
//
// Phase 1 ships STUB types only — `Container` is an empty struct and `State`
// carries just the schema version + container map. Plan 03 (Wave 3) finalizes
// `Container` with full json-tagged fields (service, image, tag, digests,
// status, etc.) and feeds the result through tygo to regenerate
// `ui/src/lib/types.d.ts`.
//
// These stubs exist now so that:
//   1. `tygo generate` has a package to read (FOUND-08 fail-on-diff check).
//   2. `internal/api` compiles in plan 04 even before plan 03 expands the
//      Container fields.
package api

// Container is the on-the-wire representation of a watched Docker container.
// Phase 1 ships an empty body; plan 03 expands per RESEARCH.md §"tygo
// configuration" verbatim Go example.
type Container struct{}

// State is the top-level wire schema served at GET /api/state.
//
// The Version field is the on-disk schema version (currently 1 — see brief §F4).
// Containers is keyed by compose service name.
type State struct {
	Version    int                  `json:"version"`
	Containers map[string]Container `json:"containers"`
}
