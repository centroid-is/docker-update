---
phase: 05-web-ui-completeness
reviewed: 2026-05-15T12:03:27Z
depth: standard
files_reviewed: 20
files_reviewed_list:
  - ui/src/app.css
  - ui/src/App.svelte
  - ui/src/lib/StatusBadge.svelte
  - ui/src/lib/ActionButton.svelte
  - ui/src/lib/CopyButton.svelte
  - ui/src/lib/Row.svelte
  - ui/src/lib/Header.svelte
  - ui/src/lib/Table.svelte
  - ui/src/lib/Toast.svelte
  - ui/src/lib/ToastContainer.svelte
  - ui/src/lib/WarningModal.svelte
  - ui/src/lib/focus-trap.ts
  - ui/src/lib/display-warning.ts
  - ui/src/lib/actions.ts
  - ui/src/lib/relative-time.ts
  - cmd/hmi-update/main.go
  - internal/api/handlers.go
  - internal/api/static.go
  - internal/api/handlers_assets_test.go
  - e2e/tests/ui-table.spec.ts
  - e2e/tests/ui-flutter-warning.spec.ts
  - e2e/tests/ui-header.spec.ts
  - e2e/tests/ui-actions.spec.ts
  - e2e/tests/ui-inplace-upgrade.spec.ts
  - e2e/playwright.config.ts
  - e2e/fixtures/rebuild-binary.ts
  - e2e/compose.test.yml
findings:
  critical: 3
  warning: 7
  info: 5
  total: 15
status: issues_found
fixed:
  at: 2026-05-15T12:18:00Z
  iteration: 1
  scope: critical_warning
  in_scope: 10
  applied: 10
  skipped: 0
  ids:
    - CR-01
    - CR-02
    - CR-03
    - WR-01
    - WR-02
    - WR-03
    - WR-04
    - WR-05
    - WR-06
    - WR-07
  report: .planning/phases/05-web-ui-completeness/05-REVIEW-FIX.md
---

# Phase 5: Code Review Report

**Reviewed:** 2026-05-15T12:03:27Z
**Depth:** standard
**Files Reviewed:** 26
**Status:** issues_found

## Summary

Phase 5 delivers a complete Svelte 5 dashboard with polling, toasts, a flicker
warning modal, and the load-bearing Pitfall 8 hardening of the embedded static
handler. The Pitfall 8 trio (`/assets/*` immutable + strict 404, `/` no-cache,
`/api/state` no-store) is wired correctly and pinned by Go unit tests. The
flutter/weston warning is wired with correct Cancel/Continue/Escape semantics.

However, three BLOCKER-level defects surfaced under adversarial review:

1. **CR-01**: The load-bearing `ui-inplace-upgrade.spec.ts` (the only e2e proof
   of Pitfall 8 end-to-end) has a path mismatch between the marker file it
   writes and the import path it injects into `App.svelte`. The spec will fail
   on first run because Vite cannot resolve the injected import.
2. **CR-02**: `static.go` sets the `Cache-Control: public, max-age=31536000,
   immutable` header *before* delegating to `http.FileServerFS`, so a 404 on a
   missing `/assets/*` URL also receives `immutable` for one year. Browsers
   that cache 404s will never recover until manual cache clear — directly
   undermines the very behaviour Pitfall 8 is meant to defend against.
3. **CR-03**: `WarningModal.svelte`'s focus trap never restores focus to the
   triggering button on close. Review priority #5 explicitly demands this;
   the `focus-trap.ts` action captures no prior `activeElement` and provides
   no restoration on destroy.

Seven warnings cover: unsafe `StatusKind` cast, polling effect timer churn,
misleading "Watch now" toast for 5xx, missing focus restoration documentation,
modal restoration in afterAll only handles the marker file not App.svelte,
and a few quality items. Five info items round out the report.

The Pitfall 5 (flutter/weston) wiring is correct. The Solaris palette mapping
matches UI-SPEC §4.3 (`--color-pending` for update_available, `--color-warning`
reserved for flicker). Svelte 5 runes are used correctly throughout.

## Critical Issues

### CR-01: `ui-inplace-upgrade.spec.ts` writes marker file to wrong directory — `make ui` will fail

