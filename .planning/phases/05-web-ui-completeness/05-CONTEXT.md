# Phase 5: Web UI Completeness - Context

**Gathered:** 2026-05-15
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — design defaults locked from user-saved Solaris palette preference + brief §F6; no scoping debate required

<domain>
## Phase Boundary

Ship the real Svelte 5 single-page UI that turns `/api/state` into a field-engineer-grade dashboard with three working per-row actions, status badges, toasts, 5 s polling, in-place-upgrade-safe asset caching, and a pre-action "display may flicker" warning for `flutter`/`weston` services.

Concretely this phase fills:

1. **Tailwind v4 `@theme` Solaris token set** in `ui/src/app.css` — replaces the zinc/slate baseline with the Solaris palette mapped to semantic CSS variables (bg, fg, accent, success, warning, danger, etc.).
2. **Component tree** — `App.svelte`, `Header.svelte`, `Table.svelte` (replacing the current scaffold in `ui/src/lib/Table.svelte`), `Row.svelte`, `ActionButton.svelte`, `CopyButton.svelte`, `StatusBadge.svelte`, `Toast.svelte`, `ToastContainer.svelte`, `WarningModal.svelte`.
3. **State management via Svelte 5 runes** — page-level `state = $state({...})`, `$derived` for status pill mapping and last-poll relative timestamp, `$effect` for the 5 s polling timer. No stores.
4. **5 s background poll** — `setInterval`-style `$effect` that GETs `/api/state` and re-binds `state`. Pauses on action in-flight to avoid clobbering optimistic UI; resumes on response.
5. **Three action POSTs** — `fetch('/api/containers/:svc/update' | '/rollback' | '/force-pull', { method: 'POST' })` wired to the three icon-buttons per row. Optimistic disable on click, re-enable on response; success/error toasts; row's `action_in_flight` reflected in the status pill.
6. **Flutter/weston pre-action warning modal** — when the targeted service name (case-insensitive substring) matches `DISPLAY_DRAWING_SERVICES = ['flutter', 'weston']`, intercept the click and show a warning modal ("Display may flicker for 5–30 s during recreate. Continue?") with Confirm/Cancel before the POST fires.
7. **Header bar** — title + Refresh + Watch-now + relative last-poll timestamp ("3 s ago"). Refresh re-fetches `/api/state` immediately; Watch-now hits a `/api/poll-now` endpoint (Phase 3 added the trigger; Phase 5 just wires the button — if endpoint absent, gracefully degrades to Refresh).
8. **Asset-caching server config (Pitfall 8)** — `internal/api/handlers.go` sets `Cache-Control: public, immutable, max-age=31536000` on `/assets/*`, `Cache-Control: no-cache` on `index.html`, registers `mime.AddExtensionType(".js", "application/javascript")` at boot, and returns strict 404 on missing `/assets/*` (never falls back to index.html). Phase 1 wired the embed pipeline; Phase 5 verifies via the in-place-upgrade Playwright spec.
9. **Playwright e2e** — 5 spec files covering Success Criteria 1–4 (F6 surface, in-place-upgrade, flutter/weston warning, header + lock icon for disabled rows). The "manual smoke at 1024px" is the SC5 checkpoint.

Out of scope for this phase: the display-blackout UX *decision* between toast / two-step / danger-flag (Phase 6 — UX-01..03); production Dockerfile and image-size verification (Phase 7); full GitHub Actions pipeline (Phase 8). Phase 5 ships only the toast-only flutter/weston warning; the product decision happens in Phase 6 with this UI in hand.

</domain>

<decisions>
## Implementation Decisions

### Area 1 — Theme + Design Tokens (Plan 05-01)

