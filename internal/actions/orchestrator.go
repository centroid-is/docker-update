// Package actions sequences the Update / Rollback / Force-pull workflows that
// the web UI exposes as per-row buttons.
//
// Phase 1 ships the interface only; the body lands in phase 4 (ACTION-01..05).
package actions

// Orchestrator coordinates the multi-step action lifecycle:
//   - mark in-flight in state
//   - call compose.Runner
//   - update state on success/failure
//   - emit toast event
//
// Plan-04's internal/api server delegates POST /api/containers/{name}/update
// (and friends) to an Orchestrator.
//
// TODO(phase-4): implement — see .planning/phases/04-*/*.md (ACTION-01..05).
type Orchestrator interface{}
