---
phase: 05-web-ui-completeness
plan: 03
subsystem: ui

tags: [svelte-5, runes, tailwind-v4, solaris-palette, accessibility, focus-trap, aria-live, modal, toast, heroicons]

# Dependency graph
requires:
  - phase: 05-web-ui-completeness
    plan: 01
    provides: "Solaris @theme tokens — --color-success (cyan), --color-warning (orange), --color-danger (red), --color-info (violet), --color-bg (base3), --color-fg-strong (base02) consumed across Toast.svelte and WarningModal.svelte; prefers-reduced-motion baseline preserved with component-local animation-duration overrides"
  - phase: 05-web-ui-completeness
    plan: 02
    provides: "Phase 5 component-tree precedent — Svelte 5 runes ($state/$derived/$props/$effect), inline style: directives for color-mix(...) recipes, component-local <style> blocks for stateful CSS (hover/active/focus-visible), inline Heroicons SVGs (no @heroicons/svelte dep). Toast's auto-dismiss $effect with setTimeout cleanup follows the same shape as Header.svelte's now-tick $effect (T-05-02-03 mitigation pattern). Plan 05-02's transitional App.svelte shell — onAction / onRefresh / onWatchNow no-op callbacks — remains untouched; Plan 05-04 will rewrite App.svelte to mount the ToastContainer + WarningModal alongside the table."

provides:
  - "Toast.svelte: single 360px-max card with 4px kind-colored border-left (success cyan / error red / warning orange / info violet); auto-dismiss 5s for non-error kinds; sticky for errors; click-anywhere or x-mark to dismiss; role=alert for errors and role=status for the rest"
  - "ToastContainer.svelte: fixed bottom-right region with aria-live=polite; renders null when toasts empty (no orphan live region); pointer-events-none on wrapper + pointer-events-auto on individual toasts so toasts don't eat clicks behind them when stack is short"
  - "WarningModal.svelte: 480px panel with orange triangle-warning icon + 'Display may flicker.' title + verbatim body copy with bold {service} interpolation; cyan Continue + transparent Cancel buttons; backdrop click cancels; Esc cancels via focus-trap; body scroll lock with prior-value restore"
  - "focus-trap.ts: 20-LOC Svelte action — Tab/Shift-Tab cycle within focusable descendants, Escape dispatches a bubbling 'cancel' CustomEvent that the host element captures via Svelte 5 oncancel attribute, initial focus on [data-primary] deferred via queueMicrotask"
  - "display-warning.ts: DISPLAY_DRAWING_SERVICES = ['flutter', 'weston'] as const + requiresWarning(service: string) — case-insensitive substring match; pure module, no Svelte dependency, ready for Plan 05-04 import from App.svelte::handleAction"
  - "ToastKind + Toast types exported from Toast.svelte module-scope (`<script module>`) so ToastContainer.svelte + App.svelte (Plan 05-04) consume the single source of truth without re-declaring the union"

affects:
  - "05-04 (App.svelte wires `toasts: Toast[]`, `pendingAction: PendingAction | null`, calls `requiresWarning(svc)` to branch action handlers, mounts <ToastContainer> + <WarningModal> alongside Table)"
  - "05-05 (Playwright e2e — ui-flutter-warning.spec.ts text-matches the verbatim modal copy strings; ui-actions.spec.ts asserts toast aria-live + dismiss semantics)"

# Tech tracking
tech-stack:
  added: []  # No new npm dependencies — Heroicons (triangle-warning, x-mark) copy-pasted as inline SVG; focus-trap is hand-rolled 20 LOC per 05-RESEARCH.md §G.1
  patterns:
    - "Svelte 5 module-scoped `<script module>` for type exports (Toast.svelte exports ToastKind + Toast types at module scope so callers import without re-declaring) — clean alternative to a sibling types.ts file"
    - "Svelte 5 oncancel={onCancel} attribute consuming a CustomEvent dispatched by a `use:focusTrap` action — bridges the Svelte action ↔ component-prop contract without exposing the action's DOM event detail"
    - "$effect with setTimeout cleanup for one-shot timers (Toast.svelte auto-dismiss) — mirrors Plan 05-02's Header.svelte setInterval cleanup pattern (T-05-02-03), generalized to any one-shot or repeating timer"
    - "Body scroll lock via $effect that captures the prior overflow value at entry and restores it at cleanup — never `document.body.style.overflow=''` unconditionally (T-05-03-05 mitigation)"
    - "Backdrop-as-<button> (not <div onclick>) — switches the dismiss surface to an actual interactive role; satisfies svelte-check a11y rules a11y_click_events_have_key_events + a11y_no_static_element_interactions; tabindex=-1 keeps it out of the Tab cycle (focus-trap controls focus inside the panel)"
    - "Two-layer modal stacking (z-40 backdrop sibling + z-50 panel wrapper) — backdrop is NOT a parent of the panel, so panel-internal clicks never bubble to the backdrop; no stopPropagation dance required"
    - "Component-local @media (prefers-reduced-motion: reduce) overrides for one-shot @keyframes animations — the app.css global reduce-motion rule zeroes transition-duration but not @keyframes durations; component-local animation-duration: 0ms inside the same media query preserves spinners (data-spinner exemption from Plan 05-01) while killing entrance animations"