**File:** `e2e/tests/ui-inplace-upgrade.spec.ts:47, 137`
**Issue:** The marker file is written to `ui/src/build-marker.ts` (line 47):

```ts
const MARKER_FILE = join(REPO_ROOT, 'ui', 'src', 'build-marker.ts');
```

But the import injected into `App.svelte` (line 137) references `./lib/build-marker`:

```ts
import { BUILD_MARKER } from './lib/build-marker';
```

From `ui/src/App.svelte`, `./lib/build-marker` resolves to `ui/src/lib/build-marker.ts`,
NOT `ui/src/build-marker.ts`. Vite will throw an unresolved-module error during
`rebuildAndRestart()`'s `make ui` step, the spec body throws, and the only
end-to-end byte-level proof of Pitfall 8 (the test scope explicitly calls this
out as load-bearing) never runs.

Worse, even though the `try { ... } finally { writeFileSync(appSvelte, appOriginal) }`
restores App.svelte, the orphaned `ui/src/build-marker.ts` file remains on
disk because `afterAll` only deletes `MARKER_FILE` if `!markerExistedBefore`
— which it wasn't before. So a developer's working tree gets cluttered by
a phantom marker file at the wrong path.

**Fix:** Make the two paths agree. Either:

```ts
// Option A — write the marker where the import expects:
const MARKER_FILE = join(REPO_ROOT, 'ui', 'src', 'lib', 'build-marker.ts');
```

Or change the import injection to reference `./build-marker`:

```ts
appPatched = appOriginal.replace(
  /(<script lang="ts">\s*\n)/,
  `$1  import { BUILD_MARKER } from './build-marker';
  void BUILD_MARKER;
`,
);
```

Verify with `node -e "import('${MARKER_FILE}')"` or a smoke run of `make ui`
after the fix.

**Severity rationale:** Blocks the only end-to-end proof of Pitfall 8.
Pitfall 8 is exactly the bug class that "looks fixed" until an in-place
upgrade silently breaks every operator's browser. Without this spec running
green, the phase ships unprovably.

---

### CR-02: `/assets/*` strict-404 path applies `immutable` Cache-Control to 404 responses

**File:** `internal/api/static.go:44-47`
**Issue:**

```go
case strings.HasPrefix(clean, "/assets/"):
    // Strict static serve. http.FileServerFS returns 404 on miss
    // and sets Content-Type via mime.TypeByExtension.
    w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
    fileServer.ServeHTTP(w, r)
```

The `Cache-Control` header is set BEFORE delegating to `http.FileServerFS`.
When `FileServerFS` finds nothing it writes a 404 response — but the
`Cache-Control: public, max-age=31536000, immutable` header is already in
the ResponseWriter's header map. Per RFC 9111 §3, 404 responses MAY be
cached if cacheable headers are present; Chromium, Firefox, and most CDNs
honour `Cache-Control: immutable` for any status including 4xx.

This is the exact failure mode Pitfall 8 was meant to prevent: an operator
performs an in-place upgrade, the deploy momentarily 404s on the new asset
URL (e.g. a service worker probe races the recreate), Chromium caches the
404 as immutable for a year, and the operator's UI is permanently broken
until they manually clear the cache.

The Go unit test `TestAssets_StrictNoFallback` (handlers_assets_test.go:109)
asserts the status code is 404 and the body lacks `<html` — but it does
NOT assert the Cache-Control header is removed on 404. So the regression
slips past CI.

**Fix:** Check for asset existence before setting the header, or strip
the header on non-200 responses via a wrapped ResponseWriter:

```go
case strings.HasPrefix(clean, "/assets/"):
    // Probe first; only set immutable on a real hit so 404s aren't cached.
    assetPath := strings.TrimPrefix(clean, "/")  // dist FS root
    if _, err := fs.Stat(sub, assetPath); err != nil {
        http.NotFound(w, r)
        return
    }
    w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
    fileServer.ServeHTTP(w, r)
```

Or, more idiomatically, wrap the writer:

```go
case strings.HasPrefix(clean, "/assets/"):
    cw := &cacheControlOnSuccessWriter{ResponseWriter: w,
        header: "public, max-age=31536000, immutable"}
    fileServer.ServeHTTP(cw, r)
```

