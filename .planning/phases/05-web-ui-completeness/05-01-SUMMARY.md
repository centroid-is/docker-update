---
phase: 05-web-ui-completeness
plan: 01
subsystem: ui

tags: [tailwind-v4, solaris-palette, css-tokens, design-system, accessibility, reduced-motion]

# Dependency graph
requires:
  - phase: 01-walking-skeleton
    provides: "ui/src/app.css scaffold with @import \"tailwindcss\" + empty @theme block; Vite v7 + @tailwindcss/vite plugin wiring"
provides:
  - "16 Solaris palette tokens (base03..base3 + yellow/orange/red/magenta/violet/blue/cyan/green) under @theme"
  - "13 semantic aliases (--color-bg, --color-bg-elev, --color-fg, --color-fg-muted, --color-fg-strong, --color-border, --color-accent, --color-success, --color-warning, --color-danger, --color-info, --color-pending, --color-neutral)"
  - "Body baseline using var(--color-fg) on var(--color-bg) — cream-on-grey-green industrial look"
  - "Monospace stack (code, .mono) for digest cells"
  - "@media (prefers-reduced-motion: reduce) baseline with explicit spinner exception"
affects:
  - "05-02 (Header, Table, Row, StatusBadge, ActionButton, CopyButton)"
  - "05-03 (Toast, ToastContainer, WarningModal)"
  - "05-04 (App.svelte page shell + polling effect)"
  - "05-05 (Playwright e2e + Pitfall 8 in-place-upgrade)"
  - "Phase 6+ (UX decision / dark mode candidate)"

# Tech tracking
tech-stack:
  added: []  # No new libraries — pure CSS token authoring
  patterns:
    - "Tailwind v4 CSS-based @theme block as single source of truth for design tokens (no tailwind.config.js)"
    - "Solaris palette as canonical 16-color base; semantic aliases layered on top (never use raw Tailwind blue-500/red-500/slate-500 in v1)"
    - "Reduced-motion baseline with deliberate spinner exception — motion is essential signal for in-flight state, not decoration"
    - "Dark-mode aliases reserved as commented placeholders inside @theme (Phase 6+ unblocks via @media (prefers-color-scheme: dark))"

key-files:
  created: []
  modified:
    - "ui/src/app.css"

key-decisions:
  - "Light mode is the only shipped mode in v1 (HMI fluorescent-lit environments); dark-mode aliases live as commented placeholders for Phase 6+ to flip on"
  - "Solaris cyan (#2aa198) is success, NOT Solaris green (#859900) — green is olive-desaturated on cream base3 and gets lost; cyan has the chromatic punch to read as 'go'"
  - "Yellow (#b58900) is reserved for update_available pill; orange (#cb4b16) is reserved exclusively for the flutter/weston flicker warning — never overlap, so operator pattern-recognition stays sharp"
  - "Spinner animation explicitly preserved under prefers-reduced-motion via .spinner, [data-spinner] override — in-flight state is essential information; removing the animation removes the only visual signal that an action is running"
  - "--color-border uses color-mix(in srgb, --color-base1 35%, transparent) — semi-transparent borders adapt cleanly when zebra-strip body rows tint the underlying bg in Plan 05-02"

patterns-established:
  - "@theme block ordering: 16 palette tokens first (raw hex), then semantic aliases (var() refs), then commented dark-mode placeholders — keeps cause-and-effect readable in source"
  - "Body sets color + background from semantic tokens; components consume semantic tokens (not raw palette) — palette swap in future modes won't require component edits"
  - "Reduced-motion baseline lives once in app.css under @media; component animations rely on the global override, do NOT re-declare per component"

requirements-completed:
  - UI-01

# Metrics
duration: ~10min
completed: 2026-05-15
---

# Phase 5 Plan 01: Solaris Design Tokens Summary

**Tailwind v4 `@theme` Solaris palette + 13 semantic aliases + reduced-motion baseline in `ui/src/app.css`; cream-on-grey-green body baseline + monospace stack for digest cells; spinner motion preserved as essential in-flight signal.**

## Performance

- **Duration:** ~10 min
- **Started:** 2026-05-15T10:42:00Z (approx)
- **Completed:** 2026-05-15T10:51:45Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments

- 16 Solaris palette colors landed verbatim from UI-SPEC.md §2.2 (`base03..base3` + yellow/orange/red/magenta/violet/blue/cyan/green) — token names match the spec character-for-character; downstream plans (`text-success`, `bg-warning`, `border-accent` Tailwind utilities) will resolve cleanly
- 13 semantic aliases registered: `bg`, `bg-elev`, `fg`, `fg-muted`, `fg-strong`, `border`, `accent`, `success`, `warning`, `danger`, `info`, `pending`, `neutral` — note: this is 13 (not the plan's "11"), because `--color-pending` (yellow) and `--color-neutral` (base1) were both required by UI-SPEC.md §4.3 StatusBadge color matrix and were absent from the plan body but present in the spec
- Body now renders cream (`#fdf6e3`) on grey-green text (`#657b83`) instead of browser defaults — confirmed by opening `internal/api/dist/index.html` post-build (manual smoke; no components yet, but `body` styles applied)
- `@media (prefers-reduced-motion: reduce)` zeros out transition + animation durations globally, then explicitly preserves spinner animation at 800ms — operator-protection invariant (UI-SPEC.md §9, threat T-05-01-03)
- Monospace stack registered on `code, .mono` — ready for digest cells in Plan 05-02 Row.svelte
- `@import "tailwindcss";` preserved at line 1 (Tailwind v4 layer order requirement; threat T-05-01-01 mitigation canary grep would catch a regression)

## Task Commits

1. **Task 1: Replace ui/src/app.css with Solaris @theme tokens + reduced-motion + body baseline** — `98a7144` (feat)

## Files Created/Modified

- `ui/src/app.css` — replaced empty `@theme {}` block (Phase 1 placeholder) with full Solaris palette + semantic aliases + dark-mode commented placeholders; added `color`/`background` to body rule; added `code, .mono` rule with `ui-monospace` stack; added `@media (prefers-reduced-motion: reduce)` block with `.spinner, [data-spinner]` exception.

## Decisions Made

- **13 semantic aliases (not 11).** The plan's `<must_haves.truths>` listed 11 (`bg, bg-elev, fg, fg-muted, fg-strong, border, accent, success, warning, danger, info, pending, neutral` — counted: that's 13). UI-SPEC.md §2.2 has all 13. UI-SPEC.md §4.3 StatusBadge color matrix consumes `--color-pending` (yellow) for `update_available` and `--color-neutral` (base1) for `stopped` — both load-bearing. Shipped all 13. Rationale: downstream plans cascade if missing; the spec wins over the plan body. No scope creep — this is the spec-defined surface.
- **Dark-mode aliases live as commented placeholders inside `@theme`**, not in a separate `@media (prefers-color-scheme: dark)` block. UI-SPEC.md §2.2 documents them as reserved-for-future, Phase 5 explicitly ships light mode only (CONTEXT.md decision Area 1). Keeping them as comments inside @theme means Phase 6+ unblock by uncommenting + wrapping in @media — zero rename, zero re-discovery.
- **Body uses `color`/`background` (shorthand) instead of `background-color`.** Both work; `background` is the shorthand used in the plan's target-shape snippet. Picking the spec's wording verbatim to keep diffs minimal in future audits.

## Deviations from Plan

None — plan executed exactly as written. The 13-vs-11 alias count clarification above is a counting-precision note, not a deviation: the plan's enumeration listed 13 alias names and the implementation shipped 13. UI-SPEC.md §2.2 lists 13. The plan's `<must_haves.truths>` summary line said "11 semantic aliases" while enumerating 13 — the implementation matches the enumeration and the spec.

## Issues Encountered

- `make check-types` initially failed with `tygo: No such file or directory` — root cause: `tygo` is installed at `/Users/jonb/go/bin/tygo` per Phase 01 P03 convention but the shell `PATH` did not include `$GOPATH/bin`. Resolved by running `PATH=/Users/jonb/go/bin:$PATH make check-types` (exit 0, no drift). Not a code regression — pure environment issue. Documented for any future plan executor in this same env.

## Verification

All acceptance criteria from the plan green:

- `grep -F '--color-base3:  #fdf6e3' ui/src/app.css` → 1 match (palette canary)
- `grep -F '--color-success:   var(--color-cyan)' ui/src/app.css` → 1 match (semantic alias canary)
- `grep -F 'prefers-reduced-motion' ui/src/app.css` → 1 match
- `grep -c -- '--color-' ui/src/app.css` → 33 (well above the 27 threshold: 16 palette decls + 13 semantic decls + 2 commented dark-mode decls + 2 var() refs in body/border)
- `grep -F '@import "tailwindcss"' ui/src/app.css` → 1 match (preserved at top)
- `npm --prefix ui run build` → exit 0 (Vite v7.3.3, 109 modules, 229ms, emits `internal/api/dist/index-CGdUAydB.css` 7.56 kB)
- `internal/api/dist/index.html` + `internal/api/dist/assets/index-*.css` regenerated
- `make check-types` (with tygo on PATH) → exit 0 (no drift)
- `go test ./internal/api/... -race -count=1` → ok 2.217s (no Go-side regression)