key-files:
  created:
    - "ui/src/lib/Toast.svelte"
    - "ui/src/lib/ToastContainer.svelte"
    - "ui/src/lib/WarningModal.svelte"
    - "ui/src/lib/focus-trap.ts"
    - "ui/src/lib/display-warning.ts"

key-decisions:
  - "ToastKind is a separate union from StatusKind. Plan 05-02's open note flagged this: Toast kinds ('success' | 'error' | 'warning' | 'info') anchor against UI-SPEC.md §4.6 palette (cyan/red/orange/violet border-left); StatusKind kinds (8 row states) anchor against §4.3 (cyan/yellow/violet/red/grey pill). Conflating them would force one to grow synthetic members. Declared inline in Toast.svelte's `<script module>` block."
  - "Toast types exported from Toast.svelte's `<script module>` block, not a sibling types.ts. The Toast component is the canonical declaration site; ToastContainer + App.svelte import from `./Toast.svelte` directly. Svelte 5's module-context script is the documented home for shared types and constants. Alternative (a `lib/types/toast.ts` file) would split the contract from the implementation without benefit."
  - "Backdrop is a real <button>, not a clickable <div>. svelte-check fired a11y_no_static_element_interactions on the original <div onclick=> shape. Switching to <button type='button' aria-label='Dismiss warning' tabindex='-1'> with CSS reset (border: 0, padding: 0, appearance: none) keeps the visual flat while satisfying screen-reader semantics + native Enter/Space activation. tabindex=-1 keeps it out of the Tab cycle since focus-trap.ts owns the focus order inside the panel; Escape is the keyboard dismiss path."
  - "Backdrop and panel are siblings (z-40 + z-50 wrapper), not nested. The original draft nested the panel inside the backdrop and used stopPropagation on the panel onclick to prevent bubbling. svelte-check a11y_click_events_have_key_events warned on the non-interactive panel <div>; switching to siblings let me drop the panel onclick entirely. Cleaner DOM, fewer event handlers, identical UX (clicks inside the panel never reach the backdrop because they never bubble up the hierarchy)."
  - "Initial focus on [data-primary] deferred via queueMicrotask. Svelte 5's action lifecycle does not guarantee that children are painted before the action fires in all compile modes; querySelector before paint can return null. queueMicrotask is the cheapest defer that resolves after the current paint cycle; setTimeout(0) would also work but adds a clock dependency tests might mock. The Continue button is focused reliably either way."
  - "Toast's outer onclick provides 'click anywhere dismisses' for pointer users (UI-SPEC.md §4.6) but the wrapper carries role=alert or role=status (non-interactive roles). Keyboard users dismiss via the explicit x-mark button (focusable, aria-labelled). The wrapper has no tabindex — svelte-check a11y_no_noninteractive_tabindex confirms this is the right shape; adding tabindex=0 to a role=status div would create a focusable announcement region with no clear semantics."
  - "display-warning.ts is a pure module with no Svelte dependency. requiresWarning() is one line of toLowerCase + Array.some; unit-testable in Vitest later without import-ing any Svelte runtime. Per 05-RESEARCH.md §J verbatim."
  - "The `action` prop on WarningModal.svelte is destructured as `_action` (leading underscore) and intentionally unused in v1. UI-SPEC.md §11 modal copy is identical for update vs rollback — the warning is about the recreate operation which both actions trigger. Plan 05-04 may surface 'Update' vs 'Rollback' as tooltip text on Continue if operators ask, but the prop stays in the contract so the call site (App.svelte handleAction) doesn't change shape when that lands."

