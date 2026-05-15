# Phase 6: Display-Blackout UX Checkpoint - Research

**Researched:** 2026-05-15
**Confidence:** HIGH on the decision rationale (corroborated by Pitfall 5 research, Phase 5 deliverables, brief Core Value). LOW research surface — this is a documentation-led decision-checkpoint phase; the load-bearing work was done in `.planning/research/PITFALLS.md` Pitfall 5 (2026-05-13) and the Phase 5 UI-08 spec.

This file is intentionally brief. Phase 6 is a product-decision + documentation phase. The recommended decision (option (a)) is locked in 06-CONTEXT.md; this file captures the reasoning-trace and the few external references that informed the decision.

---

## Decision-Locking Reasoning Trace

### The three options the brief enumerates

Per REQUIREMENTS.md UX-01, the explicit choices are:

- **(a) Leave Update as-is + README warning.** Operator clicks Update, the existing F2 flow runs, the display blacks out for 5-30s. README documents the expected behavior.
- **(b) Two-step prepare/switch UX.** Operator clicks "Prepare" (pre-pull + extract, no recreate), then later clicks "Switch now" (recreate from the prepared image). State schema gains `prepared_digest`; UI gains a third per-row button; new HTTP endpoint.
- **(c) Per-service danger flag with double-confirm.** Operator labels the container `hmi-update.danger=true`; UI shows a double-confirm modal before recreate. New label, new UI component, no schema change.

### What Phase 5 has already shipped (under option (a)'s assumption)

Per 05-CONTEXT.md "Per-action UX" (and UI-08), Phase 5 ships a **pre-action confirmation toast** for service names matching `flutter` or `weston` (case-insensitive substring). The toast warns "display may flicker" before triggering recreate. The operator can cancel.

This is a partial implementation of what option (c) would deliver — the "are you sure?" gate — without the per-service label. The substring match is more robust by default than a configurable label, because it does not require operator discipline.

### Why (a) wins the cost/benefit

| Aspect | Option (a) | Option (b) | Option (c) |
|--------|------------|------------|------------|
| **State schema changes** | None | New `prepared_digest` field; tygo regen; migration concerns | None |
| **New HTTP endpoints** | None | `POST /api/containers/{svc}/prepare` | None |
| **New UI components** | None (Phase 5 toast suffices) | Third per-row button; Stage 1/2 visual state | Double-confirm modal |
| **New Playwright specs** | One verification spec | Stage 1 → Stage 2 + cancel paths | Double-confirm + cancel paths |
| **Operator discipline required** | Zero | Zero (button choice is the discipline) | Per-service label discipline |
| **Preserves "one button per container"** | Yes | No (two buttons per service for flutter/weston) | Yes |
| **Mitigates Pitfall 5 (display blackout)** | Toast warning + Rollback safety net | Pre-pull eliminates pull-time blackout fraction (~50%); recreate-time blackout remains | Toast + double-confirm; same mitigation strength as (a) |
| **Scope for Phase 6** | Documentation only | Schema + endpoint + UI + spec + docs | Label + UI + spec + docs |

The brief's Core Value — **"one button per container"** — is directly threatened by option (b). Option (c) is a near-tie with (a) on mitigation strength but adds a configuration knob operators must remember to set. Option (a) is the **lowest-cost option that preserves the Core Value** while shipping a real UX mitigation (the Phase 5 toast).

### Why Rollback is the load-bearing safety net

The brief's Core Value full text: "A Centroid field engineer can confidently pull a fresh image to an HMI **and** roll it back to the previous digest, from one button per container in a browser." Rollback exists precisely so an operator who clicks Update and observes an unwanted blackout can immediately revert. Option (a) leans on this: the toast warns; if the operator proceeds and is surprised, Rollback is one click away. The single-slot rollback (Phase 4 design) is sufficient for this toggle-recover workflow.

### Why option (b) is the right v2 trigger

If operator feedback after v1 release indicates "the toast is not enough — I want to pre-pull during off-hours and switch during a planned maintenance window," option (b) becomes a clear v2 candidate. The schema field is small; the UI is one button. Tracked in 06-CONTEXT.md `<deferred>`.

---

## External References

The bulk of the technical research for Pitfall 5 (display blackout during recreate of Wayland clients) lives in `.planning/research/PITFALLS.md` Pitfall 5 (lines 126-147). The relevant findings:

- The operator-visible window between stop-old and run-new is dominated by image extraction (cold-pull) and application cold-start (Flutter renderer first paint), NOT by Docker itself.
- Recreating `weston` is worse than recreating `flutter` — it tears down the compositor, taking every Wayland client with it.
- Pre-pull + extract (option (b)) eliminates the image-extraction fraction of the blackout but does not eliminate the application cold-start fraction (typically 2-10s for Flutter on the HMI hardware).
- No direct community precedent exists for "container manager UX that handles Wayland client lifecycle gracefully" — derived from Weston/Wayland lifecycle docs.

Therefore the research surface for Phase 6 is the *product decision* (recorded above) rather than new technical investigation.

### Sources (carried forward from Pitfall 5 research)

- `.planning/research/PITFALLS.md` Pitfall 5 (researched 2026-05-13) — full failure-mode analysis.
- [Watchtower precedent](https://containrrr.dev/watchtower/) — the closest-comparable tool also leaves display-related UX entirely to the operator (no warnings, no danger flags).
- [Weston compositor lifecycle](https://wayland.freedesktop.org/docs/html/) — confirms compositor restart cascades all Wayland clients.
- Brief §F6 (UI/UX requirements) — UI-08 pre-action toast is the brief-mandated UX surface.

---

## What's Left to Verify

- That Phase 5 actually shipped UI-08 with substring detection on `flutter` / `weston` (case-insensitive). **Reviewable** at Phase 5 plan-completion. If Phase 5 lands a different trigger mechanism (e.g., a label), the README copy and spec adapt; the decision (option (a)) stands.
- That `weston-stub` exists in `e2e/compose.test.yml` with `hmi-update.watch=true` and the service name contains `weston`. **Reviewable** before Phase 6 execution. If absent, Plan 06-01 adds the service block.

---

*Phase 6 RESEARCH intentionally brief — heavy lifting in PITFALLS.md and Phase 5 CONTEXT.*
*Researched: 2026-05-15*
