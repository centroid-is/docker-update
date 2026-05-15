---
phase: 05-web-ui-completeness
plan: 02
subsystem: ui

tags: [svelte-5, runes, tailwind-v4, solaris-palette, heroicons, accessibility, color-mix]

# Dependency graph
requires:
  - phase: 05-web-ui-completeness
    plan: 01
    provides: "16 Solaris palette tokens + 13 semantic aliases in ui/src/app.css; reduced-motion baseline with spinner exception"
  - phase: 01-walking-skeleton
    provides: "ui/src/lib/Table.svelte scaffold (7-column thead + empty-state row); ui/src/lib/types.d.ts (tygo-generated Container + State); ui/src/App.svelte fetch('/api/state') shell; Vite 7 + @sveltejs/vite-plugin-svelte build"
  - phase: 04-action-execution
    provides: "Container.action_in_flight + Container.action_error wire fields (consumed by Row.svelte status $derived)"

provides:
  - "StatusBadge.svelte: 8-kind status pill (current, update_available, updating, rolling_back, force_pulling, action_error, pinned, stopped) with Solaris color-mix bg/border"
  - "ActionButton.svelte: 28x28 icon-only action button (update / rollback / force-pull Heroicons) with busy spinner + disabled state"
  - "CopyButton.svelte: 20x20 clipboard-write button with cyan check / red x feedback for 1500ms"
  - "Row.svelte: single-tr container row with $derived status + label-gated action buttons + 3 CopyButtons + pinned opt-out lock"
  - "Table.svelte: REPLACES Phase 1 scaffold; preserves <thead> + empty-state row verbatim; maps Container[] to Row instances"
  - "Header.svelte: sticky 64px header with Refresh / Watch now buttons + 1s-ticking last-poll relative timestamp"
  - "relative-time.ts: pure relativeTime(iso, now) formatter — never | Xs ago | Xm Ys ago | Xh Ym ago"
affects:
  - "05-03 (Toast + WarningModal mount in App.svelte alongside Table)"
  - "05-04 (App.svelte rewrites the transitional shell with full polling + busyServices + onAction wiring)"
  - "05-05 (Playwright e2e — ui-table.spec.ts asserts the preserved 7-column <thead> + empty-state wording; ui-actions.spec.ts asserts the lock-icon-vs-button branches in Row.svelte)"

# Tech tracking
tech-stack:
  added: []  # No new npm dependencies — Heroicons copy-pasted as inline SVG, no @heroicons/svelte etc.
  patterns:
    - "Svelte 5 runes (no stores): $state, $derived, $derived.by, $effect, $props — extends Phase 1 precedent (ui/src/App.svelte already used $state, ui/src/lib/Table.svelte already used $props)"
    - "Solaris color tokens consumed via inline style: directives (style:color, style:background) for color-mix(in srgb, var(...) X%, transparent) recipes that Tailwind v4 arbitrary-value classes would require verbose syntax for"
    - "Heroicons (outline, MIT) inline SVG with stroke=currentColor; no @heroicons/svelte dep (no-extra-deps ethos)"
    - "data-spinner attribute hook on every spinning SVG — picks up the app.css prefers-reduced-motion exemption registered in Plan 05-01"
    - "Pure-function helper (relative-time.ts) lives outside Svelte components; testable in Vitest later without a clock fake"
    - "Component-local <style> blocks for hover/active/disabled/focus-visible recipes — colocates the 4-state CSS with the markup; avoids polluting app.css with one-off selectors"
    - "@keyframes spin declared component-locally (StatusBadge + ActionButton) — keeps StatusBadge self-contained without depending on a global @keyframes that may or may not exist"
    - "App.svelte transitional shell pattern: when a downstream plan (05-04) will replace a parent file, the current plan ships inert no-op callbacks + empty Sets so the build stays runnable + manually smokable without preempting the downstream rewrite"

key-files:
  created:
    - "ui/src/lib/StatusBadge.svelte"
    - "ui/src/lib/ActionButton.svelte"
    - "ui/src/lib/CopyButton.svelte"
    - "ui/src/lib/Row.svelte"
    - "ui/src/lib/Header.svelte"
    - "ui/src/lib/relative-time.ts"
  modified:
    - "ui/src/lib/Table.svelte"
    - "ui/src/App.svelte"