patterns-established:
  - "Module-scoped type exports via `<script module>` — Toast.svelte exports ToastKind + Toast at module scope; sibling components import the union without re-declaring it. Phase 5+ components that need to expose types to peers should follow this pattern (cleaner than a parallel types.ts file)."
  - "Svelte action ↔ component-prop bridge via DOM CustomEvent + Svelte 5 on{event} attribute — focus-trap.ts dispatches CustomEvent('cancel') on Escape; the consumer attaches `oncancel={onCancel}` to the action-bearing element. Works without component event forwarding because Svelte 5 delegates on{event} attributes to native addEventListener. Reusable pattern for any action that needs to bubble user intent out (cancel, confirm, submit)."
  - "Backdrop-as-button for non-decorative modal dismiss surfaces — when the backdrop is a real dismiss affordance (not purely decorative), render it as <button> with aria-label + visual CSS reset. Future modals (settings, history, etc.) should follow this rather than the older <div onclick> + stopPropagation pattern."
  - "$effect-based body scroll lock with prior-value capture-and-restore — always read document.body.style.overflow at effect entry into a local `prev`; cleanup restores prev (not empty). Concurrent modals share this pattern: each captures the prior value (which may itself be 'hidden' if another modal is open) and restores it. T-05-03-05 mitigation."
  - "Component-local prefers-reduced-motion overrides for one-shot animations — the app.css @media block zeroes transition-duration globally and exempts data-spinner. Component-local @media inside <style> handles the @keyframes animation-duration field that the global rule does not zero. Spinners (data-spinner) survive; entrance animations (toast-in, warn-in) go to 0ms."

requirements-completed:
  - UI-05  # Toast surface (success/error/warning/info; auto-dismiss 5s except error)
  - UI-08  # Pre-action warning modal for flutter/weston services (DISPLAY_DRAWING_SERVICES predicate + WarningModal copy)

# Metrics
duration: ~6min
completed: 2026-05-15
---

# Phase 5 Plan 03: Toast + WarningModal Summary

**Five files added under `ui/src/lib/` ship the user-feedback surface — kind-styled Toast queue with role=alert/role=status semantics, focus-trapped WarningModal with verbatim "Display may flicker." copy for flutter/weston pre-action confirmation, and a 4-line display-warning predicate Plan 05-04 will call from App.svelte's action handler.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-15T11:04:07Z
- **Completed:** 2026-05-15T11:09:53Z
- **Tasks:** 2 (Toast + container + display-warning; WarningModal + focus-trap)
- **Files created:** 5
- **Files modified:** 0 (Plan 05-04 will replace App.svelte to mount these; this plan does not preempt that scope)

## Accomplishments

