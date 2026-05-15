# Phase 6: Display-Blackout UX Checkpoint (flutter/weston) - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning
**Mode:** Decision-checkpoint phase (autonomous — recommended option locked)

<domain>
## Phase Boundary

A small, documentation-led product-decision phase. With the real Svelte 5 UI from Phase 5 in front of the team — including its pre-action "display may flicker" warning toast (UI-08, per Pitfall 5) — make the explicit decision about how to surface the 5-30s display blackout that recreating `flutter` / `weston` causes, and ship operator-facing documentation that matches.

Concretely this phase fills:

1. **PROJECT.md Key Decisions entry for UX-01** — the explicit choice between (a) "leave Update as-is + README warning + Phase-5 pre-action toast", (b) "two-step prepare/switch UX", or (c) "per-service danger flag with double-confirm".
2. **README.md** (the operator-facing repo README; this phase **creates** it if absent) — "Before you click Update on flutter or weston" callout section that names the 5-30s blackout window, points at the Rollback button, and explains the Phase-5 pre-action toast confirmation.
3. **Manual smoke note** — a one-line entry confirming the pre-action toast actually appears on a `weston-stub` container, executed against the real e2e compose stack.

This phase is decision + documentation only under the recommended option. **No production code changes** ship in Phase 6 under option (a) — Phase 5 already shipped UI-08 (the pre-action toast), and the substring detection on service name (`flutter` / `weston`) lives in the UI layer.

Out of scope for this phase: any state-schema additions (`prepared_digest` is gated on option (b)); third per-row UI button (option (b)); per-service danger-flag label (option (c)); container labels; new HTTP endpoints; deployment packaging (Phase 7); CI/CD (Phase 8).

</domain>

<decisions>
## Implementation Decisions

### LOCKED: UX-01 chooses option (a) — README warning + Phase-5 pre-action toast