- **Palette:** Solaris (Ethan Schoonover) — `base03..base3` greys + 8 accents. Light mode is the default (HMI screens are bright industrial environments — operators are reading these at noon under fluorescent light, not in a dimmed office). Dark mode is a "nice-to-have" — document tokens, do NOT ship a toggle. Phase 6+ may add `prefers-color-scheme` darkening if operators ask.
- **Token mapping** (`ui/src/app.css`):
  ```css
  @theme {
    --color-base03: #002b36;
    --color-base02: #073642;
    --color-base01: #586e75;
    --color-base00: #657b83;
    --color-base0:  #839496;
    --color-base1:  #93a1a1;
    --color-base2:  #eee8d5;
    --color-base3:  #fdf6e3;
    --color-yellow:  #b58900;
    --color-orange:  #cb4b16;
    --color-red:     #dc322f;
    --color-magenta: #d33682;
    --color-violet:  #6c71c4;
    --color-blue:    #268bd2;
    --color-cyan:    #2aa198;
    --color-green:   #859900;

    /* Semantic aliases (light mode) */
    --color-bg:        var(--color-base3);
    --color-bg-elev:   var(--color-base2);
    --color-fg:        var(--color-base00);
    --color-fg-muted:  var(--color-base01);
    --color-fg-strong: var(--color-base02);
    --color-border:    color-mix(in srgb, var(--color-base1) 35%, transparent);
    --color-accent:    var(--color-blue);
    --color-success:   var(--color-cyan);   /* operator-positive: cyan reads as 'go' on light bg */
    --color-warning:   var(--color-orange);
    --color-danger:    var(--color-red);
    --color-info:      var(--color-violet);
  }
  ```
- **Typography:** system stack already in place (`ui-sans-serif, system-ui, ...`). Mono is `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` — used for digest cells. Sizes: base 14px (`text-sm`) for table cells, 12px (`text-xs`) for digest mono, 18px for header title, 16px for warning modal body.
- **Spacing:** Tailwind defaults; row vertical padding `py-2.5`; cell horizontal `px-4`. Sticky header height `64px`.
- **Distinctiveness over generic AI-blue:** The Solaris palette is intentional — base3 cream background instead of pure white, base00 grey-green text instead of pure black, accents skewed warm (orange warning, yellow caution, cyan success). The result reads as "industrial control panel" not "SaaS dashboard." Do NOT use generic Tailwind `blue-500`, `red-500`, `slate-500`, etc.; use the Solaris CSS variables exclusively.
- **`app.css` retains `@import "tailwindcss"`** at the top (Phase 1 established). Utility classes still resolve; semantic tokens add on top.

### Area 2 — Component Tree (Plan 05-02 + 05-03)

- **File layout** (under `ui/src/lib/`):
  - `App.svelte` (stays in `ui/src/`) — page shell, hosts the polling effect + Toast region.
  - `lib/Header.svelte` — title + Refresh + Watch-now + last-poll-relative timestamp.
  - `lib/Table.svelte` — REPLACES the Phase 1 scaffold (currently at `ui/src/lib/Table.svelte`). Owns the table structure (thead + tbody); maps containers to `<Row>` instances.
  - `lib/Row.svelte` — single `<tr>` for one container; owns status pill, three ActionButtons, three CopyButtons.
  - `lib/StatusBadge.svelte` — colored pill component; props `status: 'current' | 'update_available' | 'updating' | 'rolling_back' | 'force_pulling' | 'action_error' | 'pinned' | 'stopped'`. Spinner inline for *_ing variants.
  - `lib/ActionButton.svelte` — icon-only button; props `{ kind: 'update' | 'rollback' | 'force-pull', service: string, disabled: boolean, onClick: () => void }`. Renders inline SVG icon; tooltip from `kind`.
  - `lib/CopyButton.svelte` — clipboard icon button; props `{ value: string }`. Uses `navigator.clipboard.writeText`; success-state for 1.5 s.
  - `lib/Toast.svelte` — single toast row; props `{ id, kind: 'success'|'error'|'warning'|'info', message, onDismiss }`.
  - `lib/ToastContainer.svelte` — fixed bottom-right region; mounts a list of `<Toast>` from a page-level `toasts: Toast[]` state slice. Auto-dismiss after 5 s except `error` (sticky until clicked).
  - `lib/WarningModal.svelte` — flutter/weston pre-action confirmation. Props `{ service, action, onConfirm, onCancel }`. Modal overlay with cyan/orange palette; trap focus; Esc closes (= cancel).

