---
phase: 01-walking-skeleton-test-harness
plan: 03
subsystem: ui
tags: [svelte5, vite7, tailwindcss-v4, tygo, go-typescript-codegen, embed]

# Dependency graph
requires:
  - phase: 01-walking-skeleton-test-harness
    provides: "internal/api package stub + internal/api/dist/.gitkeep placeholder from plan 01-01; internal/state/Container json-tag shape from plan 01-02 (parallel wave)"
provides:
  - "tygo Go→TS codegen pipeline (make types + make check-types) with committed ui/src/lib/types.d.ts as the drift detector"
  - "Svelte 5 + Vite 7 + Tailwind v4 shell scaffolded in ui/ — App.svelte + lib/Table.svelte"
  - "Vite build pipeline emitting bundle to internal/api/dist/ (NOT ui/dist/) so plan 04's //go:embed all:dist resolves to real assets"
  - "Seven-column header table with locked empty-state copy per UI-SPEC; ready for plan 04's smoke-test grep assertions"
  - "internal/api/types.go expanded from stub to final Container/State shape with json tags mirroring internal/state/schema.go"
affects: [01-04, 05-ui, 08-ci]

# Tech tracking
tech-stack:
  added:
    - "tygo v0.2.21 (Go→TS codegen, installed via go install)"
    - "svelte ^5.55 (runes API: $state, $props, mount)"
    - "vite ^7"
    - "@sveltejs/vite-plugin-svelte ^6"
    - "tailwindcss ^4.3 (config-in-CSS via @import + @theme)"
    - "@tailwindcss/vite ^4.3"
    - "@tsconfig/svelte ^5.0.4"
    - "svelte-check ^4"
    - "typescript ^5.6"
  patterns:
    - "Go types as the single source of truth; tygo regenerates TS; CI fail-on-diff via git diff --exit-code"
    - "Vite build.outDir override to bypass default ui/dist/ (Pitfall C prevention)"
    - "Tailwind v4 config-in-CSS — no tailwind.config.js (v3 pattern dropped)"
    - "Svelte 5 runes API: $state<T>(initial) + $props() destructure"
    - "Makefile incremental population — plan 03 ships types/check-types only; plan 04 adds build/ui/test/e2e/image/clean"

key-files:
  created:
    - "tygo.yaml"
    - "Makefile"
    - "ui/package.json"
    - "ui/package-lock.json"
    - "ui/vite.config.ts"
    - "ui/svelte.config.js"
    - "ui/tsconfig.json"
    - "ui/index.html"
    - "ui/.gitignore"
    - "ui/src/main.ts"
    - "ui/src/app.css"
    - "ui/src/App.svelte"
    - "ui/src/lib/Table.svelte"
    - "ui/src/lib/types.d.ts (tygo-generated, committed)"
  modified:
    - "internal/api/types.go (expanded from plan-01 stub to full Container/State shape)"

key-decisions:
  - "types.go uses omitempty on Image/Tag to mirror state/schema.go verbatim (json-tag parity is the load-bearing invariant; plan's verbatim sample without omitempty was reconciled toward state.go since state.go is already on-disk and tested) — Rule 1"
  - "tygo installed via `go install` (dev tool), NOT a go.mod dependency — matches CI workflow expectation"
  - "Accepted Vite's emptyOutDir wiping internal/api/dist/.gitkeep — plan-anticipated behavior; future `make ui` always seeds the directory before any `go build` runs"
  - "$state<Container[]>([]) used in App.svelte instead of RESEARCH.md's conditional-type extract — equivalent behavior, more readable per plan note"

patterns-established:
  - "Go→TS codegen via tygo + Makefile + git diff --exit-code drift check"
  - "Vite outDir override for embedded SPA bundles"
  - "Svelte 5 runes-API component pattern: Props type + $props() destructure"
  - "Tailwind v4 entry CSS: @import + @theme block"

requirements-completed:
  - FOUND-04
  - FOUND-08

# Metrics
duration: 5min
completed: 2026-05-13
---

# Phase 01 Plan 03: Svelte 5 + Vite 7 + Tailwind v4 shell + tygo Go→TS pipeline Summary