Where the wrapper only sets the header when `WriteHeader(2xx)` is observed.

Add a regression test:

```go
func TestAssets_404DoesNotCarryImmutable(t *testing.T) {
    srv := newTestServer(t)
    req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)
    rec := httptest.NewRecorder()
    srv.Handler().ServeHTTP(rec, req)
    if rec.Code != http.StatusNotFound {
        t.Fatalf("status = %d, want 404", rec.Code)
    }
    cc := rec.Header().Get("Cache-Control")
    if strings.Contains(strings.ToLower(cc), "immutable") {
        t.Errorf("Cache-Control on 404 = %q, must NOT contain `immutable`", cc)
    }
}
```

**Severity rationale:** This is the exact Pitfall 8 failure mode the
Phase 5 hardening is meant to prevent — caching the wrong response for
a year locks operators out until manual intervention. The bug class
that the Phase 5 plan explicitly calls out as "never silently regress."

---

### CR-03: `focus-trap.ts` does NOT restore focus to the triggering element on modal close

**File:** `ui/src/lib/focus-trap.ts:26-71`
**Issue:** Review priority #5 explicitly requires "Focus trap in WarningModal
restores focus on close." UI-SPEC.md §4.7 also lists this under the modal
accessibility contract ("Cancel — secondary: ... returns focus to triggering
row"). The current `focusTrap` action:

1. Captures the focusables inside the modal.
2. On mount, focuses the `[data-primary]` Continue button.
3. Traps Tab/Shift-Tab inside.
4. Dispatches `cancel` on Escape.
5. On `destroy()`, removes the keydown listener — and does NOTHING else.

There is no `previouslyFocused = document.activeElement as HTMLElement;` at
mount and no `previouslyFocused?.focus();` on destroy. Operators using
keyboard navigation will find their focus context lost every time they
dismiss the modal — focus snaps back to `<body>`, breaking the muscle-memory
flow of "Tab to Update button → Enter → Esc → continue tabbing."

WCAG 2.4.3 (Focus Order) failure for keyboard users; UI-SPEC.md §6 violation.

**Fix:**

```ts
export function focusTrap(node: HTMLElement) {
  // Capture the element that had focus before the modal mounted so we
  // can restore it on destroy (WCAG 2.4.3 — Focus Order).
  const previouslyFocused = document.activeElement as HTMLElement | null;

  const focusables = () =>
    node.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );

  function onKeydown(e: KeyboardEvent) {
    // ... unchanged ...
  }

  node.addEventListener('keydown', onKeydown);

  queueMicrotask(() => {
    const primary = node.querySelector<HTMLElement>('[data-primary]');
    primary?.focus();
  });

  return {
    destroy() {
      node.removeEventListener('keydown', onKeydown);
      // Restore focus to the pre-modal element if it is still in the DOM
      // and focusable. Guards: the trigger might have unmounted (rare),
      // or be `<body>` (no-op).
      if (previouslyFocused && document.contains(previouslyFocused)) {
        previouslyFocused.focus();
      }
    },
  };
}
```

Add a Playwright assertion in `ui-flutter-warning.spec.ts`:

```ts
test('Cancel restores focus to the Update button', async ({ page }) => {
  const updateBtn = page.getByRole('button', { name: /^Update weston-stub$/i });
  await updateBtn.click();
  await page.getByRole('dialog').getByRole('button', { name: /^Cancel$/i }).click();
  await expect(updateBtn).toBeFocused();
});
```

**Severity rationale:** Accessibility regression in a keyboard-driven
operator UI (HMI tooling — field engineers often work with one hand on
the box, keyboard-only access). Review priority #5 explicitly demanded
this behaviour and it is absent.

## Warnings

### WR-01: `Row.svelte` casts `container.action_in_flight` (a wire string) to `StatusKind` without runtime validation

**File:** `ui/src/lib/Row.svelte:42-46`
**Issue:**

```ts
if (container.action_in_flight) {
  // Server emits one of 'updating' | 'rolling_back' | 'force_pulling'.
  // Trust the value verbatim; the StatusKind union covers exactly these.
  return container.action_in_flight as StatusKind;
}
```

The wire type (types.d.ts:95) is `action_in_flight?: string` — any string is
type-compatible. The comment claims "the StatusKind union covers exactly
these" but provides no runtime guard. If the server (now or in a future
phase) ever emits an unexpected value (e.g. `downloading`, `verifying`,
empty string treated as falsy but `" "` as truthy), the `StatusBadge`
switch falls through all cases and `accentVar` is `undefined`. The
template then renders `style:color="var(undefined)"` which is invalid CSS
and silently renders the pill with the browser's default color (black on
cream — not the operator-protective contrast).

