---
phase: 05-web-ui-completeness
plan: 04
subsystem: ui

tags: [svelte-5, runes, $state, $derived, $effect, fetch, polling, cache-no-store, action-error, toast, modal, in-place-upgrade]

# Dependency graph
requires:
  - phase: 05-web-ui-completeness
    plan: 01
    provides: "Solaris @theme tokens + prefers-reduced-motion baseline + app.css globals; the rewritten App.svelte renders inside this CSS root without touching tokens directly (component children read them)"
  - phase: 05-web-ui-completeness
    plan: 02
    provides: "Header.svelte (onRefresh + onWatchNow + lastPollEnd props), Table.svelte (containers + onAction + busyServices props), ActionButton.svelte ActionKind union, Row.svelte (consumes busyServices.has(c.service) → isBusy prop). All four components mount unchanged from Plan 05-04's App.svelte rewrite."
  - phase: 05-web-ui-completeness
    plan: 03
    provides: "Toast.svelte module-scoped ToastKind + Toast types, ToastContainer.svelte (toasts + onDismiss props), WarningModal.svelte (open + service + action + onConfirm + onCancel props), display-warning.ts requiresWarning() predicate. All consumed by Plan 05-04's App.svelte without re-declaration."
  - phase: 04-update-rollback-force-pull-actions-safety-state-persistence
    provides: "POST /api/containers/{service}/{update|rollback|force-pull} action endpoints with ACT-11 always-present `current_digest` + `previous_digest` on success; ACT-06 / ACT-07 idempotency `no_op: true` envelope; ACT-10 service-name allowlist regex (the server-side gate that encodeURIComponent defends in depth); 409 service_busy from ACT-08 per-service mutex; full error matrix documented in API.md"

provides:
  - "ui/src/lib/actions.ts: ActionKind union, ActionResult envelope, ActionError class, postAction(svc, kind) → Promise<ActionResult>, pollNow() → Promise<boolean>. Single module that touches /api/containers/*/{action} and /api/poll-now."
  - "ui/src/App.svelte (rewritten): Svelte 5 runes-driven brain — `appState`, `toasts`, `pendingAction`, `busyServices` $state slices; `isActing`, `containers`, `lastPollEnd` $derived; 5s setInterval poll with cache: 'no-store'; handleActionRequest → executeAction wiring with requiresWarning gate for flutter/weston Update/Rollback; ActionError catch with 409 service_busy → warning toast / else → error toast branches; pollNow() 404 fallback to plain poll() with info toast."
  - "Toast translation contract: no_op → info 'No change needed'; update/rollback success → success toast with 'sha256:' + first 8 hex chars of current_digest + ellipsis; force-pull success → info 'Re-pulled'; 409 service_busy → warning; other ActionError → error with verbatim server reason; network throw → generic 'Network error' info string."
  - "<noscript> fallback rendering a single short JS-required message (UI-SPEC.md §12)."

affects:
  - "05-05 (Playwright e2e specs assert the full happy + sad paths against the wired UI: ui-actions.spec.ts asserts the digest-prefix success toast; ui-flutter-warning.spec.ts asserts the WarningModal pre-action interception; ui-poll-now.spec.ts asserts the 404 graceful degradation; Plan 05-05 also hardens /assets/* + index.html cache headers server-side to close the in-place upgrade leg cache: 'no-store' alone cannot)"
  - "Phase 6+ (any new server endpoints surfacing in the UI should route through ui/src/lib/actions.ts to inherit the ActionError shape; new toast call sites should consume addToast(kind, title, body?) signature established here)"

# Tech tracking
tech-stack:
  added: []  # No new npm dependencies. actions.ts is a 147-LOC TS module with zero imports.
  patterns:
    - "Single-module action layer (ui/src/lib/actions.ts) — App.svelte never calls fetch on action endpoints directly; postAction is the only path. Mirrors the 'single registry layer' pattern from internal/registry on the backend."
    - "ActionError class extending Error with `status`, `code`, `reason` fields — typed structured error replaces stringly-typed status comparisons. Catch arm uses `e instanceof ActionError` + property access; no JSON re-parsing at the call site."
    - "Reassignment over mutation for $state collections (busyServices = new Set([...busyServices, svc]); toasts = [...toasts, entry]). Svelte 5 tracks both, but reassignment is the most legible pattern when you want 'new snapshot' semantics to be visible in the code review."
    - "$derived gate over a mutable state slice — isActing = $derived(busyServices.size > 0) — keeps the poll-pause condition reactively tied to the busy set without bookkeeping a parallel boolean."
    - "Optional endpoint graceful degradation via boolean return — pollNow() returns false on 404 / any non-2xx / network failure; caller fork is a single `if (!ok)` branch with an info toast. Reusable pattern for forward-compat features that may or may not be implemented server-side."
    - "Belt-and-braces input encoding — encodeURIComponent on the service path-tail in postAction even though the server's ACT-10 regex is the authoritative gate. Two-layer defense documented as T-05-04-08 mitigation."