key-decisions:
  - "update_available pill uses --color-pending (yellow), NOT --color-warning (orange) — locks the Plan 05-01 open-note for 05-02; orange stays reserved for the flutter/weston flicker warning toast/modal (Plan 05-03)"
  - "Container.action_in_flight cast directly to StatusKind in Row.svelte without re-validation — server emits one of the 3 in-flight values ('updating' | 'rolling_back' | 'force_pulling'); UI trusts the wire-shape contract; defensive cast is per Phase 4 ActionInFlight godoc"
  - "Force-pull button is never label-gated and never hidden by allow-* labels (only the pinned-opt-out branch hides it) — matches Phase 4 semantic (force-pull is read-only w.r.t. the running container)"
  - "Rollback button is rendered (not hidden) when previous_digest is absent — disabled state is the affordance; the operator can see the row supports rollback in principle. Server-side ACT-08 also returns no-op for absent previous_digest"
  - "Component-local <style> blocks over Tailwind utility chains for stateful (hover/active/disabled/busy/focus-visible) recipes — Tailwind utility chains for color-mix-based hover backgrounds get unreadable fast (bg-[color-mix(...)] hover:bg-[color-mix(...)] active:bg-[color-mix(...)]); local <style> keeps the markup quiet"
  - "App.svelte rewired with inert no-op callbacks + empty busyServices Set (Rule 3 auto-fix — blocking issue) so the build remains green + manually smokable until Plan 05-04 rewrites it; the rewire is minimal and Plan 05-04 will replace App.svelte wholesale"
  - "Tag fallback in Row's imageTag derivation is 'latest' (per the brief's :latest watching semantic) — matches the wire's omitempty stripping when the server didn't populate tag"
  - "Lock icons in disabled-by-label branches share a common 24px Heroicons lock-closed path inlined directly (3 copies in Row.svelte — pinned cell, allow-update=false cell, allow-rollback=false cell); not refactored into a sub-component because the surrounding 'wrap with tooltip + aria-label' boilerplate per branch differs by copy"

patterns-established:
  - "Status enum sourced from StatusBadge.svelte (export type StatusKind) and re-imported by Row.svelte — single source of truth for the 8 status names; type drift between badge and row impossible by construction"
  - "ActionKind enum sourced from ActionButton.svelte (export type ActionKind) and consumed by Row + Table (and by Plan 05-04 App.svelte) — same single-source-of-truth pattern as StatusKind"
  - "Row.svelte status priority order (action_in_flight > action_error > pinned > stopped > update_available > current) matches 05-CONTEXT.md Area 2 verbatim — load-bearing for operator pattern recognition"
  - "Header timestamp uses Date.now() as the initial $state seed (not 0 or undefined) so the first paint after mount renders the correct relative time without waiting for the first 1s interval fire — eliminates a 'never→Xs ago' flash"
  - "CopyButton's setTimeout reset timer is cleared on rapid double-click via a stored ref — prevents the visual flicker that would happen if a second copy fired during the 1500ms confirmation window"

requirements-completed:
  - UI-01  # tokens consumed at the component layer (Plan 05-01 landed the @theme; this plan exercises them in components)
  - UI-02  # 7-column table replaces scaffold (Table.svelte preserves the structure + adds Row composition)
  - UI-03  # safety-label gating + lock icons + pinned opt-out
  - UI-04  # last-poll relative timestamp in Header
  - UI-07  # action buttons present and addressable per row (wiring happens in 05-04)
  - UI-09  # CopyButton present; full digest copied via navigator.clipboard.writeText

# Metrics
duration: ~6min
completed: 2026-05-15
---

# Phase 5 Plan 02: Svelte Component Tree Summary

**Six Svelte 5 components (StatusBadge, ActionButton, CopyButton, Row, Table, Header) + one pure helper (relative-time.ts) built against the Solaris tokens shipped in Plan 05-01; all `$props()`-driven, runes-only, prop-callback composition with the page-level state slice deferred to Plan 05-04.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-15T10:54:26Z
- **Completed:** 2026-05-15T10:59:54Z
- **Tasks:** 2
- **Files created:** 6 (5 components + 1 helper)
- **Files modified:** 2 (Table.svelte — full replace; App.svelte — transitional shell rewire)