**Fix:** Whitelist:

```ts
const IN_FLIGHT = new Set<StatusKind>(['updating', 'rolling_back', 'force_pulling']);

const status = $derived.by<StatusKind>(() => {
  const inFlight = container.action_in_flight;
  if (inFlight && IN_FLIGHT.has(inFlight as StatusKind)) {
    return inFlight as StatusKind;
  }
  if (container.action_error)     return 'action_error';
  // ... rest unchanged ...
});
```

And add a default case to `StatusBadge.accentVar` returning `--color-fg-muted`
as a safe fallback.

**Severity rationale:** Wire-trust violation with a silent failure mode.
Operator sees an "uncategorised" badge with no warning that something
unexpected is happening server-side.

---

### WR-02: `App.svelte` polling `$effect` re-creates the interval timer on every busy-state flip

**File:** `ui/src/App.svelte:155-159`
**Issue:**

```ts
$effect(() => {
  void poll();
  const t = setInterval(poll, 5000);
  return () => clearInterval(t);
});
```

The first `void poll()` synchronously reads `isActing` (a `$derived` over
`busyServices.size`). That reactive read causes the effect to re-run every
time `busyServices` mutates. So every Update click triggers:

1. Cleanup: `clearInterval(t)` (the existing 5s timer)
2. Re-run: `void poll()` (early-returns because `isActing` is now true)
3. New `setInterval` is started