**End-to-end Svelte UI build pipeline emitting to internal/api/dist/ (for plan 04's go:embed) plus tygo-driven Go→TS type codegen with `make check-types` drift detector**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-05-13T13:01:33Z
- **Completed:** 2026-05-13T13:06:43Z
- **Tasks:** 2 / 2
- **Files created:** 14 (1 Go + 1 yaml + 1 Makefile + 11 ui/* files)
- **Files modified:** 1 (internal/api/types.go expanded)
- **Files deleted:** 1 (internal/api/dist/.gitkeep — eaten by Vite's emptyOutDir per plan)

## Accomplishments

- tygo end-to-end pipeline: `go install` → `tygo.yaml` → `make types` regenerates `ui/src/lib/types.d.ts`; committed file is the drift baseline that `make check-types` compares against (FOUND-08)
- Svelte 5 + Vite 7 + Tailwind v4 shell builds end-to-end; output lands in `internal/api/dist/` ready for plan 04's `//go:embed all:dist` (FOUND-04)
- Seven-column header table (`container`, `image:tag`, `current digest`, `available digest`, `previous digest`, `status`, `actions`) renders with the locked empty-state copy ("No watched containers yet" + `hmi-update.watch=true` body) per UI-SPEC verbatim
- App.svelte imports `State` and `Container` from `./lib/types` — proves the tygo codegen loop end-to-end (UI-SPEC explicit requirement)
- Generated TS contains the optional/required field distinction: `image?: string`, `tag?: string`, `current_digest?: string`, `previous_digest?: string` per their Go `omitempty` tags

## Task Commits

Each task was committed atomically:

1. **Task 1: tygo pipeline (Go types → TS types) + Makefile types/check-types targets** — `4558e25` (chore)
2. **Task 2: Svelte 5 + Vite 7 + Tailwind v4 scaffold; emit bundle into internal/api/dist/** — `4e08291` (feat)

**Plan metadata commit:** (this SUMMARY + STATE/ROADMAP/REQUIREMENTS bumps, follows below)

## Files Created/Modified

### Created
- `tygo.yaml` — tygo config: maps `internal/api` → `ui/src/lib/types.d.ts`, honors json tags, adds the "do not edit by hand" frontmatter
- `Makefile` — `.PHONY: types check-types`; tab-indented command lines verified
- `ui/package.json` — locked devDependencies per STACK.md
- `ui/package-lock.json` — committed so CI's `npm ci` is reproducible (supply-chain mitigation per threat T-01-03-04)
- `ui/vite.config.ts` — `build.outDir: '../internal/api/dist'`, `emptyOutDir: true`
- `ui/svelte.config.js` — vitePreprocess wiring
- `ui/tsconfig.json` — extends `@tsconfig/svelte`; strict + isolatedModules + bundler resolution
- `ui/index.html` — Vite entry, mounts `#app`
- `ui/.gitignore` — node_modules/, dist/, .DS_Store
- `ui/src/main.ts` — Svelte 5 `mount(App, ...)` entry
- `ui/src/app.css` — Tailwind v4 `@import "tailwindcss"` + `@theme {}` + body font-family
- `ui/src/App.svelte` — page shell with header bar + Table; fetches `/api/state` onMount
- `ui/src/lib/Table.svelte` — 7-column header table + colspan="7" empty-state row + per-container row stubs for Phase 5
- `ui/src/lib/types.d.ts` — tygo-generated, committed for drift detection

### Modified
- `internal/api/types.go` — expanded from plan-01's empty `type Container struct{}` stub to the full six-field json-tagged shape mirroring `internal/state/schema.go`

### Deleted
- `internal/api/dist/.gitkeep` — Vite's `emptyOutDir: true` removes it on each build; no longer needed now that `npm run build` populates the directory. Plan explicitly anticipated this.

## Decisions Made

- **Mirror state/schema.go field tags verbatim** (including `omitempty` on Image/Tag) rather than the plan's verbatim Go sample (which omitted `omitempty` on those two). State.go is the on-disk reality and is already test-locked by plan 01-02's RED tests; aligning types.go to it preserves wire/disk schema parity. Plan 04 will reconcile by aliasing or re-using one in terms of the other — both shapes are now bit-identical for tags.
- **tygo via `go install`, not a go.mod dependency.** Plan-recommended; matches how CI's workflow installs it. Avoids polluting go.mod with a dev-only tool.
- **Accepted the `.gitkeep` deletion.** Vite's `emptyOutDir: true` removes it. Plan explicitly anticipated this ("Vite ate it") and the root .gitignore correctly ignores all dist contents on a fresh clone — `make ui` always populates the directory before any `go build` runs.
- **`$state<Container[]>([])` over the RESEARCH.md conditional-type extract.** Plan body explicitly endorsed this simplification — equivalent behavior, more readable.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 — Wire/Disk Schema Alignment] internal/api/types.go field tags adjusted to match internal/state/schema.go verbatim**
- **Found during:** Task 1 (read of state/schema.go before generating types.d.ts)
- **Issue:** Plan's verbatim Go sample for `Container.Image` and `Container.Tag` used `json:"image"` / `json:"tag"` (no `omitempty`), but state/schema.go uses `json:"image,omitempty"` / `json:"tag,omitempty"`. If both shipped as written, the same Container value persisted by the store would serialize with the field present-and-empty while the wire payload (if it ever defined its own) would emit empty strings. The plan body itself says "Field names and json tags must match internal/state/Container" — so state.go is the canonical source.
- **Fix:** Added `,omitempty` to Image and Tag in `internal/api/types.go`. Generated TS now exposes `image?: string` and `tag?: string` (optional) consistent with the Go runtime behavior.
- **Files modified:** `internal/api/types.go`
- **Verification:** `make check-types` passes; `ui/src/lib/types.d.ts` shows `image?: string` / `tag?: string` as optional fields.
- **Committed in:** `4558e25` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 — bug-class correctness fix to honor the plan's stated invariant over its sample code)
**Impact on plan:** Zero scope creep. The fix aligns the wire types with the on-disk types as the plan's prose required. Plan 04's reconciliation (alias / re-use) becomes a trivial line because the tags are now bit-identical.

## Issues Encountered

None.

## TDD Gate Compliance

N/A — this plan is `type: execute`, not `type: tdd`. No RED/GREEN/REFACTOR gates required.

## Known Stubs

None that prevent the plan's goal. `ui/src/lib/Table.svelte` has the per-container row body wired (renders service, image:tag, digests, status) using the tygo-generated types — Phase 5 may polish styling but the data path is complete. The `actions` `<td>` is intentionally empty (`<!-- Phase 5 -->`) per UI-SPEC's `DEFERRED TO PHASE 5` block (UI-01, UI-03, UI-09).

## Threat Surface Scan

No new threat surface beyond what the plan's threat model already covered. The Vite bundle is build-time-only; Svelte 5 auto-escapes interpolated values; no user-controlled HTML reaches the render path. T-01-03-01 (drift detection) is mitigated by the committed `types.d.ts` + `make check-types`. T-01-03-04 (npm supply chain) is mitigated by the committed `package-lock.json`.

## Self-Check Verification

**Files claimed created — existence check:**
- tygo.yaml — FOUND
- Makefile — FOUND
- internal/api/types.go — FOUND (modified)
- ui/package.json — FOUND
- ui/package-lock.json — FOUND
- ui/vite.config.ts — FOUND
- ui/svelte.config.js — FOUND
- ui/tsconfig.json — FOUND
- ui/index.html — FOUND
- ui/.gitignore — FOUND
- ui/src/main.ts — FOUND
- ui/src/app.css — FOUND
- ui/src/App.svelte — FOUND
- ui/src/lib/Table.svelte — FOUND
- ui/src/lib/types.d.ts — FOUND
- internal/api/dist/index.html — FOUND (Vite build output)
- internal/api/dist/assets/index-*.js — FOUND
- internal/api/dist/assets/index-*.css — FOUND

**Commits claimed — existence check:**
- 4558e25 — FOUND (`git log --oneline -3` confirms)
- 4e08291 — FOUND

## Self-Check: PASSED

All 14 created files, 1 modified file, 3 build-output paths, and 2 commit hashes claimed in this SUMMARY were verified present immediately after writing.

- `tygo.yaml`, `Makefile`, `internal/api/types.go` — FOUND
- All 11 `ui/*` files (package.json + package-lock.json + svelte/vite/ts config + index.html + .gitignore + 4 src files) — FOUND
- `ui/src/lib/types.d.ts` — FOUND (tygo-generated, committed)
- `internal/api/dist/index.html` + `assets/*.js` + `assets/*.css` — FOUND
- Commits `4558e25` (Task 1 — tygo pipeline) and `4e08291` (Task 2 — Svelte shell) — both FOUND in `git log --oneline --all`

## User Setup Required

None — no external service configuration required. `npm install` and `go install` are local toolchain operations.

## Next Phase Readiness

**Plan 04 (Wave 3) can now:**
- Run `//go:embed all:dist` from `internal/api/static.go` — `internal/api/dist/index.html` and `assets/*` exist
- Use `internal/api.State` and `internal/api.Container` (full shape, tag-compatible with `internal/state`)
- Use Make targets `types` and `check-types`; full Makefile (build/ui/test/e2e/image/clean) ships in plan 04
- Smoke-test against the 7-column header strings and the "No watched containers yet" empty-state copy — those are now in the served bundle's index.html-referenced JS chunk

**Concerns / open seams:**
- Plan 02 (parallel wave) has uncommitted `internal/state/persist.go` and `internal/state/store.go` in the working tree (read-only verified, not staged by this plan). The state plan's own SUMMARY/commit will land those.
- `internal/api/dist/` is now empty on a fresh `git clone` until `npm run build` runs. CI's workflow already runs `make ui` before `make build`; local devs should too. The `Makefile.PHONY: build` target in plan 04 must `depend on ui` or document the order.

---
*Phase: 01-walking-skeleton-test-harness*
*Completed: 2026-05-13*