## Accomplishments

- **6 new components shipped** under `ui/src/lib/`:
  - `StatusBadge.svelte` — 8 status kinds, color matrix per UI-SPEC.md §4.3 verbatim. `update_available` correctly uses `--color-pending` (yellow), NOT `--color-warning` (orange — the Plan 05-01 open-note for 05-02 is now locked).
  - `ActionButton.svelte` — 28x28 square icon button, 3 Heroicons (arrow-up-tray / arrow-uturn-left / arrow-path), busy spinner replaces icon, disabled state at 40% opacity, `aria-busy` swap.
  - `CopyButton.svelte` — 20x20 clipboard icon, `navigator.clipboard.writeText`, 1500ms cyan check on success / red x on failure, aria-live polite region for screen-reader announcement.
  - `Row.svelte` — single `<tr>` with `$derived` status (priority verbatim from 05-CONTEXT.md Area 2), label-gated action buttons with lock-icon fallback per label, pinned-opt-out branch hiding ALL buttons, 3 CopyButtons inline next to digest cells.
  - `Header.svelte` — sticky 64px, --color-bg-elev background; labeled text Refresh + Watch now buttons (NOT icon-only — header buttons are explicitly textual per UI-SPEC.md §4.1); 1s `$effect` clock tick driving `relativeTime(lastPollEnd, now)` with proper `clearInterval` cleanup (threat T-05-02-03 mitigation).
  - `Table.svelte` — replaces the Phase 1 scaffold. 7-column `<thead>` structure and empty-state row text preserved verbatim — load-bearing for `ui-table.spec.ts` in Plan 05-05.

- **1 helper shipped:** `relative-time.ts` — 12-LOC pure function; "never" | "Xs ago" | "Xm Ys ago" | "Xh Ym ago"; no dayjs/date-fns dep.

- **No new npm dependencies** — Heroicons copy-pasted as inline SVG (3 unique icons in ActionButton, 2 in CopyButton, 1 in StatusBadge, 1 in Row = 7 total; some shared with header). No `@heroicons/svelte`, no `lucide-svelte`. The no-extra-deps ethos holds.

- **App.svelte rewired transitionally** — uses the new Header + Table with inert no-op callbacks + empty `busyServices` Set so the build stays runnable until Plan 05-04 fully wires polling, actions, toasts, and modal. Plan 05-04 will replace `App.svelte` wholesale.

## Confirmation: Row.svelte Status Priority Matches 05-CONTEXT.md Area 2 Verbatim

The plan output spec requires explicit confirmation that Row's `$derived` status follows the 05-CONTEXT.md Area 2 priority order. Verbatim from 05-CONTEXT.md:

```
if (container.action_in_flight) return container.action_in_flight;
if (container.action_error)     return 'action_error';
if (container.pinned)           return 'pinned';
if (container.stopped)          return 'stopped';
if (container.update_available) return 'update_available';
return 'current';
```

Verbatim from `ui/src/lib/Row.svelte`:

```ts
const status = $derived.by<StatusKind>(() => {
  if (container.action_in_flight) {
    return container.action_in_flight as StatusKind;
  }
  if (container.action_error)     return 'action_error';
  if (container.pinned)           return 'pinned';
  if (container.stopped)          return 'stopped';
  if (container.update_available) return 'update_available';
  return 'current';
});
```

The cast `as StatusKind` is the only deviation (typesafe narrowing — the Container.action_in_flight wire field is typed as `string` from tygo, but the server emits exactly one of three values per Phase 4 ActionInFlight godoc; the cast surfaces that contract at the UI boundary). Priority order is identical.

## Confirmation: Table.svelte <thead> 7-Column Structure Preserved

The plan output spec requires explicit confirmation that the Phase 1 scaffold's `<thead>` structure was preserved (load-bearing for `ui-table.spec.ts` in Plan 05-05).

Phase 1 `<thead>` columns (in order): `container | image:tag | current digest | available digest | previous digest | status | actions`.