key-files:
  created:
    - "ui/src/lib/actions.ts (147 LOC) — postAction + pollNow + ActionError"
  modified:
    - "ui/src/App.svelte (rewritten end-to-end; 366 LOC, was 54) — polling, action wiring, toast + modal hosting"

key-decisions:
  - "Renamed the /api/state state slice from `state` to `appState`. svelte-check (TS5.6 + svelte-check 4.0) misreads `let state = $state<State | null>(null)` as a self-referential declaration on the rune-magic `$state` identifier and emits 8 cascading errors ('Block-scoped variable $state used before its declaration', plus 'Untyped function calls may not accept type arguments' on every other rune call in the file). Renaming the local to `appState` sidesteps the shadow without changing semantics. The Toast import is still named `Toast` because the module-scoped type does not collide with the rune. This is a known TS-parser limitation; a future svelte-check release may fix it (no GitHub issue filed yet — out of scope for this plan)."
  - "force-pull intentionally bypasses requiresWarning. Default force-pull mode does NOT recreate the container (API.md SAFE-03 carve-out, 'running container is unaffected — no compose call, no verify loop'), so the 'display may flicker' warning would be a lie. Update + Rollback ARE gated. ?recreate=true on force-pull is not a UI affordance in v1; if it lands later, the gate must extend."
  - "Toast translation key on HTTP status + error code, not on action kind alone. 409 service_busy → warning toast (operator-actionable: wait and retry); every other ActionError → error toast with verbatim server `reason`. Distinct from the success path which keys on `kind` to choose the verb ('Updated' / 'Rolled back' / 'Re-pulled'). The split is intentional: success messages are about what happened; failure messages are about why."
  - "Digest prefix display: 'sha256:' literal + first 8 hex chars after the colon + ellipsis. Matches the truncation form in the Row.svelte CopyButton tooltip; the operator reads the same prefix in two places and can sanity-check by eye. A naive `digest.slice(0, 16)` would slice mid-prefix on a non-sha256 digest; the `startsWith('sha256:')` guard short-circuits to the raw value if the algorithm ever changes."
  - "addToast is reassignment + spread (toasts = [...toasts, entry]) rather than toasts.push(entry). Svelte 5 tracks both forms, but reassignment is the most legible 'new snapshot' semantics in code review — the diff highlights the queue change verbatim. Same pattern in dismissToast (filter not splice) and in busyServices add/remove (Set spread not Set.add/delete)."
  - "Poll early-return on isActing keeps the interval ticking — the next interval tick re-checks isActing and proceeds if the action has finished. Alternative (clearInterval + recreate on action complete) would cost an extra setInterval per action and add a subtle bug surface around 'what if the user clicks twice quickly'. Early-return is the simplest correct shape."
  - "WarningModal.action prop is mapped via ternary: pendingAction?.kind === 'rollback' ? 'rollback' : 'update'. The modal's action type is a strict union 'update' | 'rollback' (force-pull never opens the modal). If pendingAction is null (initial paint + post-cancel), the ternary falls through to 'update' as a benign default — the modal's `open={pendingAction !== null}` gates the entire render so the action value is unobservable in that state."
  - "Network-error catch (the `else` branch of the ActionError instanceof check) emits a generic 'Could not reach hmi-update; check the LAN connection.' toast. The exact server `reason` is unavailable on a network throw; surfacing the JS exception message would be operator-hostile. A LAN-only deployment makes 'check the LAN connection' the right operator hint."
  - "<noscript> uses inline style (font-family: system-ui, color: #586e75 — Solaris base01 hex inlined) so it renders legibly even without app.css. The Vite build embeds app.css via the same <script type=module> entry that JS loads from, so a JS-disabled browser sees neither the JS nor the CSS — the inline style is the only thing standing."