- **No state stores.** All state lives in `App.svelte` as `let state = $state<State | null>(null)`, `let toasts = $state<Toast[]>([])`, `let pendingAction = $state<{service, action, kind} | null>(null)`. Child components receive props + callbacks. Svelte 5 runes precedent set by Phase 1.

- **Status badge derivation** (`$derived` in Row.svelte):
  ```ts
  const status = $derived.by(() => {
    if (container.action_in_flight) return container.action_in_flight; // 'updating'|'rolling_back'|'force_pulling'
    if (container.action_error)     return 'action_error';
    if (container.pinned)           return 'pinned';
    if (container.stopped)          return 'stopped';
    if (container.update_available) return 'update_available';
    return 'current';
  });
  ```

- **Status color map** (in StatusBadge.svelte):
  | Status | Color (CSS var) | Notes |
  |--------|----------------|-------|
  | `current` | `--color-success` (cyan) | up-to-date |
  | `update_available` | `--color-warning` (yellow #b58900) | yellow not orange — orange is reserved for the flicker-warning toast |
  | `updating` | `--color-info` (violet) + spinner | in-flight |
  | `rolling_back` | `--color-info` (violet) + spinner | in-flight |
  | `force_pulling` | `--color-info` (violet) + spinner | in-flight |
  | `action_error` | `--color-danger` (red) | shows ActionError text on hover |
  | `pinned` | `--color-fg-muted` (base01) + lock icon | opt-out |
  | `stopped` | `--color-base1` slate | container died |

### Area 3 — Disabled Actions + Safety Labels (Plan 05-02)

- Row.svelte reads `container.labels?.["hmi-update.allow-update"]` and `["hmi-update.allow-rollback"]`. When `"false"`:
  - The corresponding ActionButton is NOT rendered (per UI-03: "buttons disabled when safety label opt-out" — interpret as "hidden + small lock icon in column header position to indicate why").
  - A small inline lock icon renders in the actions cell with a tooltip: `Update disabled by hmi-update.allow-update=false` (resp. rollback).
- Force-pull is NEVER disabled by labels (matches Phase 4 — force-pull is read-only with respect to the running container).
- Pinned containers (`container.pinned === true`) render NO action buttons at all + a single lock icon + tooltip `pinned: opt-out`.

### Area 4 — Toast + WarningModal (Plan 05-03)

- **Toast queue:** `toasts: Toast[]` slice in App.svelte; helper `addToast(t)` appends; `dismissToast(id)` removes. Auto-dismiss timer in `Toast.svelte` via `$effect` with `setTimeout(5000)` — except `kind === 'error'`, which stays until user clicks.
- **Toast shapes:**
  - `success` — cyan border-left, 4 px, white-ish base3 bg, base02 text. e.g. `Updated grafana → sha256:abc123…`
  - `error` — red border-left, base3 bg, red title, base00 body. e.g. `Update failed: verify_failed: container restarted 3 times in 15s`
  - `warning` — orange border-left, base3 bg. e.g. `Display may flicker during recreate.`
  - `info` — violet border-left.
- **WarningModal flow** (UI-08):
  1. User clicks Update or Rollback on a row whose `service` matches (case-insensitive substring) `DISPLAY_DRAWING_SERVICES = ['flutter', 'weston']`.
  2. App.svelte sets `pendingAction = {service, action, kind}` instead of firing the POST.
  3. WarningModal renders over the page (fixed inset, semi-transparent base02 backdrop). Title: `Display may flicker.` Body: `Recreating <strong>{service}</strong> on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?`. Two buttons: `Cancel` (returns focus to triggering row) and `Continue` (cyan; fires the POST).
  4. On Continue: invoke the action POST; close modal. On Cancel/Esc: clear `pendingAction`; no POST.
- **Force-pull does NOT trigger the warning.** Force-pull without `?recreate=true` is image-cache-only (no display blink). The default frontend behavior is force-pull-no-recreate; documented in tooltip.
- **DISPLAY_DRAWING_SERVICES is hardcoded** in `ui/src/lib/display-warning.ts`. A future Phase 6 may promote this to a per-service `hmi-update.display-drawing=true` label; deferred.

### Area 5 — Polling + Action Wiring (Plan 05-04)

- **Polling effect** in App.svelte:
  ```ts
  let state = $state<State | null>(null);
  let isActing = $state(false); // pauses poll while a per-row action is in flight at the UI level
  $effect(() => {
    const poll = async () => {
      if (isActing) return;
      try {
        const r = await fetch('/api/state', { cache: 'no-store' });
        if (r.ok) state = await r.json();
      } catch { /* network blip — next tick retries */ }
    };
    poll();
    const t = setInterval(poll, 5000);
    return () => clearInterval(t);
  });
  ```
  - 5 s interval = brief F6 and Phase 1 baseline.
  - `cache: 'no-store'` because in-place upgrade may swap the binary; we never want a stale `/api/state`.
  - On `isActing`, the next interval skip is a no-op; once the POST resolves, the next interval re-syncs.

- **Action POST helper** in `ui/src/lib/actions.ts`:
  ```ts
  export async function postAction(service: string, kind: 'update'|'rollback'|'force-pull'): Promise<ActionResult> {
    const r = await fetch(`/api/containers/${encodeURIComponent(service)}/${kind}`, { method: 'POST' });
    const body = await r.json().catch(() => ({}));
    if (!r.ok) throw new ActionError(r.status, body.error ?? 'unknown', body.reason ?? '');
    return body as ActionResult;
  }
  ```
  - `encodeURIComponent` on service name belt-and-braces (server-side regex is the actual gate — ACT-10).
  - Error shape mirrors Phase 4 (status, error code, reason); UI maps to a toast.

- **Per-row optimistic UI:**
  - Click Update → Row's three action buttons set `disabled=true` (local `let busy = $state(false)`); set `isActing = true` at the page level.
  - On success → push success toast with `Updated {service}` + short digest tail; clear `busy`; clear `isActing`; the next 5 s poll re-syncs the row's `current_digest`.
  - On error → push error toast with `Update failed: {reason}`; clear `busy`; clear `isActing`.
  - The row's `action_in_flight` (set by the server in `/api/state` after the channel propagates `KindActionStart`) will also color the badge — but the UI-local `busy` flag is what prevents the double-click race (Pitfall 11 UX side).

- **Idempotent / no-op response (Phase 4 ACT-06/07):**
  - Server returns 200 `{"no_op": true, "current_digest": ...}`; UI shows an info toast `No update needed for {service}` rather than success.

### Area 6 — Header + Last-Poll Timestamp (Plan 05-02)

- **Header.svelte** props: `{ lastPollEnd: string | undefined, onRefresh: () => void, onWatchNow: () => void }`.
- **Last-poll relative time** computed in Header via `$derived` against a small "now" tick:
  ```ts
  let now = $state(Date.now());
  $effect(() => { const t = setInterval(() => now = Date.now(), 1000); return () => clearInterval(t); });
  const ago = $derived.by(() => relativeTime(lastPollEnd, now));
  ```
  Format: `Xs ago` for <60 s, `Xm Ys ago` for <1 h, `Xh Ym ago` beyond. Falls back to `never` when `lastPollEnd` is undefined.
- **Refresh** button — calls `onRefresh` (App.svelte's `poll()` immediately, outside the 5 s tick).
- **Watch now** button — calls `onWatchNow` which POSTs `/api/poll-now` if available, else falls back to Refresh. Phase 3's poll surface exposes `/api/poll-now` for the "force a fetch now" affordance; if not (Phase 5 should not depend on Phase 6+); the button degrades gracefully to a manual refresh + a small note in the response toast.

### Area 7 — Asset Caching (Plan 05-05)

- **`internal/api/handlers.go` (Phase 1 wired the embed FS; Phase 5 hardens the headers):**
  - `mime.AddExtensionType(".js", "application/javascript")` at server boot (NOT in the handler — once, in `NewServer`). Same for `.css`, `.svg`, `.json`, `.woff2`.
  - `/assets/*` handler: serves from `dist/assets/`; sets `Cache-Control: public, immutable, max-age=31536000` on every response; returns strict `404` (no fallback) on miss.
  - `/` (and any non-`/assets/`, non-`/api/`, non-`/healthz` path) handler: serves `dist/index.html`; sets `Cache-Control: no-cache` and `Pragma: no-cache`; etag computed from bundle hash.
  - `/api/state` already memory-only (Phase 4 OBS-03); Phase 5 adds `Cache-Control: no-store`.
- **In-place-upgrade Playwright spec:** open the page; rebuild + restart `hmi-update` with new bundle hash; soft-refresh (`location.reload()`); assert the page still works, that `/assets/*-<newhash>.js` returns 200 with `application/javascript` + `immutable` Cache-Control, and that the *old* hashed asset URL returns 404 (not index.html).

### Area 8 — Playwright Test Plan (Plan 05-05)

- **`e2e/tests/ui-table.spec.ts`** — UI-01..02, UI-09:
  - Table renders 7 columns by header text.
  - At least one row appears for the stub container; digests are 12-char short form.
  - Copy icon click writes the full digest to the clipboard (Playwright's `clipboard` permission + `evaluate(() => navigator.clipboard.readText())`).
- **`e2e/tests/ui-actions.spec.ts`** — UI-03, UI-05, UI-07:
  - Click Update on a non-safety-labelled row; assert buttons disabled mid-flight; assert success toast appears with new digest tail; assert row re-enables.
  - Click Rollback; assert success toast.
  - Click Force-pull; assert info toast `Re-pulled {service}` (no recreate).
  - Click Update on a `hmi-update.allow-update=false` row; assert NO Update button + lock icon visible.
- **`e2e/tests/ui-flutter-warning.spec.ts`** — UI-08:
  - Test stack includes a `weston-stub` service (alias of busybox, labelled `hmi-update.watch=true`).
  - Click Update on `weston-stub`; assert WarningModal renders with the flicker copy; click Cancel — assert NO POST fired (network log assertion via Playwright route interception).
  - Click Update again; click Continue — assert POST fires; success path closes modal.
- **`e2e/tests/ui-header.spec.ts`** — UI-04, UI-06:
  - Header shows `hmi-update` title + Refresh + Watch-now buttons + last-poll text matching `/^\d+s ago$/` or `never`.
  - Wait 6 s; assert state re-fetched (intercept `/api/state` count, assert >= 2 in 6 s window).
- **`e2e/tests/ui-inplace-upgrade.spec.ts`** — UI-10, Pitfall 8:
  - Load the page; capture the current `/assets/*-<hash>.js` URL.
  - Run `make ui && make build && docker compose up -d hmi-update` from a `child_process` helper (`e2e/fixtures/rebuild-binary.ts`) — this rebuilds the bundle and restarts the container.
  - `await page.reload({ waitUntil: 'networkidle' })`.
  - Assert the page works; assert new bundle URL has a different hash; assert old URL returns 404 (`page.request.get(oldUrl).status() === 404`); assert `application/javascript` content-type on the new bundle (Pitfall 8 byte-level proof).

### File Layout

- `ui/src/app.css` — Solaris @theme tokens + base reset (Plan 05-01)
- `ui/src/App.svelte` — page shell, polling effect, toasts, modal host (Plan 05-04)
- `ui/src/lib/Header.svelte` — header bar (Plan 05-02)
- `ui/src/lib/Table.svelte` — REPLACED — table with rows (Plan 05-02)
- `ui/src/lib/Row.svelte` — single row with status + 3 action buttons + copy icons (Plan 05-02)
- `ui/src/lib/StatusBadge.svelte` — pill component (Plan 05-02)
- `ui/src/lib/ActionButton.svelte` — icon-only button (Plan 05-02)
- `ui/src/lib/CopyButton.svelte` — clipboard icon button (Plan 05-02)
- `ui/src/lib/Toast.svelte` — single toast (Plan 05-03)
- `ui/src/lib/ToastContainer.svelte` — fixed bottom-right region (Plan 05-03)
- `ui/src/lib/WarningModal.svelte` — flutter/weston pre-action modal (Plan 05-03)
- `ui/src/lib/display-warning.ts` — `DISPLAY_DRAWING_SERVICES` + `requiresWarning(svc)` (Plan 05-03)
- `ui/src/lib/actions.ts` — `postAction` + `ActionError` (Plan 05-04)
- `ui/src/lib/relative-time.ts` — `relativeTime(iso, now)` formatter (Plan 05-02)
- `internal/api/handlers.go` — Cache-Control + mime registration hardening (Plan 05-05)
- `internal/api/handlers_assets_test.go` — Go unit test for /assets/* immutable + strict 404 + js content-type (Plan 05-05)
- `e2e/tests/ui-table.spec.ts` — RED FIRST. UI-01..02, UI-09 (Plan 05-05)
- `e2e/tests/ui-actions.spec.ts` — RED FIRST. UI-03, UI-05, UI-07 (Plan 05-05)
- `e2e/tests/ui-flutter-warning.spec.ts` — RED FIRST. UI-08 (Plan 05-05)
- `e2e/tests/ui-header.spec.ts` — RED FIRST. UI-04, UI-06 (Plan 05-05)
- `e2e/tests/ui-inplace-upgrade.spec.ts` — RED FIRST. UI-10 + Pitfall 8 (Plan 05-05)
- `e2e/fixtures/rebuild-binary.ts` — helper that runs `make ui && make build && docker compose up -d hmi-update` (Plan 05-05)

### Concurrency Invariants

- All state mutations in the UI happen on the main JS thread (single-threaded — no shared workers). The `isActing` flag is a cooperative lock that prevents poll-vs-click races (Pitfall 11 UX side).
- The server is the authoritative race-resolver (per-service `sync.Mutex` from Phase 4 — Pitfall 11 server side). The UI's `disabled` state is just a debounce; if it desyncs, the server returns 409 and the UI shows an error toast.

### Configuration Knobs (none new for Phase 5)

- Reuse `HMI_UPDATE_POLL_INTERVAL_MS` (defaults to 5000) — env var read by App.svelte via a `window.__HMI_CONFIG__` server-side injection? **Decision: NO** — hardcode 5000 in the bundle. The poll interval is part of the brief; env-driving it would add complexity for zero v1 benefit. Document as a v2 candidate.

### Claude's Discretion

- Whether to put `Header.svelte`, `Row.svelte`, etc. under `ui/src/lib/` (matches Phase 1's `Table.svelte` location) or `ui/src/components/`. Lean `ui/src/lib/` — Phase 1 precedent.
- Whether to ship a global `toast()` helper or pass `addToast` down by props. Lean prop-passing — Svelte 5 idiomatic, no implicit globals.
- Icon set — Heroicons (MIT, inline SVG) or `lucide-svelte` (small dep). Lean inline SVGs from Heroicons copy-pasted into components — keeps the no-extra-deps ethos; ~10 icons total (refresh, eye, upload, undo, repeat, copy, check, lock, x, info-triangle).
- Whether the table collapses to vertical cards at 768 px or stays as a horizontally-scrolled table. Lean stays-as-table with `overflow-x-auto` wrapper — operators use 1024 px+ HMIs; mobile is a "happens occasionally on a phone" concern, not a daily flow.
- Whether the success toast shows the full new digest or a short prefix. Lean short prefix `sha256:abc1234…` (8 hex chars) — operators want a glance, not a paste target; CopyButton is for the paste.
- Whether the WarningModal traps focus via a Svelte action or a hand-rolled `onMount` focus loop. Lean Svelte action — reusable; ~20 LOC.
- Whether `force-pull` ever shows a "?recreate=true" toggle in the UI. Lean NO for v1 — server defaults to no-recreate; operators who need recreate run Update.
- Whether to add a `<noscript>` fallback. Lean YES — single sentence "hmi-update requires JavaScript. Re-enable and reload." in base00 on base3.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets

- `ui/src/App.svelte` (Phase 1) — current shell does the initial `fetch('/api/state')`; Phase 5 replaces with full polling + actions + modal/toast hosting.
- `ui/src/lib/Table.svelte` (Phase 1) — current scaffold has the 7-column thead + the empty-state row; Phase 5 keeps the table structure verbatim, swaps the tbody for `<Row>` instances.
- `ui/src/lib/types.d.ts` (Phase 1+2+3+4 tygo-generated) — `Container` and `State` types already include `action_in_flight`, `action_error`, `last_poll_start/end/error`. No regen required for Phase 5 (no Go-side type changes).
- `ui/src/app.css` (Phase 1) — has `@import "tailwindcss"` and an empty `@theme` block; Phase 5 fills the `@theme`.
- `ui/vite.config.ts` (Phase 1) — emits to `internal/api/dist/`; Phase 5 unchanged.
- `internal/api/handlers.go` (Phase 1) — current `/assets/*` strict-no-fallback + `/` index.html serving exists; Phase 5 adds the `Cache-Control` + `mime.AddExtensionType` hardening.
- `internal/api/server.go` (Phase 1+4) — routes registered; Phase 5 does not add routes (POST endpoints land in Phase 4 Plan 04-04).
- `internal/state.State.LastPollStart/LastPollEnd/LastPollError` (Phase 3) — surfaces the timestamps the header needs.
- `internal/state.Container.ActionInFlight / ActionError` (Phase 4) — surfaces the per-row spinner + error state the row badge reads.
- `e2e/compose.test.yml` (Phase 1+2+3+4) — Phase 5 adds a `weston-stub` service (busybox alias) for the flutter/weston warning spec.
- `e2e/playwright.config.ts` (Phase 1) — Phase 5 reuses globalSetup/teardown.
- `e2e/fixtures/push-image.ts` (Phase 3) — Phase 5 reuses for the in-place-upgrade spec (push the new bundle's manifest if testing via the registry path) — though the canonical in-place-upgrade test rebuilds the binary directly via `e2e/fixtures/rebuild-binary.ts` (new).

### Established Patterns

- **Svelte 5 runes for state** (Phase 1) — `$state`, `$derived`, `$effect`. No stores. Phase 5 doubles down.
- **`//go:embed all:dist` + strict no-fallback `/assets/*`** (Phase 1) — Phase 5 hardens with Cache-Control + mime.AddExtensionType.
- **Tygo source-of-truth for types** (Phase 1) — Phase 5 imports `Container` + `State` directly from `types.d.ts`; never hand-defines wire shapes.
- **Sentinel errors per package** (Phase 2 onwards) — `actions.ts` mirrors with an `ActionError` class.
- **RED-first Playwright e2e** (Phase 1+2+3+4) — Phase 5 adds 5 spec files, all RED first.

### Integration Points

- `ui/src/App.svelte` rebinds: `containers` → full `State` object including `last_poll_end`.
- `internal/api/handlers.go` modifies the FileServer composition to add Cache-Control headers via a wrapping `http.Handler`.
- `cmd/hmi-update/main.go` boot — Phase 5 may add `mime.AddExtensionType` calls before `api.NewServer` (or move them inside `NewServer`); leaner inside `NewServer` so tests get them too.
- `e2e/compose.test.yml` — Phase 5 adds `weston-stub` watched service.

</code_context>

<specifics>
## Specific Ideas

- **The five Playwright specs run in parallel in Phase 5's CI matrix.** Each spec is a fresh `globalSetup` per Phase 1's pattern; total e2e time stays under 2 minutes.
- **`ui-inplace-upgrade.spec.ts` is the load-bearing Pitfall 8 proof.** Without this test the regression class returns silently. The spec MUST run in CI on every PR — never skip.
- **The orange-warning palette choice for flutter/weston is intentional.** Solaris orange (#cb4b16) is the operator's universal "pay attention" signal; red is reserved for hard errors only. Reusing red for the flicker warning would dilute the danger signal.
- **The cyan-success choice is also intentional.** Solaris green (#859900) reads as olive/desaturated and gets lost on the cream base3 background; cyan (#2aa198) has enough chromatic punch to be the "go" color.
- **DISPLAY_DRAWING_SERVICES is a frontend constant, not a server setting.** The server has no opinion about which services are display-drawing; it just executes the action. The UI is the right layer to add operator-protective warnings. Promoting this to a label (`hmi-update.display-drawing=true`) is a Phase 6 UX-01 candidate.
- **The 5 s poll skip during `isActing` is deliberate.** A POST takes 1–30 s (verify window); we do NOT want a poll to overwrite optimistic state mid-flight. The server's `action_in_flight` field will eventually carry the right value once the channel propagates, but until the POST response arrives, the UI is the source of truth for "this row is busy."
- **Last-poll timestamp updates every 1 s** (the local "now" tick) but the poll itself fires every 5 s. This makes the "Xs ago" counter feel responsive even when no new state has arrived.
- **The `cache: 'no-store'` on `/api/state` GETs is critical** for in-place upgrade — browsers cache JSON by default; we never want a stale state JSON from before the binary was swapped.
- **Manual smoke at 1024 px (SC5)** uses Chrome DevTools device mode at the "Surface Pro 7" preset (912×1368) or a 1024×768 viewport in Playwright headed mode. Both Centroid HMI screens fall in this band.

</specifics>

<deferred>
## Deferred Ideas

- **`prefers-color-scheme: dark` Solaris dark mode** — tokens documented in UI-SPEC.md but not shipped in Phase 5. Phase 6+ if operators request.
- **`hmi-update.display-drawing=true` label promotion** — Phase 6 UX-01 candidate. Phase 5 hardcodes the frontend array.
- **Two-step prepare/switch UX for flutter/weston** — Phase 6 UX-01 option (b). Phase 5 ships only the toast-only warning; the product decision happens with this UI in hand.
- **Per-row history / N-deep rollback selector** — V2-N-DEEP. Phase 5 honors single-slot semantic.
- **Server-side push (SSE / WebSocket)** — V2-WEBSOCKET. Phase 5 ships 5 s polling.
- **Mobile vertical-card layout** — Phase 5 ships horizontally-scrolled table at <768 px; vertical cards is a v1.1 polish item.
- **Bulk Update / Update-all button** — out of scope per Phase 4 deferred list.
- **i18n** — Phase 5 ships English-only copy. v2 if Centroid grows non-English ops.
- **`HMI_UPDATE_POLL_INTERVAL_MS` env exposure to the bundle** — hardcoded 5000 in Phase 5; v2 candidate.

</deferred>
</content>