Plan 05-02 `<thead>` columns (in order): `container | image:tag | current digest | available digest | previous digest | status | actions`.

Identical column count (7) and identical column labels. The only changes are stylistic:
- Background switched from Tailwind `bg-zinc-100` → semantic `var(--color-bg-elev)` (base2)
- Text color switched from Tailwind `text-zinc-700` → semantic `var(--color-fg-strong)` (base02)
- `actions` cell text-align switched from `text-left` → `text-right` (per UI-SPEC.md §4.2 — actions cluster right so the icon buttons line up)

Empty-state row's wording preserved verbatim: `No watched containers yet` + `Label a service in your compose file with hmi-update.watch=true and it will appear here on the next poll.` Plan 05-05's ui-table.spec.ts will exercise both branches.

## Task Commits

1. **Task 1: Add leaf components (StatusBadge, ActionButton, CopyButton, relative-time)** — `739146f` (feat)
2. **Task 2: Compose Header + Table + Row from leaf components** — `4653a15` (feat)

## Files Created/Modified

- `ui/src/lib/StatusBadge.svelte` — new (8-kind status pill).
- `ui/src/lib/ActionButton.svelte` — new (28x28 icon-only button).
- `ui/src/lib/CopyButton.svelte` — new (20x20 clipboard button).
- `ui/src/lib/Row.svelte` — new (single-tr with status + actions + 3 copy buttons).
- `ui/src/lib/Header.svelte` — new (sticky header with last-poll timestamp).
- `ui/src/lib/relative-time.ts` — new (pure formatter helper).
- `ui/src/lib/Table.svelte` — modified (full replace; preserves Phase 1 thead + empty-state).
- `ui/src/App.svelte` — modified (transitional shell with inert callbacks; Plan 05-04 will rewrite).

## Decisions Made

- **`update_available` uses `--color-pending` (yellow), NOT `--color-warning` (orange).** Plan 05-01 SUMMARY surfaced this in its "Open Notes for 05-02" section — UI-SPEC.md §4.3 has orange reserved for the flutter/weston flicker warning (Plan 05-03), yellow for "ready when you are." Locked in StatusBadge.svelte's accent-var switch.
- **Component-local `<style>` blocks for stateful recipes.** Tailwind v4 utility chains for color-mix-based hover backgrounds get illegible fast (`bg-[color-mix(in_srgb,var(--color-accent)_10%,transparent)] hover:bg-[color-mix(in_srgb,var(--color-accent)_20%,transparent)]`). Component-local `<style>` keeps the recipe at the bottom of the file, near the markup. Same approach for `@keyframes spin` in StatusBadge and ActionButton.
- **Lock-icon SVG path inlined three times in Row.svelte rather than a sub-component.** The pinned-opt-out branch, allow-update=false branch, and allow-rollback=false branch each wrap the lock icon in different surrounding markup (different tooltips, different aria-labels, different parent gap-spacing) — extracting a `<LockIcon>` would just push the differences to props and lengthen the file. Three 24-px Heroicons paths copy-pasted is the readable choice.
- **`Container.action_in_flight` cast to `StatusKind` in Row.svelte.** Tygo emits the field as `string` (optional). Per Phase 4 `ActionInFlight` godoc, the server only ever writes `'updating'`, `'rolling_back'`, or `'force_pulling'`. Casting at the UI boundary surfaces the contract. A defensive default-to-`'current'` would mask a server bug; trusting the wire is the correct stance for the same-repo contract.
- **`Header.svelte`'s `now = $state(Date.now())` initial seed.** Initializing to `Date.now()` (not 0 or `undefined`) makes the first paint after mount render the correct relative time. A naïve `let now = $state(0)` would briefly render `Xs ago` with X equal to the seconds since the Unix epoch — operator-visible glitch.
- **Rollback button is rendered + disabled when no previous_digest, NOT hidden.** UI affordance: operators see the row supports rollback in principle; the disabled state communicates "nothing to roll back to" without removing the icon from the interface. Phase 4 server-side ACT-08 also returns no-op for absent previous_digest.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 — Blocking] Rewire App.svelte to compile with the new Table.svelte props**