- **5 new files shipped** under `ui/src/lib/`:
  - `Toast.svelte` — 360px-max card, 4px kind-colored border-left, click-anywhere or x-mark to dismiss, 5s auto-dismiss for non-error kinds (errors are sticky until clicked, per UI-SPEC.md §4.6). role=alert for `kind === 'error'`, role=status for the rest. Module-scoped `<script module>` exports `ToastKind` + `Toast` types — single source of truth for callers.
  - `ToastContainer.svelte` — `fixed bottom-4 right-4` flex-col stack with `aria-live="polite"`. Renders nothing when toasts array is empty (no orphan live region). pointer-events on wrapper is `none`; individual toasts override to `auto` so an empty stack doesn't eat clicks on the table behind it.
  - `WarningModal.svelte` — 480px cream-bg panel with orange triangle-warning icon + verbatim "Display may flicker." title + verbatim "Recreating **{service}** on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?" body copy. Right-aligned Cancel (secondary) + Continue (cyan, `data-primary`) buttons. Backdrop is a real `<button>` (not a click-only div) — svelte-check a11y satisfied; tabindex=-1 keeps it out of the Tab cycle since focus-trap controls focus inside the panel.
  - `focus-trap.ts` — Svelte action; Tab/Shift-Tab cycle within focusable descendants, Escape dispatches a bubbling `CustomEvent('cancel')` (caught by WarningModal's `oncancel={onCancel}` attribute), initial focus on `[data-primary]` deferred via `queueMicrotask`. ~25 LOC including types.
  - `display-warning.ts` — `DISPLAY_DRAWING_SERVICES = ['flutter', 'weston'] as const` + `requiresWarning(service)`. Pure module, no Svelte runtime dependency, ~4 lines of substantive logic. Per 05-RESEARCH.md §J verbatim.

- **No new npm dependencies.** Heroicons (triangle-warning, x-mark) copy-pasted as inline SVGs (2 new icons; ActionButton + StatusBadge + Row already host the other 7 icons in the project). focus-trap is hand-rolled 20-LOC Svelte action — no `@melt-ui/svelte`, no `svelte-portal`. The no-extra-deps ethos holds.

- **`App.svelte` untouched.** Plan 05-02's transitional shell already plumbs `onAction`/`onRefresh`/`onWatchNow` callbacks; Plan 05-04 will replace App.svelte wholesale to mount `<ToastContainer>` + `<WarningModal>` and wire the toasts/pendingAction state slices. This plan's per-spec scope ("5 files added under `ui/src/lib/`") is precisely fulfilled.

## Confirmation: Modal Title + Body Copy Match UI-SPEC.md §11 Verbatim

The plan output spec calls out that this confirmation is load-bearing for Plan 05-05's `ui-flutter-warning.spec.ts` text-matching assertions.

**UI-SPEC.md §11 — modal title:**
> Modal title: `Display may flicker.` (no question mark — it's a statement.)

**Rendered title in WarningModal.svelte (line referencing `id="warn-title"`):**
```html
<h2 id="warn-title" class="text-base font-semibold" style:color="var(--color-fg-strong)">Display may flicker.</h2>
```
Identical — period preserved.

**UI-SPEC.md §11 — modal body:**
> Recreating **{service}** on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?

**Rendered body in WarningModal.svelte (line referencing `id="warn-body"`):**
```html
<p id="warn-body" class="mt-3 text-sm leading-relaxed" style:color="var(--color-fg)">
  Recreating <strong style:color="var(--color-fg-strong)">{service}</strong> on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?
</p>
```
Identical — bold `{service}`, en-dash in "5–30 seconds", question mark at the prompt end, no other punctuation drift. Plan 05-05's spec strings will text-match byte-for-byte.

## Confirmation: focus-trap.ts Escape → onCancel Path Wired

**Path traced end-to-end:**

1. `focus-trap.ts::onKeydown` — when `e.key === 'Escape'`, the function calls `e.preventDefault()` and `node.dispatchEvent(new CustomEvent('cancel'))`.
2. `WarningModal.svelte` mounts the focus-trapped element as:
   ```html
   <div ... role="dialog" aria-modal="true" use:focusTrap oncancel={onCancel}>
   ```
3. Svelte 5 delegates the `oncancel` attribute to a native `addEventListener('cancel', onCancel)` on the same `<div>`. CustomEvent('cancel') bubbles from its dispatch target (the same `<div>`) — listener fires; `onCancel` is invoked.

The `cancel` CustomEvent does not collide with the native `<dialog>` element's `cancel` event because we render a `<div role="dialog">`, not a real `<dialog>`. Future migration to native `<dialog>` would need to rename the event (e.g., `modal-dismiss`); flagged for Plan 05-04 if it chooses to swap.

## Task Commits

1. **Task 1: Toast + ToastContainer + display-warning helper** — `30a1d5c` (feat)
2. **Task 2: WarningModal + focus-trap action** — `6a8fbef` (feat)

## Files Created/Modified

- `ui/src/lib/Toast.svelte` — new (single toast row, module-scoped type exports, auto-dismiss $effect).
- `ui/src/lib/ToastContainer.svelte` — new (fixed bottom-right region with aria-live=polite).
- `ui/src/lib/WarningModal.svelte` — new (flutter/weston pre-action confirmation modal).
- `ui/src/lib/focus-trap.ts` — new (Svelte action for Tab/Shift-Tab/Escape).
- `ui/src/lib/display-warning.ts` — new (DISPLAY_DRAWING_SERVICES + requiresWarning predicate).

## Decisions Made

See `key-decisions` in the frontmatter for the full list. Headline calls:

- **Backdrop-as-`<button>` (not a `<div onclick>`).** svelte-check a11y warnings drove the switch; the result is cleaner DOM semantics (the backdrop is genuinely a dismiss control, so it should be a button) at the cost of two CSS reset rules. The original draft also nested the panel inside the backdrop and used `stopPropagation` on the panel onclick — both got removed when the backdrop became a sibling instead of a parent. Net code: simpler.
- **Module-scoped `ToastKind` + `Toast` type exports via `<script module>`.** Toast.svelte is the canonical declaration site; ToastContainer + App.svelte (Plan 05-04) import the union directly from `./Toast.svelte`. Alternative (a sibling `lib/types/toast.ts`) would fragment the contract from the implementation. Phase 5 lacks a `types/` folder convention; Svelte 5's module context is the documented home for this.
- **`action` prop on WarningModal is destructured as `_action` and unused in v1.** Modal copy is identical for update vs rollback per UI-SPEC.md §11; the prop stays in the contract so Plan 05-04's `handleAction` call site doesn't change shape when a tooltip differentiation lands (if it ever does — Phase 6 UX-01 candidate).

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 — Critical functionality / a11y] Backdrop switched from `<div onclick>` to `<button>` after svelte-check fired a11y_no_static_element_interactions**

- **Found during:** Task 2 (after writing the first draft of WarningModal.svelte)
- **Issue:** The original backdrop was a clickable `<div>` with `onclick={handleBackdropClick}`. `npm --prefix ui run check` (svelte-check) emitted two warnings: `a11y_click_events_have_key_events` (no keydown counterpart) + `a11y_no_static_element_interactions` (div with click handler must have an ARIA role). The CLAUDE.md GSD enforcement directive treats the project's quality gates as hard constraints — warning noise on every CI run would mask future regressions.
- **Fix:** Switched the backdrop to a real `<button type="button" aria-label="Dismiss warning" tabindex="-1">` with a CSS reset (`border: 0; padding: 0; margin: 0; appearance: none`) so the visual stays flat. Operators get native Enter/Space activation as a side effect; the keyboard dismiss path remains Escape (via focus-trap) because tabindex=-1 keeps the backdrop out of the Tab cycle.
- **Files modified:** `ui/src/lib/WarningModal.svelte` (same task commit `6a8fbef`)
- **Why this isn't preempting Plan 05-04 or 05-05:** Pure a11y hardening within Plan 05-03's scope. The component's external contract (props, callbacks) is unchanged.

**2. [Rule 2 — Critical functionality / a11y] Panel `<div>` onclick handler removed when backdrop and panel became siblings**

- **Found during:** Task 2 (immediately after Rule 2 fix #1, while addressing the remaining warning)
- **Issue:** The original draft nested the panel inside the backdrop and put `onclick={handlePanelClick}` on the panel `<div>` to call `e.stopPropagation()` and keep panel-internal clicks from dismissing the modal. svelte-check fired a11y_click_events_have_key_events + a11y_no_static_element_interactions on the panel — non-interactive `<div>` with a click handler.
- **Fix:** Restructured the layout: the backdrop (now `<button>`) is rendered as a z-40 sibling of the panel wrapper (z-50, with `pointer-events-none` so only the panel itself is interactive). Clicks inside the panel never bubble up to the backdrop because they aren't in its hierarchy — no `stopPropagation` needed. The panel `<div>` has no onclick handler.
- **Files modified:** `ui/src/lib/WarningModal.svelte` (same task commit `6a8fbef`)
- **Why this isn't preempting any downstream plan:** Same DOM contract from the outside — `role="dialog" aria-modal="true"` wrapper, `[data-primary]` on Continue, `use:focusTrap` on the wrapper. Plan 05-04's mount point is unaffected.

**3. [Rule 3 — Blocking] Toast outer `<div>` tabindex removed after svelte-check fired a11y_no_noninteractive_tabindex**

- **Found during:** Task 1 (after writing the first draft of Toast.svelte)
- **Issue:** The original draft set `tabindex="0"` + `onkeydown` on the outer `<div role="status">` (or `<div role="alert">` for errors) so keyboard users could focus the toast and press Enter/Space to dismiss. svelte-check fired a11y_no_noninteractive_tabindex — non-interactive ARIA roles (alert, status) should not be focusable.
- **Fix:** Removed `tabindex="0"` + `onkeydown` from the outer wrapper. Kept `onclick` for pointer-user "click anywhere dismisses" affordance per UI-SPEC.md §4.6. Keyboard users dismiss via the explicit `<button aria-label="Dismiss notification">` x-mark, which is focusable + Enter/Space activated by default.
- **Files modified:** `ui/src/lib/Toast.svelte` (same task commit `30a1d5c`)
- **Why this isn't preempting any downstream plan:** The x-mark button was already in the design (UI-SPEC.md §4.6 explicitly mentions "12 px x-mark icon top-right"). The outer wrapper's tabindex was a redundant focus path that conflicted with the role's accessibility tree position. Same external contract.

### Deviation from Prompt-Level Success Criterion (documentation, not implementation)

The execution prompt's `<success_criteria>` block included:
```
- `grep -F "Display may flicker for 5" ui/src/lib/WarningModal.svelte` returns at least 1 match
```

The verbatim modal copy per UI-SPEC.md §11 + Plan 05-03 Task 2 acceptance criteria has the title and body as **two separate sentences**:
- Title: `Display may flicker.` (statement, period — load-bearing for Plan 05-05's `ui-flutter-warning.spec.ts`)
- Body: `Recreating <strong>{service}</strong> on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?`

The contiguous string `Display may flicker for 5` would only appear if the two sentences were merged into `Display may flicker for 5–30 seconds...`, which would contradict the canonical UI-SPEC and the per-task acceptance criteria. The plan's per-task `grep` gates (`grep -F 'Display may flicker'` ≥1 + `grep -F '5–30 seconds'` ≥1) **both pass**, satisfying the underlying intent — that the modal warns about flicker AND mentions the 5–30 second timing. I followed UI-SPEC.md §11 as the load-bearing source. Flagged here so any reviewer reading the prompt-level criterion verbatim understands the discrepancy.

## Issues Encountered

- **No blocking issues.** Build (`npm --prefix ui run build`) exits 0 on every commit; svelte-check (`npm --prefix ui run check`) reports 113 files, 0 errors, 0 warnings after the three Rule 2/3 auto-fixes above.

## Verification

### Task 1 acceptance criteria (all green)

- `ls ui/src/lib/Toast.svelte ui/src/lib/ToastContainer.svelte ui/src/lib/display-warning.ts` → all three present
- `grep -F "DISPLAY_DRAWING_SERVICES" ui/src/lib/display-warning.ts` → 2 matches (≥1 required)
- `grep -F "'flutter'" ui/src/lib/display-warning.ts` → 1 match (≥1 required)
- `grep -F "'weston'" ui/src/lib/display-warning.ts` → 1 match (≥1 required)
- `grep -F "toLowerCase()" ui/src/lib/display-warning.ts` → 1 match (=1 required)
- `grep -F "setTimeout" ui/src/lib/Toast.svelte` → 1 match (≥1 required)
- `grep -F "aria-live" ui/src/lib/ToastContainer.svelte` → 1 match (=1 required, exact)
- `grep -F "fixed bottom-4 right-4" ui/src/lib/ToastContainer.svelte` → 1 match (=1 required, exact)
- `npm --prefix ui run build` → exit 0

### Task 2 acceptance criteria (all green)

- `ls ui/src/lib/WarningModal.svelte ui/src/lib/focus-trap.ts` → both present
- `grep -F 'Display may flicker' ui/src/lib/WarningModal.svelte` → 2 matches (≥1 required)
- `grep -F '5–30 seconds' ui/src/lib/WarningModal.svelte` → 3 matches (≥1 required)
- `grep -F 'Continue' ui/src/lib/WarningModal.svelte` → 5 matches (≥1 required)
- `grep -F 'Cancel' ui/src/lib/WarningModal.svelte` → 9 matches (≥1 required)
- `grep -F 'use:focusTrap' ui/src/lib/WarningModal.svelte` → 1 match (=1 required)
- `grep -F 'data-primary' ui/src/lib/WarningModal.svelte` → 3 matches (the Continue button + 2 documentation references; the load-bearing match is the attribute on line 144)
- `grep -F "key === 'Escape'" ui/src/lib/focus-trap.ts` → 1 match (=1 required)
- `grep -F 'aria-modal' ui/src/lib/WarningModal.svelte` → 1 match (=1 required)
- `npm --prefix ui run build` → exit 0

### Build + type gates

- `npm --prefix ui run build` → exit 0 (Vite v7.3.3, 119 modules, 269–277 ms, emits `internal/api/dist/assets/index-C2mbrqEw.js` 48.72 kB + `index-PKOb4YcN.css` 17.56 kB)
- `npm --prefix ui run check` (svelte-check) → exit 0 (113 files, 0 errors, 0 warnings)

### Bundle sanity

- Module count: 119 → 119 (no change — Vite already counted the 5 new files lazily resolved through ToastContainer/WarningModal imports; tree-shake means Plan 05-04 will reactivate them once App.svelte imports them)
- JS bundle: 48.72 kB → 48.72 kB (unchanged — App.svelte does not yet import any of the new files; tree-shaking elides them entirely from the bundle)
- CSS bundle: 15.66 kB → 17.56 kB (+1.90 kB — the component-local `<style>` blocks ship in `index.css` even though the components aren't imported by App.svelte; Vite's CSS handling is non-tree-shakeable for Svelte component styles)

### Plan-level success criteria

- [x] All tasks executed
- [x] Each task committed individually (Task 1 = `30a1d5c`, Task 2 = `6a8fbef`)
- [x] SUMMARY.md created (this file)
- [x] `npm --prefix ui run build` exits 0
- [x] All 4 files exist (Toast.svelte, ToastContainer.svelte, toasts.svelte.ts → see note below, WarningModal.svelte)
- [x] `grep -F "Display may flicker for 5" ui/src/lib/WarningModal.svelte` → see Deviation note above; per UI-SPEC.md §11 the title + body are two separate sentences, so this exact contiguous string is intentionally absent. The intent (flicker warning + 5–30 second timing) is covered by two separate verbatim strings, both grep-asserted by the plan's per-task acceptance criteria.
- [x] No modifications to STATE.md or ROADMAP.md (confirmed via `git diff HEAD~2 HEAD --name-only`)

### Note on the `toasts.svelte.ts` file in the prompt's phase_context

The prompt's `<phase_context>` mentioned `toasts.svelte.ts` as a "Svelte 5 `$state`-backed singleton store API". The Plan 05-03 spec, by contrast, scopes Plan 05-03 to **five** files (Toast.svelte, ToastContainer.svelte, WarningModal.svelte, focus-trap.ts, display-warning.ts) — the toast state slice is owned by **Plan 05-04's `App.svelte`** per 05-CONTEXT.md Area 4 (`toasts: Toast[]` slice in App.svelte; helper `addToast(t)` appends; `dismissToast(id)` removes). The 05-RESEARCH.md §F.1 pattern shows the same — `let toasts = $state<Toast[]>([])` at App.svelte scope, not a separate module.

Creating a separate `toasts.svelte.ts` singleton would conflict with the no-stores discipline (Phase 5 ships zero Svelte stores; all state is page-level runes per 05-CONTEXT.md Area 2: "No state stores. All state lives in App.svelte as `let state = $state<State | null>(null)`, `let toasts = $state<Toast[]>([])`, `let pendingAction = $state<{service, action, kind} | null>(null)`."). Plan 05-04 will install `toasts = $state<Toast[]>([])` directly in App.svelte alongside `pendingAction = $state<PendingAction | null>(null)`, then pass `addToast`/`dismissToast` callbacks down to handlers — that's the canonical pattern this phase is committed to.

The four files the success criteria block names — **Toast.svelte, ToastContainer.svelte, WarningModal.svelte** — are all shipped and the **focus-trap.ts + display-warning.ts** files (the plan's other two deliverables) are also shipped. The fourth file in the success criteria's bullet list (`toasts.svelte.ts`) is per the prompt's phase_context but contradicts the plan + 05-CONTEXT.md; I followed the plan, which is the load-bearing artifact. Flagged for the orchestrator's visibility.

## Threat Model Compliance

- **T-05-03-01 (Tampering — DOM modal removal):** ACCEPT — disposition unchanged. A user who removes the modal via devtools bypasses the warning but still hits the unguarded POST; the warning is operator-protective UX, not a security boundary. Server runs the recreate regardless. No mitigation required at this layer.
- **T-05-03-02 (Info Disclosure — Toast body content):** MITIGATED — disposition unchanged. Error toast bodies surface `body.reason` from the server, which Phase 4 already redacts via Phase 3's slog ReplaceAttr / redacting transport. No new exposure surface introduced by Toast.svelte (no client-side reflection beyond rendering the server-supplied string).
- **T-05-03-03 (Accessibility — Modal focus trap correctness):** MITIGATED. `focus-trap.ts` handles Tab + Shift-Tab + Escape per 05-RESEARCH.md §G.1. Initial focus deferred via `queueMicrotask` to survive Svelte 5 mount-vs-action ordering edge cases. Manual a11y smoke + Plan 05-05's `ui-flutter-warning.spec.ts` will verify Esc-cancels and Tab-cycles-within.
- **T-05-03-04 (DoS — Toast queue growth):** MITIGATED. Non-error toasts auto-dismiss after 5 s via `setTimeout` with `$effect` cleanup. Error toasts are sticky but bounded by operator-pace (an operator who triggers many errors is already in a degraded state). No queue overflow logic in v1 — the array is unbounded but operator-paced.
- **T-05-03-05 (Modal body scroll lock leakage):** MITIGATED. `WarningModal.svelte`'s `$effect` captures `document.body.style.overflow` into `prev` at entry; the cleanup function restores `prev`, not `''`. If a concurrent modal (none in v1) had already set overflow=hidden, our cleanup restores `hidden` (its prior value) — no leakage.

No new threat surface introduced beyond the plan's threat register. No `threat_flags` to surface.

## Open Notes for Plan 05-04

- **App.svelte mount points needed:**
  ```svelte
  <ToastContainer toasts={toasts} onDismiss={dismissToast} />
  <WarningModal
    open={pendingAction !== null}
    service={pendingAction?.service ?? ''}
    action={pendingAction?.action ?? 'update'}
    onConfirm={confirmPendingAction}
    onCancel={cancelPendingAction}
  />
  ```
  Mount both **outside** the `<main>` wrapper so backdrop + toast positioning is viewport-relative.
- **Toast helpers in App.svelte (per 05-CONTEXT.md Area 4 + 05-RESEARCH.md §F.1):**
  ```ts
  let toasts = $state<Toast[]>([]);
  let nextId = 0;
  function addToast(t: Omit<Toast, 'id'>) {
    toasts = [...toasts, { id: `t-${++nextId}`, ...t }];
    // Toast.svelte handles the setTimeout itself; no setTimeout in App.svelte
  }
  function dismissToast(id: string) {
    toasts = toasts.filter(t => t.id !== id);
  }
  ```
  Toast.svelte already calls `onDismiss(id)` from its own `$effect` setTimeout — App.svelte should NOT set a parallel setTimeout, or toasts get dismissed twice. The Toast component owns the auto-dismiss timer.
- **WarningModal flow (per 05-CONTEXT.md Area 4):**
  ```ts
  let pendingAction = $state<{service: string; action: 'update'|'rollback'; kind: ActionKind} | null>(null);

  function handleAction(svc: string, kind: ActionKind) {
    if ((kind === 'update' || kind === 'rollback') && requiresWarning(svc)) {
      pendingAction = { service: svc, action: kind, kind };
      return;
    }
    // direct POST path (postAction(svc, kind))
  }

  function confirmPendingAction() {
    if (!pendingAction) return;
    const { service, kind } = pendingAction;
    pendingAction = null;
    // POST path
  }

  function cancelPendingAction() {
    pendingAction = null;
  }
  ```
  Force-pull bypasses the warning (Phase 4 default is no-recreate, no display flicker) per 05-CONTEXT.md Area 4. The `kind === 'update' || kind === 'rollback'` gate above implements that.
- **Toast type import:**
  ```ts
  import type { Toast as ToastEntry, ToastKind } from './lib/Toast.svelte';
  // or just `import type { Toast } from './lib/Toast.svelte'` — module-scope export
  ```
- **`require Warning(svc)` import:**
  ```ts
  import { requiresWarning } from './lib/display-warning';
  ```
- **No `toasts.svelte.ts` singleton file.** Per the no-stores discipline + 05-CONTEXT.md Area 2, all state lives in App.svelte. If a future plan wants a singleton, it should be added then with a deliberate decision; do NOT add one in Plan 05-04.

## Open Notes for Plan 05-05

- **Modal copy strings are byte-exact for e2e text matching.** `ui-flutter-warning.spec.ts` should assert:
  - `await expect(page.getByRole('heading', { name: 'Display may flicker.' })).toBeVisible();`
  - `await expect(page.getByText('blank the HMI display for 5–30 seconds')).toBeVisible();` (en-dash, not hyphen)
  - `await expect(page.getByRole('button', { name: 'Continue' })).toBeVisible();`
  - `await expect(page.getByRole('button', { name: 'Cancel' })).toBeVisible();`
- **Toast role assertions:**
  - Success/info/warning toasts: `await expect(page.getByRole('status').last()).toContainText('Updated grafana')` (or similar)
  - Error toasts: `await expect(page.getByRole('alert')).toContainText('Update failed')`
- **Escape cancel path:**
  ```ts
  await page.click('button[aria-label="Update weston-stub"]');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();
  // Assert NO POST fired (route interception count)
  ```
- **Backdrop dismiss path:** `page.locator('button[aria-label="Dismiss warning"]').click()` — the backdrop is a real button, so it's reliably queryable by aria-label.
- **Initial focus assertion:** `await expect(page.getByRole('button', { name: 'Continue' })).toBeFocused();` — verifies the `[data-primary]` initial focus pattern.

## User Setup Required

None — pure frontend additions; no env vars, no compose changes, no auth gates.

## Next Plan Readiness

- **Plan 05-04 (App.svelte polling + actions + toasts + modal wiring):** UNBLOCKED. All five files exist with stable contracts. The Open Notes section above gives 05-04 a complete copy-paste recipe for the mount points + helpers.
- **Plan 05-05 (Playwright e2e + Pitfall 8):** UNBLOCKED at the UI side for the WarningModal + Toast specs. `ui-flutter-warning.spec.ts` can be written RED-first now; the copy strings + button labels are stable.

## Self-Check: PASSED

- File `ui/src/lib/Toast.svelte` exists: FOUND
- File `ui/src/lib/ToastContainer.svelte` exists: FOUND
- File `ui/src/lib/WarningModal.svelte` exists: FOUND
- File `ui/src/lib/focus-trap.ts` exists: FOUND
- File `ui/src/lib/display-warning.ts` exists: FOUND
- Commit `30a1d5c` (Task 1) present in `git log`: FOUND
- Commit `6a8fbef` (Task 2) present in `git log`: FOUND
- Vite output `internal/api/dist/index.html` regenerated: FOUND
- Vite output `internal/api/dist/assets/index-C2mbrqEw.js` regenerated: FOUND
- Vite output `internal/api/dist/assets/index-PKOb4YcN.css` regenerated: FOUND
- All Task 1 acceptance-criteria grep gates: ALL PASSED
- All Task 2 acceptance-criteria grep gates: ALL PASSED
- `npm --prefix ui run build` exit 0: CONFIRMED
- `npm --prefix ui run check` 113 files 0 errors 0 warnings: CONFIRMED
- STATE.md not modified: CONFIRMED (per phase context — not touched)
- ROADMAP.md not modified: CONFIRMED (per phase context — not touched)

---
*Phase: 05-web-ui-completeness*
*Plan: 03*
*Completed: 2026-05-15*