patterns-established:
  - "Action endpoint module pattern (ui/src/lib/actions.ts) — typed errors via custom Error subclass with HTTP-status + code + reason fields; .catch(() => ({})) on body parse to tolerate malformed JSON; encodeURIComponent on user-named path segments even with a server-side regex gate. New endpoint surfaces in Phase 6+ should follow this shape."
  - "Polling $effect with isActing gate — the interval body early-returns while a mutating action is in flight, deferring the next poll to after the action's finally re-poll. Avoids the clearInterval + recreate dance and keeps the loop's lifecycle tied to the component's lifetime via the $effect cleanup."
  - "ActionError → toast translation table — App.svelte's executeAction catch branch is the canonical mapping point; new error codes from the backend land as new branches here, not at the call sites. Centralizes the operator-facing UX of failure across all per-row actions."
  - "Optional-endpoint helper returns boolean (pollNow → true/false) — the caller's degradation logic is a single `if (!ok)` branch with an info toast. Reusable for any future 'might-not-be-implemented' surface."
  - "Local-variable rename to avoid svelte-check rune-identifier shadow (state → appState) — when a $state slice naturally wants the same name as the rune, prefer a 1-letter prefix (`app`, `ui`, etc.) over a comment-only suppression."

requirements-completed:
  - UI-04  # Action wiring (POST update/rollback/force-pull through a typed module)
  - UI-05  # Toast surface wired (success/error/warning/info translation per response shape)
  - UI-06  # 5s poll loop with cache: 'no-store' and isActing pause
  - UI-07  # Watch-now button with /api/poll-now → degrade-to-poll() fallback
  - UI-08  # WarningModal pre-action interception for flutter/weston (Update + Rollback only)

# Metrics
duration: ~6min
completed: 2026-05-15
---

# Phase 5 Plan 04: Polling + Action Wiring Summary

**App.svelte is now the page brain — 5s `cache: 'no-store'` polling of `/api/state` paused while any action is in-flight, requiresWarning-gated POSTs through a typed `postAction` helper in `ui/src/lib/actions.ts`, ActionError → toast translation (409 service_busy → warning, else → error with verbatim server reason), success toasts with `sha256:` + 8-hex-char digest prefix, and `pollNow()` 404 fallback so the Watch-now button never goes dark even if `/api/poll-now` isn't implemented.**

## Performance

- **Duration:** ~6 min
- **Started:** 2026-05-15T11:15:43Z
- **Completed:** 2026-05-15T11:21:01Z
- **Tasks:** 2 (both atomic commits)
- **Files modified:** 2 (1 new — `actions.ts`; 1 rewritten — `App.svelte`)

## Accomplishments