- **Found during:** Task 2 (after writing the new `Table.svelte` that requires `onAction` + `busyServices`)
- **Issue:** The existing Phase 1 `App.svelte` calls `<Table containers={containers} />`. The new `Table.svelte` requires three props (`containers`, `onAction`, `busyServices`). Svelte 5 compiles silently with missing props, but at runtime `busyServices.has(c.service)` would throw on `undefined.has` as soon as the `/api/state` fetch populated `containers`. Manual smoke (and Phase 1's onMount fetch path) would break.
- **Fix:** Rewired `App.svelte` as a transitional shell — uses the new Header + Table with inert no-op callbacks + an empty `busyServices = new Set<string>()`. The transitional shell is documented inline as "Plan 05-04 will replace this file in full." Manual smoke now opens the page, fetches /api/state, renders the Table (empty state or populated), and the buttons are clickable but no-op.
- **Files modified:** `ui/src/App.svelte`
- **Commit:** `4653a15` (same commit as Task 2, since the rewire is the same logical change that landed Task 2's Row + Table + Header)
- **Why this isn't preempting Plan 05-04:** Plan 05-04's own scope per 05-CONTEXT.md Area 5 is to install `state = $state<State | null>(null)`, `isActing = $state(false)`, the 5s polling effect, the `postAction` helper integration, toasts state slice, modal state slice — a full file rewrite. Plan 05-02's transitional shell is ~30 LOC of inert callbacks that 05-04 will throw away. Necessary for the build-stays-green success criterion; doesn't compete with 05-04.

## Issues Encountered

- **None blocking.** `make check-types` and `npm --prefix ui run build` and `npm --prefix ui run check` (svelte-check) all green on every commit.
- **Environment note (carried from Plan 05-01):** `tygo` lives at `/Users/jonb/go/bin/tygo` but is not on the default shell `PATH`. `make check-types` requires `PATH=/Users/jonb/go/bin:$PATH make check-types`. Documented for future executors in this same env. Not a code regression.

## Verification

All acceptance criteria from both tasks green:

### Task 1 (leaf components)

- `grep -F 'export function relativeTime' ui/src/lib/relative-time.ts` → 1 match
- `grep -F 's ago' ui/src/lib/relative-time.ts` → 4 matches (≥1 required)
- `ls ui/src/lib/StatusBadge.svelte ui/src/lib/ActionButton.svelte ui/src/lib/CopyButton.svelte` → all three present
- `grep -F 'navigator.clipboard.writeText' ui/src/lib/CopyButton.svelte` → 2 matches (≥1 required)
- `grep -F 'aria-label' ui/src/lib/ActionButton.svelte` → 2 matches (≥1 required)
- `grep -F 'data-spinner' ui/src/lib/ActionButton.svelte` → 2 matches (≥1 required)

### Task 2 (Row + Table + Header)

- `ls ui/src/lib/Row.svelte ui/src/lib/Table.svelte ui/src/lib/Header.svelte` → all three present
- `grep -F '$derived' ui/src/lib/Row.svelte` → 5 matches (≥1 required)
- `grep -F 'hmi-update.allow-update' ui/src/lib/Row.svelte` → 4 matches (≥1 required)
- `grep -F 'pinned' ui/src/lib/Row.svelte` → 8 matches (≥1 required — pinned-row branch present)
- `grep -F 'setInterval' ui/src/lib/Header.svelte` → 1 match
- `grep -F 'lastPollEnd' ui/src/lib/Header.svelte` → 3 matches (≥1 required)
- `grep -F 'busyServices' ui/src/lib/Table.svelte` → 4 matches (≥1 required)
- `grep -c -F '<thead' ui/src/lib/Table.svelte` → 1 (table structure preserved)
- `grep -F 'No watched containers yet' ui/src/lib/Table.svelte` → 1 (empty-state preserved)

### Build + type gates

- `npm --prefix ui run build` → exit 0 (Vite v7.3.3, 119 modules, 282 ms, emits `internal/api/dist/assets/index-DFt20bqk.js` 48.72 kB / `index-DtJicLNY.css` 15.66 kB)
- `npm --prefix ui run check` (svelte-check) → exit 0 (108 files, 0 errors, 0 warnings)
- `make check-types` (with tygo on PATH) → exit 0 (no drift)
- `go test ./internal/api/...` → ok 1.389 s (no Go-side regression)

### Bundle sanity

- Bundle module count: 109 → 119 (added 10 — accounts for the 6 new Svelte components + their TypeScript companions emitted by `@sveltejs/vite-plugin-svelte`)
- Bundle JS size: 34.26 kB → 48.72 kB (+14.46 kB; ~42% increase to host 6 new components + Row composition; well within the 30 MB image target)
- CSS bundle size: 12.02 kB → 15.66 kB (component-local `<style>` blocks compiled in)

## Threat Model Compliance

- **T-05-02-01 (Tampering — Row.svelte label-gated action visibility):** ACCEPT — disposition unchanged. UI gating is UX-only; server enforces (Phase 4 SAFE-01/02). A user who DOM-edits to expose the Update button hits a 409.
- **T-05-02-02 (Info Disclosure — StatusBadge error reason tooltip):** ACCEPT — disposition unchanged. `container.action_error` reaches StatusBadge's `title` attribute verbatim; LAN-only posture documented.
- **T-05-02-03 (DoS — Header now-tick setInterval):** MITIGATED. `$effect`'s return-callback runs `clearInterval(t)` on unmount; verified by reading `Header.svelte` line 49-51. HMR + future component teardown won't leak timers.
- **T-05-02-04 (Accessibility — ActionButton icon-only):** MITIGATED. `aria-label={\`${kind} ${service}\`}` on every ActionButton (grep'd above). `title` attribute supplements as a sighted-user tooltip. Focus ring is 2px solid `--color-accent` on `:focus-visible` via component-local `<style>`.
- **T-05-02-05 (Pinned bypass):** MITIGATED. `Row.svelte`'s `{#if container.pinned}` branch hides ALL three action buttons and renders a single lock icon with `title="pinned: opt-out"`. Server-side enforcement is the authoritative layer; this is the UI's complement.

No new threat surface introduced beyond what the plan's threat register enumerated. No `threat_flags` to surface.

## Open Notes for Plan 05-03

- **StatusBadge.svelte exports `StatusKind`.** Toast.svelte may need a `kind` discriminator that's *separate* from StatusKind (`success` | `error` | `warning` | `info` per UI-SPEC.md §4.6 — different palette anchors). Don't conflate; declare a new `ToastKind` union in Toast.svelte.
- **WarningModal.svelte needs to mount in App.svelte alongside Table.** Plan 05-03 should add the modal *and* the ToastContainer to App.svelte's transitional shell; Plan 05-04 will then layer in the actual `pendingAction` + `toasts` state slices. Suggest 05-03 follows the same "inert no-op callbacks" pattern this plan used so 05-04 doesn't have to undo 05-03's wiring.
- **`--color-warning` (orange) is still unconsumed.** This plan reserved it intentionally per the Plan 05-01 lock-in note. Plan 05-03's Toast (warning kind) + WarningModal (flicker copy + title-row triangle icon) will be its first real consumer.
- **Focus trap helper (05-RESEARCH.md §G.1) is a 20-LOC Svelte action.** Plan 05-03 should land it in `ui/src/lib/focus-trap.ts` and `use:focusTrap` on the WarningModal panel.

## Open Notes for Plan 05-04

- **App.svelte is transitional.** Plan 05-04 owns the rewrite; this plan's no-op callbacks (`noopAction`, `noopRefresh`, `noopWatchNow`) and empty `busyServices` Set should be thrown away. The Set-based busy-services pattern (Row.svelte reads `busyServices.has(c.service)`) is the load-bearing contract — keep that shape; only swap the empty Set for a `$state<Set<string>>(new Set())` that mutates on action POST start/end.
- **`onAction` callback chain:** `App.svelte → Table.svelte → Row.svelte` already plumbed; 05-04 just replaces the `noopAction` body with a function that:
  1. checks `requiresWarning(svc)` for flutter/weston pre-warning (Plan 05-03)
  2. sets `busyServices.add(svc)` (reassign to trigger reactivity: `busyServices = new Set([...busyServices, svc])`)
  3. calls `postAction(svc, kind)` from `ui/src/lib/actions.ts` (Plan 05-04 ships this)
  4. on resolve/reject: removes svc from busyServices, pushes toast, lets the next poll re-sync the row
- **Polling state shape:** 05-CONTEXT.md Area 5 spec'd `let state = $state<State | null>(null)`. The transitional App.svelte uses two separate `$state` slices (`containers` array and `lastPollEnd` string) — 05-04 should consolidate into a single `state: State | null` and derive `containers = $derived(Object.values(state?.containers ?? {}))`. The Header's `lastPollEnd` prop becomes `state?.last_poll_end`.
- **Watch now endpoint:** Header passes `onWatchNow` straight through; Plan 05-04 should implement it as `POST /api/poll-now` with graceful 404-fallback to `onRefresh()` per 05-CONTEXT.md Area 6. If the Phase 3 endpoint isn't shipped yet, the fallback is the v1 behavior.

## Open Notes for Plan 05-05

- **Empty-state row wording is the load-bearing assertion** for `ui-table.spec.ts`. Preserve the exact string `No watched containers yet` and the `hmi-update.watch=true` code span. Plan 05-05 should grep for both.
- **Lock icon is rendered as inline SVG with no class/test-id hook.** If the Playwright spec needs to assert "lock visible when allow-update=false", consider asserting on the `aria-label` text instead (e.g., `await expect(page.getByLabel(/Update .* disabled by hmi-update.allow-update=false/)).toBeVisible()`) — that's stable across icon SVG path changes.
- **`data-spinner` attribute is the cross-cutting hook** for prefers-reduced-motion spinner-preservation. If any test wants to assert "spinner spins even in reduced-motion mode," it should query for `[data-spinner]` and read computed animation-duration.

## User Setup Required

None — pure frontend changes; no env vars, no compose changes, no auth gates.

## Next Plan Readiness

- **Plan 05-03 (Toast + WarningModal):** UNBLOCKED. Solaris tokens consumed; the visual recipe pattern (inline color-mix style: directives + component-local <style> for stateful recipes) is set. WarningModal's flicker-copy `{service}` substitution will use the same `$props()` discipline.
- **Plan 05-04 (App.svelte polling + actions):** UNBLOCKED. All three callback chains (`onAction`, `onRefresh`, `onWatchNow`) are plumbed top-to-bottom; 05-04 only replaces the no-op bodies. `busyServices` reactivity contract (Set<string>, reassign-not-mutate) is locked in Table.svelte → Row.svelte.
- **Plan 05-05 (Playwright e2e + Pitfall 8):** UNBLOCKED at the UI side — components exist and render; e2e specs can now begin RED-first against the unwired UI (with the expectation that some assertions go GREEN only after Plan 05-04 wires actions).

## Self-Check: PASSED

- File `ui/src/lib/StatusBadge.svelte` exists: FOUND
- File `ui/src/lib/ActionButton.svelte` exists: FOUND
- File `ui/src/lib/CopyButton.svelte` exists: FOUND
- File `ui/src/lib/Row.svelte` exists: FOUND
- File `ui/src/lib/Header.svelte` exists: FOUND
- File `ui/src/lib/Table.svelte` exists (modified): FOUND
- File `ui/src/lib/relative-time.ts` exists: FOUND
- File `ui/src/App.svelte` exists (modified): FOUND
- Commit `739146f` (Task 1) present in `git log`: FOUND
- Commit `4653a15` (Task 2) present in `git log`: FOUND
- Vite output `internal/api/dist/index.html` regenerated: FOUND
- Vite output `internal/api/dist/assets/index-DFt20bqk.js` regenerated: FOUND
- Vite output `internal/api/dist/assets/index-DtJicLNY.css` regenerated: FOUND
- All 15 acceptance-criteria grep gates: ALL PASSED
- Build + check-types + svelte-check + go test: ALL EXIT 0
- STATE.md not modified: CONFIRMED (per phase context — not touched)
- ROADMAP.md not modified: CONFIRMED (per phase context — not touched)

---
*Phase: 05-web-ui-completeness*
*Plan: 02*
*Completed: 2026-05-15*