**The decision is locked at planning time** (rationale verified against the brief's Core Value, Phase 5 deliverables, and the Pitfall 5 mitigation surface). Plan execution writes this verbatim into PROJECT.md Key Decisions.

**Option chosen:** **(a)** — leave Update as-is + README warning, augmented by Phase 5's pre-action "display may flicker" confirmation toast (UI-08).

**Options rejected:**

- **(b) Two-step prepare/switch UX** — would require: a new `prepared_digest` state schema field (Phase 1 atomic-write contract carries it; tygo regen; UI Phase 5 already shipped); a third per-row button ("Prepare" / "Switch now"); a corresponding new HTTP endpoint (`POST /api/containers/{service}/prepare`); a Stage 1 → Stage 2 Playwright e2e spec; an operator-facing two-action flow that doubles the per-row surface area in the table. This is **significant scope for marginal UX gain** — Phase 5's pre-action toast already gives the operator the "are you ready for the blackout?" gate, and Rollback is the safety net for "I clicked Update and got a blackout I didn't want." The brief's Core Value is "one button per container" — option (b) explicitly violates the one-click semantic for two services.

- **(c) Per-service danger-flag label with double-confirm** — would require: a new container label (`hmi-update.danger=true` or similar); operator-side label-application discipline (operators must remember to set the label when adding a new `flutter`-style service); UI double-confirm modal (new component, new e2e spec). Adds a **configuration knob operators must remember to set**, whereas the substring detection on service name (`flutter` / `weston`) is more robust by default — Phase 5 already does this without any label.

**Rationale (1 paragraph, to be appended verbatim to PROJECT.md Key Decisions):**

> **UX-01 chose option (a) — README warning + Phase-5 pre-action toast.** Phase 5 already ships a "display may flicker" confirmation toast for `flutter` / `weston` services (UI-08, Pitfall 5 mitigation). The operator is explicitly confirmed before the recreate begins; Rollback is the safety net if the blackout was unwanted. Options (b) and (c) double the surface area — option (b) adds a new state schema field, a new HTTP endpoint, a third per-row button, and a Stage 1 / Stage 2 spec; option (c) adds a per-service label operators must remember to set. Option (a) preserves the brief's "one button per container" semantic, ships **zero production code changes in Phase 6**, and is consistent with the project's no-extra-deps ethos. Phase 6 deliverables under option (a) are **documentation only**: the PROJECT.md Key Decisions entry recording this choice, plus a README.md "Before you click Update on flutter or weston" operator callout.

### Phase 6 deliverables (under option (a))

1. **PROJECT.md Key Decisions** — append the rationale paragraph above as a new row in the Key Decisions table (and a referencing "UX-01" anchor inline).
2. **README.md** — this phase **creates** the operator-facing README at the repo root (`/Users/jonb/Projects/tmp/README.md`). The file does not exist today. The README contains:
   - One-paragraph "What this is" intro (paraphrase PROJECT.md).
   - Quick start: `docker compose up -d hmi-update` + the `id -g docker` install step (DEPLOY-08 / Pitfall 9 preview; Phase 7 expands).
   - **"Before you click Update on flutter or weston" section** — the load-bearing UX-02 deliverable. Names the 5-30s blackout window (Pitfall 5 lifted directly), points at Rollback, explains the Phase-5 pre-action toast.
   - Pointers: link to PROJECT.md for full requirements; link to API.md for endpoint shapes; link to `.planning/` for full design context.
3. **`e2e/tests/weston-warning.spec.ts`** — RED-first Playwright e2e (reuses Phase 5's `concurrent-actions` / `safety-labels` spec scaffolding). Asserts that clicking Update on a `weston-stub` container surfaces the pre-action warning toast **before** the recreate begins; the operator must confirm; the toast text mentions "display" and "flicker". Note: under option (a) the toast itself is shipped by Phase 5 — this spec exists to **verify the toast actually fires for the weston substring**, closing the UX-02 → UI-08 trace.
4. **Manual smoke note** — a one-line entry in the SUMMARY confirming the toast appears in a real browser session against the e2e compose stack.

### Service-name substring detection (reused from Phase 5)

The Phase 5 UI layer detects `flutter` and `weston` via a case-insensitive substring match on the compose service name. This phase **does not add a configurable label or env var** for the trigger list — substring match on the two well-known service names is the contract, documented in the README and in PROJECT.md Container labels reference. If a future HMI adds a different display-drawing service (e.g., `kiosk`, `signage`), the operator can either rename it to include `flutter` or `weston`, or open a follow-up to extend the substring list. Documented as a `<deferred>` item below.

### What "documentation only" means in C4 terms

Per CLAUDE.md C4 ("TDD: verify → implement → verify → implement"), every functional requirement starts as a failing Playwright test. UX-01 / UX-02 are *documentation* requirements; UX-03 is gated on option (b) and is **not shipped** in this phase. The single Playwright spec (`weston-warning.spec.ts`) provides the verify→green loop for UX-02, ensuring the README callout's described UX behavior is empirically verified. UX-01 itself is a decision-record requirement and is verified by `grep -F` against PROJECT.md.

### File layout

- `PROJECT.md` — Key Decisions table extended with one new row (UX-01 → option (a)).
- `README.md` (NEW) — operator-facing readme; created at repo root.
- `e2e/tests/weston-warning.spec.ts` (NEW) — Playwright spec verifying the pre-action toast fires for `weston-stub`.
- `e2e/compose.test.yml` — extended (if not already from Phase 5) with a `weston-stub` service carrying `hmi-update.watch=true` and a service name containing `weston`. Phase 5's `concurrent-actions.spec.ts` likely already added this; if so, this phase only references; if not, it adds the service block.
- `.planning/phases/06-display-blackout-ux-checkpoint/06-01-PLAN.md` — the single plan.
- `.planning/phases/06-display-blackout-ux-checkpoint/06-01-SUMMARY.md` — produced at plan-completion.

### Acceptance — what "Phase 6 done" looks like

- `grep -F 'UX-01' PROJECT.md` matches the Key Decisions row.
- `test -f README.md` is true and `grep -F 'Before you click Update on flutter' README.md` matches the callout section.
- `e2e/tests/weston-warning.spec.ts` exists and is green in CI against the e2e compose stack.
- Manual smoke: open the UI in a browser against the e2e stack, click Update on `weston-stub`, observe the toast, click Cancel, observe no recreate happened.

### Claude's Discretion

- README.md tone — operator-facing, not contributor-facing. Brief, clear, no marketing. The "Before you click Update on flutter or weston" section is the load-bearing content; everything else is a pointer.
- Whether the README links the full Pitfall 5 text from `.planning/research/PITFALLS.md` or paraphrases it inline. Lean **paraphrase + link** — operators read READMEs; researchers read PITFALLS.md.
- The exact wording of the toast verification — the spec asserts the toast text contains both "display" and "flicker" (case-insensitive). Phase 5's UI-08 owns the exact copy; this phase only checks the load-bearing keywords. If Phase 5 ships different copy, the spec defers to Phase 5's wording (the spec gets updated; the UX decision does not change).
- Whether to include a "Force Pull" mention in the README's weston callout. Lean **yes, briefly** — force-pull is the recovery path if the new image cold-pull caused the blackout duration to surprise the operator; Phase 5 ships the Force Pull button.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets

- **Phase 5 pre-action toast (UI-08)** — the entire UX-02 surface depends on Phase 5 having shipped the "display may flicker" confirmation toast for `flutter` / `weston` substring matches. Phase 6 verifies; Phase 5 implements.
- **`e2e/tests/concurrent-actions.spec.ts` (Phase 5)** — the closest cousin spec; the `weston-warning.spec.ts` here mirrors its structure (Playwright fixtures, dialog/toast wait helpers).
- **`e2e/compose.test.yml`** — Phase 5 likely adds a `weston-stub` service block when shipping UI-08; if not, this phase adds it.
- **`internal/api/types.go` / `state.Container.Labels`** — already exposes the service name via the `Service` field; no schema additions needed for option (a).
- **PROJECT.md Key Decisions table** — Phase 4 extended this with multiple rows; the pattern is well-established. Phase 6 appends one row.

### Established Patterns

- **RED-first Playwright e2e** (Phase 1 onward) — `weston-warning.spec.ts` is written RED-first; toast verification drives green.
- **Documentation deliverables in commit alongside code** (Phase 4 PROJECT.md additions in plan 04-05) — Phase 6 follows the same pattern: docs land in the same plan as the spec.
- **Substring detection over configurable labels** for well-known service names — established in Phase 5 UI-08; Phase 6 documents it.
- **Single-paragraph rationale in Key Decisions** — Phase 4 set the precedent; Phase 6 follows.

### Integration Points

- Phase 5 → Phase 6: UI-08 implementation in Phase 5 is the load-bearing dependency for UX-02. If Phase 5 shipped UI-08 with substring detection on `flutter` / `weston` (per the Phase 5 CONTEXT plan), Phase 6 is purely doc + verify. If Phase 5 shipped UI-08 with a different trigger mechanism (e.g., a label), Phase 6's README copy and spec assertion adapt.
- Phase 6 → Phase 7: the README this phase creates is the **same README** Phase 7 extends with the full install runbook (`id -g docker`, compose deployment block, host-docker-GID step). Phase 6 establishes the file; Phase 7 extends it.
- Phase 6 → Pitfall 5: the README callout is the operator-visible surface of Pitfall 5's mitigation. The pitfall research stays in `.planning/research/PITFALLS.md`; the README is the operator's view.

</code_context>

<specifics>
## Specific Ideas

- **"Documentation only" is a feature, not a shortcut.** Option (a) is recommended explicitly *because* Phase 5 already paid the UX cost via UI-08. Phase 6 honoring "no code changes" is the integrity of the recommended decision — adding scope here would undermine the rationale for picking (a) over (b) / (c).
- **The README is operator-facing, not contributor-facing.** Contributors read `.planning/` and `CLAUDE.md`. Operators read the README. Keep it short, name the failure modes (blackout, rollback, force-pull), point at the buttons.
- **`weston-warning.spec.ts` is a Phase 5 → Phase 6 contract test.** It pins the pre-action toast UX behavior in CI so a future Phase 5 refactor cannot silently regress UI-08 without UX-02 going red. The spec is small (one happy path + one cancel path) but load-bearing for the decision-locking trace.
- **The PROJECT.md Key Decisions entry is the load-bearing artifact for UX-01.** A reviewer reading PROJECT.md in six months should see exactly *why* the team picked (a) over (b) / (c), and what the rejected options would have required. The 1-paragraph rationale above is the canonical text; the plan copies it verbatim.
- **The pre-action toast text is owned by Phase 5, not Phase 6.** The spec asserts only the load-bearing keywords ("display", "flicker") so a Phase 5 copy refinement does not break this phase's verification. The README paraphrases without quoting verbatim.
- **No state schema changes.** Option (a) explicitly does not require `prepared_digest`. If a future phase changes the UX-01 decision to (b), it must be tracked as a separate phase (insertion via `/gsd-phase`); Phase 6 closes UX-01 as decided.

</specifics>

<deferred>
## Deferred Ideas

- **Extending the substring trigger list beyond `flutter` / `weston`** — if a future HMI adds a `kiosk`, `signage`, or other display-drawing service, the pre-action toast won't fire. Two future paths: (a) extend the substring match list in the UI (Phase 5 follow-up); (b) introduce a `hmi-update.display-drawing=true` label that the UI checks alongside the substring match. **Defer to v2 unless a real HMI ships with such a service.**
- **Option (b) two-step prepare/switch UX as a v2 feature** — if operator feedback after v1 release indicates the pre-action toast is insufficient (e.g., "I want to pre-pull on Tuesday and switch on Friday during a maintenance window"), the two-step UX becomes a clear v2 candidate. The schema field (`prepared_digest`) is a small surface; the UI button + endpoint are the bulk of the work. Tracked as a follow-up.
- **Option (c) per-service danger flag** — if an HMI ever runs a non-`flutter`/`weston` display-drawing service that operators want gated, this becomes attractive. Schema: a new label `hmi-update.danger=true`; UI: double-confirm modal. Tracked.
- **Blackout duration logging in slog (Pitfall 5 instrumentation)** — Phase 4 shipped OBS-01 (every action emits before/after digests, exit code, duration). The current implementation logs the action duration including the verify-after-recreate window but does NOT separately log the operator-visible blackout window (stop-old → first-frame-on-new). A future instrumentation task could measure this empirically. Tracked but defer; the README warning is the v1 mitigation.
- **`flutter` and `weston` running together — recreating `weston` blackouts every Wayland client** — the README callout should note this; the existing Pitfall 5 research has the detail. The phase explicitly does not gate recreate-order or auto-detect dependent services.

</deferred>
</content>