- `ui/src/lib/actions.ts` — single module that talks to Phase 4 action endpoints and `/api/poll-now`. `ActionKind` union, `ActionResult` envelope, `ActionError` class with `status`/`code`/`reason`, `postAction(svc, kind)`, `pollNow()`. `encodeURIComponent` defense in depth (T-05-04-08).
- `ui/src/App.svelte` rewritten as a 366-LOC brain. Five `$state` slices (`appState`, `toasts`, `pendingAction`, `busyServices`, `nextToastId`); three `$derived` (`isActing`, `containers`, `lastPollEnd`); 5 s `setInterval(poll, 5000)` `$effect` with `clearInterval` cleanup; `cache: 'no-store'` on the poll fetch; `isActing` early-return gate; `handleActionRequest` → `executeAction` action pipeline with the `requiresWarning` flutter/weston gate; `ActionError` catch branch translating 409 `service_busy` → warning toast and everything else → error toast.
- WarningModal interception confirmed for Update + Rollback on flutter/weston (NOT for force-pull, which is exempt per the SAFE-03 carve-out — default force-pull doesn't recreate the container, so no display flicker, so the warning would be a lie).
- `pollNow()` 404 fallback verified — returning `false` on any non-2xx routes to a plain `poll()` + info toast so the operator never loses the Watch-now affordance.
- `<noscript>` fallback renders a single short JS-required message with inline styles (legible without `app.css`).

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement actions.ts (postAction + ActionError + pollNow)** — `ece18ef` (feat)
2. **Task 2: Rewrite App.svelte (polling, action wiring, toast/modal hosting)** — `ded0f17` (feat)

_Note: this plan is `type: execute` (not TDD), so each task has a single feat commit. Plan 05-05 will add Playwright specs that assert the full happy + sad paths against this wiring._

## Files Created/Modified

- `ui/src/lib/actions.ts` (new, 147 LOC) — `ActionKind` union, `ActionResult` envelope, `ActionError` class, `postAction(svc, kind): Promise<ActionResult>`, `pollNow(): Promise<boolean>`. Zero npm dependencies; pure `fetch` + TypeScript.
- `ui/src/App.svelte` (rewritten end-to-end, 54 → 366 LOC) — page brain. Imports Header, Table, ToastContainer, WarningModal, requiresWarning, postAction, pollNow, ActionError, Toast types, State + Container types. Five `$state` slices, three `$derived`, two toast helpers, one polling `$effect`, three Header callbacks (handleRefresh, handleWatchNow), four action functions (handleActionRequest, confirmAction, cancelAction, executeAction).

## Decisions Made

See `key-decisions` in the frontmatter for the full set. Highlights:

- **Renamed `state` → `appState`** to sidestep a svelte-check parser quirk that reads `let state = $state<...>(...)` as a self-referential declaration on the rune identifier and emits 8 cascading errors. Semantics unchanged. This is documented as a Rule 1 - Bug deviation below (caught + fixed inline during Task 2 verification).
- **force-pull bypasses `requiresWarning`** because the default mode doesn't recreate the container (SAFE-03 carve-out). The warning would be a lie. Only Update + Rollback are gated.
- **ActionError → toast translation keys on HTTP status + error code**, not action kind. 409 service_busy → warning (operator-actionable); other ActionError → error with verbatim server reason; network throw → generic 'Network error' string.
- **Digest prefix display:** `sha256:` literal + first 8 hex chars after the colon + ellipsis. Matches Row.svelte's CopyButton tooltip truncation — operator reads the same form in two places.
- **Reassignment over mutation for `$state` collections** (`toasts = [...toasts, entry]`, `busyServices = new Set([...busyServices, svc])`). Both are tracked by Svelte 5; reassignment is the more legible 'new snapshot' semantics in code review.
- **Poll early-return on isActing** keeps the interval ticking — the next 5 s tick re-checks. Simpler than clearInterval + recreate.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Renamed local `state` to `appState` to sidestep svelte-check rune-identifier shadow**

- **Found during:** Task 2 (App.svelte rewrite — `npm --prefix ui run check` step)
- **Issue:** svelte-check 4.0 + TS 5.6 reported 8 cascading errors starting with `"Block-scoped variable '$state' used before its declaration"` at the line `let state = $state<State | null>(null);`. The parser appears to confuse the local var `state` with the rune `$state`, producing self-reference diagnostics and follow-on "Untyped function calls may not accept type arguments" errors on every subsequent `$state<T>(...)` and `$derived.by<T>(...)` call.
- **Fix:** Renamed the local to `appState`. Three follow-on call-site updates: `state?.containers` → `appState?.containers` (in `containers` derivation), `state?.last_poll_end` → `appState?.last_poll_end` (in `lastPollEnd` derivation), and `state = s` → `appState = s` (in `poll()`). Added an inline comment block above the declaration documenting the parser quirk so a future maintainer doesn't "fix" the rename back.
- **Files modified:** `ui/src/App.svelte` only (no other files referenced the local).
- **Verification:** `npm --prefix ui run check` → 0 errors / 0 warnings after the rename; `npm --prefix ui run build` exits 0; all 13 grep acceptance criteria still green (the rename does not affect any of them — `state` was never in an acceptance grep pattern).
- **Committed in:** `ded0f17` (Task 2 commit — fix landed in the same commit as the rewrite since the rename touched only lines the rewrite was already writing).

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug).
**Impact on plan:** Cosmetic local-variable rename; no scope creep, no API surface change, no externally observable behavior change. The rune contract is unchanged — `appState` is a `$state<State | null>` exactly as the plan specified for `state`.

## Issues Encountered

- **`tygo` not on default PATH** for `make check-types`. Resolved by running `PATH="$(go env GOPATH)/bin:$PATH" make check-types`. The `tygo` binary is installed under `$GOPATH/bin` (alongside `crane`, `gobco`, `gopls`) per the repo's tooling discipline; not a regression. `internal/api/types.go` was not modified in this plan, so `types.d.ts` had no drift and check-types exit was 0.

## Verification

All plan-level success criteria green:

- `npm --prefix ui run build` exits 0 — 127 modules transformed, 57.12 kB JS bundle (up from 48.72 kB pre-plan; new code from `actions.ts`, `ToastContainer`, `Toast`, `WarningModal`, `focus-trap` now loaded by App.svelte).
- `npm --prefix ui run check` exits 0 — 0 errors / 0 warnings across 114 files.
- `make check-types` exits 0 — `types.d.ts` unchanged.
- `grep -F 'cache:' ui/src/App.svelte` → 2 matches (no-store on /api/state — required `≥1`).
- `grep -F 'busyServices' ui/src/App.svelte` → 10 matches (required `≥1`).
- `grep -F 'WarningModal' ui/src/App.svelte` → 1 match (component mount — required `≥1`).
- `grep -F 'setInterval(poll, 5000)' ui/src/App.svelte` → 1 match (Task 2 acceptance).
- `grep -F 'requiresWarning' ui/src/App.svelte` → 3 matches (Task 2 acceptance, required `≥1`).
- `grep -F 'pendingAction' ui/src/App.svelte` → 10 matches (Task 2 acceptance, required `≥1`).
- `grep -F 'addToast' ui/src/App.svelte` → 10 matches (Task 2 acceptance, required `≥2`).
- `grep -F 'ActionError' ui/src/App.svelte` → 4 matches (Task 2 acceptance, required `≥1`).
- `grep -F '<noscript' ui/src/App.svelte` → 2 matches (open + close).
- `grep -F '<Header' / '<Table' / '<ToastContainer' / '<WarningModal' ui/src/App.svelte` → 1 each.
- `grep -F 'pollNow' ui/src/App.svelte` → 3 matches (Task 2 acceptance, required `≥1`).
- `grep -F 'class ActionError' / 'export async function postAction' / 'encodeURIComponent' / "method: 'POST'" / 'pollNow' ui/src/lib/actions.ts` — all Task 1 acceptance criteria green.
- STATE.md and ROADMAP.md untouched (no orchestrator updates from executor; deliberate per plan instruction).

## Open Notes for Plan 05-05

- **e2e specs that will land in 05-05:**
  - `ui-actions.spec.ts` — assert the digest-prefix success toast text (`Updated svc-a` / `sha256:0bf3b7d8…`); assert busyServices flips the row spinner during the in-flight POST and clears after.
  - `ui-flutter-warning.spec.ts` — assert the modal opens on Update click for `flutter-app`; assert Continue fires the POST; assert Cancel does NOT fire any POST; assert force-pull on `flutter-app` does NOT open the modal.
  - `ui-poll-now.spec.ts` — assert that when `/api/poll-now` returns 404, the Watch-now button surfaces the `'Poll-now endpoint not available'` info toast AND triggers a plain `/api/state` GET. Assert that when it returns 2xx, no toast appears.
  - `ui-poll-pause.spec.ts` — assert that during an in-flight action, no `/api/state` GET fires for the 5 s window (the early-return path); assert it resumes on the next tick after the action completes.
  - `ui-error-toast.spec.ts` — assert that a 500 `compose_failed` response from the server produces an error toast with the verbatim server `reason`; assert that a 409 `service_busy` produces a warning toast (not error).
- **Server-side asset hardening (Plan 05-05 scope):** `cache: 'no-store'` on `/api/state` is the client-side leg of Pitfall 8. Plan 05-05 will add `Cache-Control: no-cache` to `index.html` and `Cache-Control: public, max-age=31536000, immutable` to `/assets/*` (Vite emits hashed filenames). Together with this plan's `cache: 'no-store'`, the in-place-upgrade stale-asset window closes.
- **action prop on WarningModal:** still mapped via ternary (`pendingAction?.kind === 'rollback' ? 'rollback' : 'update'`). UI-SPEC.md §11 currently uses identical copy for both, but if operators ask for "Update svc-a" vs "Rollback svc-a" in the Continue button tooltip, the wiring already routes the right value into the modal's `action` prop.
- **No new npm dependencies introduced.** `actions.ts` is pure TS + `fetch`; App.svelte uses only Svelte 5 runes + existing component imports. Phase 5 dependency budget unchanged from Plan 05-03.

## Self-Check: PASSED

- `[x] ui/src/lib/actions.ts` exists (verified `ls`)
- `[x] ui/src/App.svelte` modified (verified `git log -1 --stat HEAD`)
- `[x] Commit ece18ef` (Task 1) found in `git log --oneline` — `feat(05-04): add actions.ts...`
- `[x] Commit ded0f17` (Task 2) found in `git log --oneline` — `feat(05-04): rewrite App.svelte...`
- `[x] All 13 grep acceptance criteria on App.svelte verified green
- `[x] All 7 grep acceptance criteria on actions.ts verified green
- `[x] `npm --prefix ui run build` exits 0
- `[x] `npm --prefix ui run check` exits 0 (0 errors / 0 warnings)
- `[x] `make check-types` exits 0
- `[x] No modifications to STATE.md or ROADMAP.md (`git diff --name-only HEAD~2 HEAD` confirms 2 files: `ui/src/App.svelte`, `ui/src/lib/actions.ts`)

---
*Phase: 05-web-ui-completeness*
*Completed: 2026-05-15*