Then again on POST completion. This means polling is "reset" twice per
action — the 5s cadence is broken in unpredictable ways the moment any
action runs. The Playwright `ui-header.spec.ts` UI-06 test ("at least 2
GETs in 6s") is loose enough to absorb this, but the cadence guarantee
is weaker than documented.

**Fix:** Make `poll` read `isActing` lazily so the synchronous body of
the effect doesn't track it:

```ts
$effect(() => {
  // Local-scoped poll closure that re-reads isActing at call time only.
  // Critically, we do NOT call poll() synchronously inside the effect —
  // that would track isActing as an effect dep and tear down the
  // interval on every busy-state flip.
  const tick = () => { void poll(); };
  tick();
  const t = setInterval(tick, 5000);
  return () => clearInterval(t);
});
```

Wait — `tick()` synchronously calls `poll()` which still reads `isActing`.
The fix needs to ensure the read happens in a non-tracked context:

```ts
import { untrack } from 'svelte';

$effect(() => {
  untrack(() => { void poll(); });
  const t = setInterval(() => { void poll(); }, 5000);
  return () => clearInterval(t);
});
```

The `setInterval` callback is a microtask outside the effect's tracking
boundary, so reads inside it don't track. The synchronous initial `poll()`
must be wrapped in `untrack()` to avoid the same tracking.

**Severity rationale:** Subtle reactivity churn that breaks the documented
5s cadence. Not user-visible until the cadence guarantee matters (e.g. a
future feature that depends on "exactly once per 5s"), but cheap to fix
and a real Svelte 5 idiom miss.

---

### WR-03: `handleWatchNow` shows "Poll-now endpoint not available" toast for any non-2xx, including 5xx

**File:** `ui/src/App.svelte:167-177`
**Issue:**

```ts
async function handleWatchNow(): Promise<void> {
  const ok = await pollNow();
  if (!ok) {
    addToast(
      'info',
      'Watch now',
      'Poll-now endpoint not available; refreshed instead.',
    );
  }
  await poll();
}
```

`pollNow()` returns false on 404 (endpoint not implemented), 5xx
(endpoint broken), and network failure (no connectivity). The toast
copy hardcodes "Poll-now endpoint not available" — misleading when the
real cause is a transient 5xx or a network blip. Operators reading the
toast on a real failure would conclude the feature is missing and stop
trying.

Review scope #7 says: "404 from pollNow uses info toast." That's
correct for 404. Other failure modes should not lie about the cause.

**Fix:** `pollNow()` should differentiate:

```ts
export type PollNowResult =
  | { ok: true }
  | { ok: false; reason: 'not_implemented' | 'server_error' | 'network' };

export async function pollNow(): Promise<PollNowResult> {
  try {
    const r = await fetch('/api/poll-now', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    });
    if (r.ok) return { ok: true };
    if (r.status === 404) return { ok: false, reason: 'not_implemented' };
    return { ok: false, reason: 'server_error' };
  } catch {
    return { ok: false, reason: 'network' };
  }
}
```

Then in `handleWatchNow`:

```ts
async function handleWatchNow(): Promise<void> {
  const result = await pollNow();
  if (!result.ok) {
    if (result.reason === 'not_implemented') {
      addToast('info', 'Watch now', 'Poll-now endpoint not available; refreshed instead.');
    } else {
      addToast('warning', 'Watch now', `Could not trigger a poll (${result.reason}); refreshed instead.`);
    }
  }
  await poll();
}
```

**Severity rationale:** Operator-facing lie that masks real failures.
Misleading diagnostics is a worse failure mode than a generic error
because operators stop reporting issues that "the toast says aren't bugs."

---

### WR-04: `ui-inplace-upgrade.spec.ts` afterAll does not restore App.svelte if test body throws BEFORE the finally block

**File:** `e2e/tests/ui-inplace-upgrade.spec.ts:127-141, 201-205`
**Issue:** The test patches App.svelte inside the test body:

```ts
const appSvelte = join(REPO_ROOT, 'ui', 'src', 'App.svelte');
const appOriginal: string = readFileSync(appSvelte, 'utf8');
let appPatched = appOriginal;
if (!appOriginal.includes('./lib/build-marker')) {
  appPatched = appOriginal.replace(...);
  writeFileSync(appSvelte, appPatched, 'utf8');
}

try {
  await rebuildAndRestart();
  // ... assertions ...
} finally {
  writeFileSync(appSvelte, appOriginal, 'utf8');
}
```

But `appOriginal = readFileSync(...)` is captured INSIDE the test function,
not in `beforeAll`. If `readFileSync` succeeds but `writeFileSync(appPatched)`
fails (disk full, permission), the file is left in unknown state with no
restoration handle. Worse, the marker-write happens BEFORE App.svelte is
patched, so a crash between the two writes leaves a phantom marker file
behind.

Combined with CR-01, every failed run leaves the repo in a dirty state
that breaks subsequent runs.

**Fix:** Move the App.svelte capture to `beforeAll` and the restoration
to `afterAll` so it always fires:

```ts
let appSvelteOriginal: string | undefined;
const appSvelte = join(REPO_ROOT, 'ui', 'src', 'App.svelte');

test.beforeAll(() => {
  appSvelteOriginal = readFileSync(appSvelte, 'utf8');
  markerExistedBefore = existsSync(MARKER_FILE);
  if (markerExistedBefore) {
    markerOriginalContent = readFileSync(MARKER_FILE, 'utf8');
  }
});

test.afterAll(() => {
  // Restore App.svelte unconditionally.
  if (appSvelteOriginal !== undefined) {
    writeFileSync(appSvelte, appSvelteOriginal, 'utf8');
  }
  // Restore the marker file (existing logic).
  if (!markerExistedBefore && existsSync(MARKER_FILE)) {
    rmSync(MARKER_FILE);
  } else if (markerExistedBefore && markerOriginalContent !== undefined) {
    writeFileSync(MARKER_FILE, markerOriginalContent, 'utf8');
  }
});
```

**Severity rationale:** Test pollution is a known foot-gun; a phase-leaving
dirty tree corrupts subsequent runs and silently breaks CI.

---

### WR-05: `ui-flutter-warning.spec.ts` uses `5.30 seconds` regex without escaping the dot

**File:** `e2e/tests/ui-flutter-warning.spec.ts:56`
**Issue:**

```ts
await expect(dialog).toContainText(/5.30 seconds/); // tolerates - / – / —
```

The `.` in regex matches any character. The comment claims it "tolerates -
/ – / —" but the actual pattern matches ANY single character, including
digits. `5X30 seconds` would also pass. More importantly, the spec exists
to verify the verbatim copy contract (UI-SPEC §11 commits to the en-dash
form "5–30 seconds"), and a regex this loose lets a regression slip:
e.g. if someone wrote "500 seconds" the test would still pass.

**Fix:** Use a character class:

```ts
await expect(dialog).toContainText(/5[-–—]30 seconds/);
```

Better: use one literal-substring check per acceptable form:

```ts
const text = await dialog.textContent();
expect(
  text?.includes('5–30 seconds') ||  // en-dash (the canonical form)
  text?.includes('5-30 seconds')      // hyphen (tolerated)
).toBe(true);
```

**Severity rationale:** Test assertion is too loose to catch the
regression class it nominally guards against. Not a runtime bug, but
weakens the protective contract.

---

### WR-06: Toast's `aria-live` semantics are double-stacked and may double-announce errors

**File:** `ui/src/lib/ToastContainer.svelte:34-39`, `ui/src/lib/Toast.svelte:97-101`
**Issue:** `ToastContainer` wraps all toasts in `<div role="status" aria-live="polite">`.
Inside, each `Toast` renders with `role={kind === 'error' ? 'alert' : 'status'}`.
Per ARIA spec, nested live regions are not recommended — some screen readers
(NVDA, JAWS) will announce both the outer region's update AND the inner
role=alert's assertion. UI-SPEC.md §6 explicitly says "Toast region
`<div role="status" aria-live="polite">` for success/info, `aria-live="assertive"`
for error" — implying ONE region with conditional aria-live, not nested
regions.

**Fix:** Make ToastContainer's aria-live computed from the highest-priority
toast (assertive if any error toast exists, otherwise polite). Remove
role from individual Toast components:

```svelte
<!-- ToastContainer.svelte -->
<script>
  const hasError = $derived(toasts.some((t) => t.kind === 'error'));
</script>
{#if toasts.length > 0}
  <div
    role={hasError ? 'alert' : 'status'}
    aria-live={hasError ? 'assertive' : 'polite'}
    aria-atomic="false"
    class="..."
  >
    {#each toasts as t (t.id)}
      <Toast {...t} {onDismiss} />
    {/each}
  </div>
{/if}
```

And drop `role={kind === 'error' ? 'alert' : 'status'}` from the inner
Toast's outer div.

**Severity rationale:** Mild accessibility hygiene issue; some SR users
will hear duplicate announcements but the content remains comprehensible.

---

### WR-07: `Toast.svelte` outer `<div>` with `onclick` is not keyboard-accessible

**File:** `ui/src/lib/Toast.svelte:96-102`
**Issue:**

```svelte
<div
  role={kind === 'error' ? 'alert' : 'status'}
  class="toast ..."
  ...
  onclick={handleClick}
>
```

The wrapper div has a click handler ("click anywhere on toast also dismisses")
but no `tabindex` and no key handler. Keyboard-only users cannot dismiss
the toast except via the explicit X button. UI-SPEC.md §4.6 says
"click anywhere on toast also dismisses (except interactive children)"
— that's a pointer affordance, fine in itself. But the comment in the
source (lines 89-95) acknowledges this and points users at the X button.

The actual problem is the `role="status"` / `role="alert"` with an
`onclick` handler creates a misleading semantic — assistive tech may
interpret the wrapper as interactive when it isn't keyboard-reachable.

**Fix:** Move the onclick to a dedicated overlay button or remove the
wrapper click and keep dismissal exclusively on the X button:

```svelte
<div
  role={kind === 'error' ? 'alert' : 'status'}
  class="toast ..."
>
  ...
  <!-- X button is the sole dismiss control; both pointer + keyboard. -->
  <button onclick={handleClose}>...</button>
</div>
```

If the click-anywhere affordance is mandatory, add `role="button"` and
`tabindex="0"` plus `onkeydown` handling for Enter/Space — but that
elevates the toast to interactive which conflicts with role=alert.
Better to drop the wrapper-click entirely.

**Severity rationale:** Accessibility hygiene; not a hard WCAG violation
because the X button is keyboard-reachable, but the conflicting semantic
is a code smell.

## Info

### IN-01: `App.svelte` uses `let { ..., action: _action }` to silence unused-prop warnings — destructuring rename is a Svelte 4-ism

**File:** `ui/src/lib/WarningModal.svelte:42`
**Issue:**

```ts
let { open, service, action: _action, onConfirm, onCancel }: Props = $props();
```

The rename `action: _action` is a TypeScript-friendly way to silence the
"unused variable" warning. Svelte 5 with `$props()` lets you destructure
only the props you use:

```ts
let { open, service, onConfirm, onCancel }: Props = $props();
```

This drops `action` entirely from the destructure — if it's not consumed,
why pull it out? The `Props` type still declares it; the parent still
passes it. The local just doesn't need it.

**Fix:** Drop the `action: _action` rename and remove from the destructure.

---

### IN-02: `static.go` mutates `r.URL.Path` to normalize "/index.html" to "/"

**File:** `internal/api/static.go:55`
**Issue:**

```ts
w.Header().Set("Cache-Control", "no-cache")
r.URL.Path = "/"
fileServer.ServeHTTP(w, r)
```

Mutating the inbound request's URL is a side effect that could surprise
future middleware. The comment explains why (avoiding 301 redirect that
would strip headers), which is fine, but a cleaner pattern is to clone
the request:

```ts
case clean == "/" || clean == "/index.html":
    w.Header().Set("Cache-Control", "no-cache")
    rr := r.Clone(r.Context())
    rr.URL.Path = "/"
    fileServer.ServeHTTP(w, rr)
```

`r.Clone()` is cheap (shallow copy) and avoids the side effect.

**Fix:** As above.

---

### IN-03: `CopyButton.svelte` `:global(.sr-only)` leaks to the global stylesheet

**File:** `ui/src/lib/CopyButton.svelte:131-141`
**Issue:**

```css
:global(.sr-only) {
  position: absolute;
  width: 1px;
  ...
}
```

The `:global()` selector means this class is registered globally any time
CopyButton mounts. Tailwind v4 ships `.sr-only` out of the box. If both
selectors exist, the cascade is undefined and small differences (Tailwind
uses `clip-path` plus the legacy `clip` rect) could subtly differ from
the local declaration. The comment says "Local sr-only fallback in case
Tailwind v4 doesn't ship it by default" — but Tailwind v4 does ship it.

**Fix:** Drop the `:global(.sr-only)` block; rely on Tailwind's `.sr-only`.
If Tailwind ever stops shipping it, surface a global utility in
`app.css`, not in a per-component `:global()`.

---

### IN-04: `dismissToast` filter creates a new array on every dismiss; reactivity is correct but allocation churn is high under burst

**File:** `ui/src/App.svelte:121-123`
**Issue:**

```ts
function dismissToast(id: string): void {
  toasts = toasts.filter((t) => t.id !== id);
}
```

For ~5-toast queues this is fine. A burst of 50 toasts (improbable but
possible during a script-driven multi-action sequence) means 50 filter
passes each O(n). Worth noting; not actionable today.

**Fix:** None recommended; document the assumption (small toast queue
under normal operation).

---

### IN-05: `executeAction`'s `Set` rebuild on add/remove is O(n) per action

**File:** `ui/src/App.svelte:246, 314`
**Issue:**

```ts
busyServices = new Set([...busyServices, service]);   // add
busyServices = new Set([...busyServices].filter(...)); // remove
```

The comments explain this is for "reactivity-explicit" — Svelte 5
tracks deep mutations on `$state` collections including `Set.add` /
`Set.delete`. The reassignment is legible but allocates a new Set on
every action. For ≤10 watched containers this is meaningless.

**Fix:** None required, but if a future Phase 6 surfaces a "select N
containers + bulk Update" feature, switch to in-place mutation:

```ts
busyServices.add(service);    // Svelte 5 tracks this
busyServices.delete(service);
```

---

_Reviewed: 2026-05-15T12:03:27Z_
_Reviewer: Claude (gsd-code-reviewer)_
_Depth: standard_

## REVIEW COMPLETE