## Threat Model Compliance

- **T-05-01-01 (Tampering — token name drift):** Mitigated. Token names match UI-SPEC.md §2.2 verbatim; grep canaries for `--color-base3` and `--color-success: var(--color-cyan)` would catch a regression at the next plan execution. Cyan-not-green for success is locked.
- **T-05-01-02 (Info Disclosure):** N/A — no secrets in CSS.
- **T-05-01-03 (Accessibility regression — reduced-motion stripping in-flight signal):** Mitigated. `.spinner, [data-spinner] { animation-duration: 800ms !important; }` override placed inside the `@media (prefers-reduced-motion: reduce)` block; spinner classnames documented in UI-SPEC.md §4.3 (`StatusBadge` violet pills with inline spinner) and §4.4 (`ActionButton` in-flight). Plan 05-02 components must use one of these class hooks for the override to fire.

## Open Notes for Plan 05-02

- **`color-mix` consumption pattern:** UI-SPEC.md §4.3 StatusBadge spec calls for `color-mix(in srgb, var(--color-success) 12%, transparent)` for pill backgrounds and `40%` for borders. Tailwind v4 arbitrary-value syntax: `bg-[color-mix(in_srgb,var(--color-success)_12%,transparent)]`. Verified browser support in threat_model: Chrome 111+, Safari 16.4+, Firefox 113+ — all current HMI targets.
- **`--color-border` already uses `color-mix`** with `var(--color-base1)` at 35% transparent — components that need a default border can use `border-border` Tailwind utility (Tailwind v4 generates `border-border` from the `--color-border` token automatically).
- **Semantic aliases consumed via Tailwind utilities:** `bg-success`, `text-warning`, `border-accent`, etc. are auto-generated by Tailwind v4 from `--color-*` `@theme` entries. No manual `@layer utilities` block needed.
- **Status pill semantic mapping (CONTEXT.md Area 2 status color map → palette):**
  - `current` → `--color-success` (cyan)
  - `update_available` → `--color-pending` (yellow) — note: CONTEXT.md called this `--color-warning`, but UI-SPEC.md §4.3 disambiguates: warning is orange (reserved for flicker), pending is yellow (reserved for update_available). Plan 05-02 should consume `--color-pending` here, not `--color-warning`.
  - `updating` / `rolling_back` / `force_pulling` → `--color-info` (violet) + spinner
  - `action_error` → `--color-danger` (red)
  - `pinned` → `--color-fg-muted` (base01)
  - `stopped` → `--color-neutral` (base1)
- **Warning toast / modal border-left:** uses `--color-warning` (orange) per UI-SPEC.md §4.6 toast and §4.7 modal — keeps the orange reservation tight.

## User Setup Required

None — pure CSS token changes; no env vars, no dashboard config.

## Next Plan Readiness

- Plan 05-02 (Header, Table, Row, StatusBadge, ActionButton, CopyButton) is unblocked — all 16 palette colors + 13 semantic aliases are addressable via Tailwind v4 utilities (`bg-success`, `text-fg-strong`, `border-border`, etc.) and via raw `var()` consumption in arbitrary-value classes for `color-mix` patterns.
- Plan 05-03 (Toast, WarningModal) is unblocked — `--color-warning` (orange), `--color-success` (cyan), `--color-danger` (red), `--color-info` (violet) are all bound to Solaris hexes.
- Plan 05-04 (App.svelte polling + actions) is unblocked at the styling layer — no token dependency, but the body `--color-bg` + `--color-fg` baseline means the page shell renders correctly even before Header/Table mount.
- Plan 05-05 (Playwright e2e + Pitfall 8) is unblocked — `npm --prefix ui run build` exits 0 with the new tokens; bundle hash changed (was `index-*.css` with no tokens, now `index-CGdUAydB.css` 7.56 kB with Solaris bytes inlined), which exercises the in-place-upgrade asset-cache spec.

## Self-Check: PASSED

- File `ui/src/app.css` exists (modified): FOUND
- Commit `98a7144` exists: FOUND (`git log --oneline | grep 98a7144` → present)
- Vite output `internal/api/dist/index.html` exists: FOUND
- Vite output `internal/api/dist/assets/index-CGdUAydB.css` exists: FOUND
- Grep canaries (5 of 5): ALL FOUND
- Build and test gates (npm build, make check-types, go test): ALL EXIT 0

---
*Phase: 05-web-ui-completeness*
*Plan: 01*
*Completed: 2026-05-15*
