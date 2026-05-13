// Package docker wraps the moby/moby Docker daemon client used to enumerate
// watched containers and pull fresh images.
//
// Phase 1 ships the interface only; the body lands in phase 2 (DOCK-01..04).
package docker

// Client is the abstraction over moby/moby that plan-04's internal/api
// server depends on. Phase 2 implements it against
// `github.com/moby/moby/client`.
//
// TODO(phase-2): implement — see .planning/phases/02-*/*.md (DOCK-01..04).
type Client interface{}
