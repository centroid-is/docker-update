// Package state (continued). notes.go owns the canonical Note string
// literals that the Phase 5 UI reads from /api/state's per-container
// `notes` field.
//
// Why these literals live here (WR-10):
//
//   Pre-WR-10 the four canonical Notes were duplicated across
//   internal/poll/poller.go (notePinnedOptOut, noteTagMismatch,
//   noteRegistryPrefix + noteRegistrySuffix, noteInvalidTagPatternMirror)
//   and internal/docker/discovery.go (noteInvalidTagPattern). The
//   docker package cannot import poll directly (poll imports state +
//   docker would create a cycle if it imported poll), so the
//   internal/docker noteInvalidTagPattern literal was a hand-mirrored
//   COPY of internal/poll noteInvalidTagPatternMirror. A compile-time
//   agreement check was impossible — a one-character typo in one place
//   would silently desync the wire payload that the Phase 5 UI greps.
//
//   Promoting to a shared `internal/state` package gives both producers
//   a single source of truth. Both packages already import
//   internal/state (state.Container, state.State, state.Store), so no
//   new dependency direction is introduced. A future Phase-4 producer
//   (actions package) that needs to surface a Note can reference the
//   same const without re-mirroring.
//
// Style invariant: each literal still appears at exactly ONE quoted
// assignment site (right here) — the source-grep acceptance criteria
// in CONTEXT.md Area 3 remain robust against doc-comment references.
package state

// Note* are the canonical short ops-readable strings that surface on
// state.Container.Notes for the Phase 5 UI. They reflect static or
// transient container properties that the operator should see at a
// glance:
//
//   - NotePinnedOptOut  — DETECT-09: container has an @sha256: image pin
//   - NoteTagMismatch   — DETECT-08: running tag fails the pattern regex
//   - NoteInvalidTagPattern — DETECT-08 fallthrough: tag-pattern label
//     failed to compile as a regex
//   - NoteRegistryPrefix + class + NoteRegistrySuffix — fetch error
//     classification (class is "permanent" or "transient")
//
// CONTEXT.md Area 3 specifies these strings verbatim; do NOT edit
// without bumping the Phase 5 UI's note-string matcher in lockstep.
const (
	NotePinnedOptOut      = "pinned: opt-out"
	NoteTagMismatch       = "running tag does not match tag-pattern label"
	NoteInvalidTagPattern = "invalid tag-pattern label, ignored"
	NoteRegistryPrefix    = "registry error: "
	NoteRegistrySuffix    = " (check image ref)"
)
