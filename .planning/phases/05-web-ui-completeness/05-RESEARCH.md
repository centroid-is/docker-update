# Phase 5 — Implementation Patterns (RESEARCH.md)

**Phase:** 05-web-ui-completeness
**Defined:** 2026-05-15
**Source:** CLAUDE.md locked stack + Svelte 5 / Vite 7 / Tailwind v4 ecosystem docs + Phase 1–4 in-repo precedent

This file collects the concrete patterns the Phase 5 plans implement against. It is descriptive (what we WILL do) not aspirational; every pattern below has a verified upstream doc citation or an in-repo precedent it extends.

---

## A. Svelte 5 runes — state, derivations, effects

### A.1 Page-level state with `$state`

```ts
// ui/src/App.svelte
let state = $state<State | null>(null);
let toasts = $state<Toast[]>([]);
let pendingAction = $state<PendingAction | null>(null);
let isActing = $state(false); // page-level "an action is in flight; pause poll"
```

- `$state` makes the variable deeply reactive. Object property mutations and array `.push` / index assignments are observed.
- Source: svelte.dev/docs/svelte/$state (May 2026 docs).
- **In-repo precedent:** `ui/src/App.svelte` Phase 1 uses `let containers = $state<Container[]>([])`. Phase 5 broadens to the full `State`.

### A.2 `$derived` for view-mapped data

```ts
const containers = $derived.by(() => Object.values(state?.containers ?? {}));
```

- `$derived.by(fn)` for derivations that need a function body (loops, conditionals).
- `$derived(expr)` for a single expression.
- Both are read-only references; they re-evaluate when the inputs change.

### A.3 `$effect` for side effects

```ts
$effect(() => {
  const poll = async () => {
    if (isActing) return;
    try {
      const r = await fetch('/api/state', { cache: 'no-store' });
      if (r.ok) state = await r.json();
    } catch { /* swallow; next tick retries */ }
  };
  poll();
  const t = setInterval(poll, 5000);
  return () => clearInterval(t);
});
```

- `$effect` runs after mount and re-runs when its tracked reads change.
- Return value from the effect = cleanup; runs on unmount or before re-execution.
- For polling, the cleanup `clearInterval` is mandatory (prevents leaked timers across HMR or future component teardown).
- Source: svelte.dev/docs/svelte/$effect.

### A.4 Props with `$props()`

```ts
type Props = { containers: Container[]; onAction: (svc: string, kind: ActionKind) => void };
let { containers, onAction }: Props = $props();
```

- `$props()` returns a destructurable object; TS types via inline `Props` type.
- **In-repo precedent:** `ui/src/lib/Table.svelte` Phase 1 already uses `let { containers }: Props = $props()`. Phase 5 extends the prop list per component.

---

## B. Tailwind v4 `@theme` + CSS variables

### B.1 Defining tokens

```css
@import "tailwindcss";

@theme {
  --color-cyan: #2aa198;
  --color-success: var(--color-cyan);
}
```

- Tailwind v4 reads `@theme` and auto-generates utility classes: `bg-cyan`, `text-success`, etc.
- CSS variables work everywhere a Tailwind utility would resolve (`bg-[color:var(--color-success)]`).
- Source: tailwindcss.com/docs/theme — May 2026 v4.3 docs.
- **No `tailwind.config.js`** — v4 abolishes the JS config file. The CSS file is the config.

### B.2 `color-mix` for alpha-mixed semantic colors

```css
border: 1px solid color-mix(in srgb, var(--color-success) 40%, transparent);
background: color-mix(in srgb, var(--color-success) 12%, transparent);
```

- `color-mix(in srgb, A X%, B (100-X)%)` is the standard CSS Color Module 5 function.
- Browser support: Safari 16.4+, Chrome 111+, Firefox 113+ — all > 99 % of HMI browsers.
- This lets us derive a single pill's text/bg/border from one Solaris hex without 3 separate tokens per state.

### B.3 `prefers-reduced-motion` + `prefers-color-scheme`

```css
@media (prefers-reduced-motion: reduce) {
  *, *::before, *::after {
    transition-duration: 0ms !important;
    animation-duration: 0ms !important;
  }
  .spinner { animation: spin 800ms linear infinite; } /* still spins — essential signal */
}
```

- Phase 5 honors reduced motion globally; spinners are explicitly exempted.

---

## C. HTTP fetch + 5 s poll

### C.1 Poll with `cache: 'no-store'`

```ts
const r = await fetch('/api/state', { cache: 'no-store' });
```

- `cache: 'no-store'` bypasses HTTP cache entirely — neither sets nor reads cached entries.
- Critical for in-place upgrade: if the browser cached `/api/state` JSON, after the binary swap the UI would render stale state until the cache TTL expired.
- Source: developer.mozilla.org/en-US/docs/Web/API/Request/cache#no-store.

### C.2 Action POST with structured error

```ts
export class ActionError extends Error {
  constructor(public status: number, public code: string, public reason: string) {
    super(`${code}: ${reason}`);
  }
}

export async function postAction(service: string, kind: ActionKind): Promise<ActionResult> {
  const r = await fetch(`/api/containers/${encodeURIComponent(service)}/${kind}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
  const body = await r.json().catch(() => ({}));
  if (!r.ok) throw new ActionError(r.status, body.error ?? 'unknown', body.reason ?? '');
  return body as ActionResult;
}
```

- `encodeURIComponent` is belt-and-braces; server-side ACT-10 regex `^[a-zA-Z0-9._-]+$` is the actual gate.
- 409 `service_busy` → re-thrown ActionError → caught in App.svelte → warning toast.
- 409 `self_protection` → never reachable from UI (UI hides own service's buttons; this is server's last-line defense).

---

## D. Cache-Control + MIME for the embedded SPA (Pitfall 8)

### D.1 Go `mime.AddExtensionType` at server boot

```go
func NewServer(...) *Server {
    mime.AddExtensionType(".js", "application/javascript")
    mime.AddExtensionType(".css", "text/css; charset=utf-8")
    mime.AddExtensionType(".svg", "image/svg+xml")
    mime.AddExtensionType(".json", "application/json")
    mime.AddExtensionType(".woff2", "font/woff2")
    // ...
}
```

- Go's default `mime.TypeByExtension(".js")` returns `text/javascript` on some platforms — which Chromium rejects with `Failed to load module script: …MIME type of "text/javascript"` when the script is loaded as `type="module"`.
- Vite emits ES modules; the HTML's `<script type="module" src="…">` is the only way the bundle loads. Wrong MIME = hard fail.
- Source: developer.chrome.com/blog/javascript-modules-mime — strict MIME enforcement for module scripts.

### D.2 `/assets/*` immutable Cache-Control

```go
// pseudo-handler
http.HandleFunc("GET /assets/", func(w http.ResponseWriter, r *http.Request) {
    f, err := assetsFS.Open(strings.TrimPrefix(r.URL.Path, "/assets/"))
    if err != nil {
        http.NotFound(w, r) // strict — never serve index.html as fallback
        return
    }
    w.Header().Set("Cache-Control", "public, immutable, max-age=31536000")
    // serve f's content
})
```

- Vite v7 emits hashed filenames (e.g., `index-abc1234.js`); changing the bundle changes the hash; old hash stays 404'd forever.
- `immutable` + `max-age=31536000` (1 year) is the canonical SPA-asset cache directive.
- Source: web.dev/articles/love-your-cache.

### D.3 `/index.html` no-cache

```go
http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/api/") /* etc */ {
        http.NotFound(w, r)
        return
    }
    w.Header().Set("Cache-Control", "no-cache")
    // serve dist/index.html
})
```

- `no-cache` (NOT `no-store`) means "you may keep the response, but re-validate every load." With an ETag this drops to a 304 on unchanged builds and a 200 on the next deploy — exactly what in-place upgrade needs.

---

## E. Svelte 5 + Vite 7 + Tailwind v4 build pipeline (already wired)

- `ui/vite.config.ts` uses `svelte()` + `tailwindcss()` plugins (Phase 1).
- Vite emits to `internal/api/dist/`; Go embeds via `//go:embed all:dist` (Phase 1).
- Phase 5 changes ZERO Vite/build config — only `ui/src/app.css` and the component tree.

---

## F. Toast queue pattern

### F.1 Append-only `$state` array

```ts
let toasts = $state<Toast[]>([]);
let nextId = 0;

function addToast(t: Omit<Toast, 'id'>): void {
  const id = `t-${++nextId}`;
  toasts = [...toasts, { id, ...t }];
  if (t.kind !== 'error') {
    setTimeout(() => dismissToast(id), 5000);
  }
}

function dismissToast(id: string): void {
  toasts = toasts.filter(t => t.id !== id);
}
```

- Reassign `toasts = [...]` (new array) rather than `toasts.push(...)`. Svelte 5 reactivity tracks the binding; in-place push works in some compiler modes but reassign is foolproof.
- `setTimeout` over a Svelte `$effect` because the timer is one-shot per toast; effect cleanup would over-complicate.

### F.2 ARIA live region

```svelte
<div role="status" aria-live="polite" class="fixed bottom-4 right-4 flex flex-col gap-2 z-50">
  {#each toasts as t (t.id)}
    <Toast {...t} onDismiss={dismissToast} />
  {/each}
</div>
```

- Use `polite` for success/info; `assertive` for error. Phase 5 splits into two stacked live regions OR uses a single polite region for v1 (operator-readable either way; SR users get announcements).

---

## G. Modal pattern (WarningModal)

### G.1 Focus trap with Svelte action

```ts
function focusTrap(node: HTMLElement) {
  const focusables = () => node.querySelectorAll<HTMLElement>(
    'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
  );
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { node.dispatchEvent(new CustomEvent('cancel')); return; }
    if (e.key !== 'Tab') return;
    const f = Array.from(focusables());
    if (f.length === 0) return;
    const first = f[0], last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) { last.focus(); e.preventDefault(); }
    else if (!e.shiftKey && document.activeElement === last) { first.focus(); e.preventDefault(); }
  }
  node.addEventListener('keydown', onKeydown);
  // initial focus
  const primary = node.querySelector<HTMLElement>('[data-primary]');
  primary?.focus();
  return { destroy() { node.removeEventListener('keydown', onKeydown); } };
}
```

- Svelte actions: `use:focusTrap` on the modal panel.
- 20 LOC, no dependency. Source: svelte.dev/docs/svelte/use (May 2026).

### G.2 Body scroll lock

```ts
$effect(() => {
  if (!open) return;
  const prev = document.body.style.overflow;
  document.body.style.overflow = 'hidden';
  return () => { document.body.style.overflow = prev; };
});
```

- Standard pattern; the cleanup restores prior overflow value.

---

## H. Clipboard write

```ts
async function copyToClipboard(value: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(value);
    return true;
  } catch {
    return false;
  }
}
```

- Modern browsers (Chrome 76+, Firefox 63+, Safari 13.1+) — covers all HMI targets.
- Playwright test must grant `clipboard-read` permission in the browser context — Phase 5's `playwright.config.ts` adds:
  ```ts
  use: {
    permissions: ['clipboard-read', 'clipboard-write'],
  },
  ```
- Read-back via `page.evaluate(() => navigator.clipboard.readText())` — verifies the copy fired.

---

## I. Relative-time formatter (in-house)

```ts
// ui/src/lib/relative-time.ts
export function relativeTime(iso: string | undefined, now: number): string {
  if (!iso) return 'never';
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return 'never';
  const s = Math.max(0, Math.floor((now - then) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s ago`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m ago`;
}
```

- No `dayjs` / `date-fns` dep — keeps the no-extra-deps ethos.
- Resolution: 1 s (header tick is 1 s).
- 12 LOC including types; trivial to unit-test in Vitest (deferred — no Vitest in Phase 1 yet; manual Playwright assertion against `/^\d+s ago$/` covers v1).

---

## J. Flutter/weston service detection

```ts
// ui/src/lib/display-warning.ts
export const DISPLAY_DRAWING_SERVICES = ['flutter', 'weston'] as const;

export function requiresWarning(service: string): boolean {
  const s = service.toLowerCase();
  return DISPLAY_DRAWING_SERVICES.some(name => s.includes(name));
}
```

- Case-insensitive substring match: `flutter-app`, `Weston-Server`, `hmi-weston` all match.
- The list is intentionally a frontend constant (CONTEXT.md decision). Phase 6 may promote to a server-side label.

---

## K. Playwright e2e — clipboard + child_process for rebuild

### K.1 Clipboard read in tests

```ts
// e2e/playwright.config.ts
export default defineConfig({
  use: {
    permissions: ['clipboard-read', 'clipboard-write'],
    // ...
  },
});

// in spec
const copied = await page.evaluate(() => navigator.clipboard.readText());
expect(copied).toBe(fullDigest);
```

### K.2 Rebuild binary mid-test (UI-10 in-place upgrade)

```ts
// e2e/fixtures/rebuild-binary.ts
import { execFile } from 'child_process';
import { promisify } from 'util';
const exec = promisify(execFile);

export async function rebuildAndRestart(): Promise<void> {
  await exec('make', ['ui'], { cwd: '..' });            // rebuilds the Svelte bundle (new hash)
  await exec('make', ['build'], { cwd: '..' });          // rebuilds Go binary with new bundle embedded
  await exec('docker', ['compose', '-f', 'e2e/compose.test.yml', 'up', '-d', '--build', '--force-recreate', 'hmi-update'], { cwd: '..' });
  // poll /healthz until 200
  for (let i = 0; i < 30; i++) {
    try {
      const r = await fetch('http://localhost:8080/healthz');
      if (r.ok) return;
    } catch {}
    await new Promise(r => setTimeout(r, 1000));
  }
  throw new Error('hmi-update did not become healthy after rebuild');
}
```

- `execFile` over `exec` — argv discipline, no shell.
- Spec uses this in a `test.beforeAll` (specific to that spec only — not globalSetup, because it's slow and scoped).

### K.3 Network interception for "no POST fired" assertion

```ts
let postCount = 0;
await page.route('**/api/containers/**', route => {
  if (route.request().method() === 'POST') postCount++;
  route.continue();
});
// ... click Cancel ...
expect(postCount).toBe(0);
```

---

## L. Go testing — handler unit tests (Plan 05-05)

```go
// internal/api/handlers_assets_test.go
func TestAssets_ImmutableCacheControl(t *testing.T) {
    s := NewServer(...)
    req := httptest.NewRequest("GET", "/assets/index-abc.js", nil)
    rr := httptest.NewRecorder()
    s.Handler().ServeHTTP(rr, req)
    if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
        t.Errorf("Cache-Control missing immutable: %q", got)
    }
    if got := rr.Header().Get("Content-Type"); got != "application/javascript" {
        t.Errorf("wrong Content-Type: %q want application/javascript", got)
    }
}

func TestAssets_StrictNoFallback(t *testing.T) {
    s := NewServer(...)
    req := httptest.NewRequest("GET", "/assets/does-not-exist.js", nil)
    rr := httptest.NewRecorder()
    s.Handler().ServeHTTP(rr, req)
    if rr.Code != http.StatusNotFound {
        t.Errorf("got %d want 404 (no index.html fallback)", rr.Code)
    }
    if body := rr.Body.String(); strings.Contains(body, "<html") {
        t.Errorf("body contains HTML — fallback to index.html happened: %q", body[:min(80, len(body))])
    }
}

func TestIndex_NoCache(t *testing.T) {
    s := NewServer(...)
    req := httptest.NewRequest("GET", "/", nil)
    rr := httptest.NewRecorder()
    s.Handler().ServeHTTP(rr, req)
    if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
        t.Errorf("got %q want no-cache", got)
    }
}
```

- Three sub-cases; table-driven if more grow.
- Phase 1 already established `httptest`-based handler tests for `/healthz` and `/api/state`.

---

## M. In-repo precedent map

| Pattern | Source file | Phase |
|---------|-------------|-------|
| Svelte 5 `$state` + `$props` | `ui/src/App.svelte`, `ui/src/lib/Table.svelte` | 1 |
| Tygo-generated TS types | `ui/src/lib/types.d.ts` | 1+2+3+4 |
| `//go:embed all:dist` + strict `/assets/*` no-fallback | `internal/api/handlers.go` | 1 |
| `mime.AddExtensionType` at boot | `internal/api/handlers.go` (Phase 1 — Phase 5 verifies + extends) | 1 |
| `httptest`-based handler tests | `internal/api/handlers_test.go` | 1 |
| RED-first Playwright e2e | `e2e/tests/smoke.spec.ts`, all Phase 2–4 specs | 1+2+3+4 |
| `e2e/globalSetup` with `docker compose up -d --wait` | `e2e/global-setup.ts` | 1 |
| `e2e/fixtures/push-image.ts` (manifest push mid-test) | `e2e/fixtures/push-image.ts` | 3 |
| Per-package sentinel errors | `internal/compose/errors.go`, `internal/registry/errors.go`, `internal/actions/errors.go` | 2+3+4 |

---

## N. Out-of-scope patterns (deferred)

- **SSE/WebSocket for state push** — V2-WEBSOCKET. 5 s `fetch` polling is sufficient on LAN.
- **State machine library (xstate, etc.)** — over-engineered for a single `pendingAction` slot.
- **Component library (skeleton.dev, shadcn-svelte)** — violates no-extra-deps ethos. UI has 9 components total.
- **CSS-in-JS** — Tailwind v4 is the styling story.
- **Vitest for unit tests of `relative-time.ts`** — Phase 5 covers via Playwright assertion. Add Vitest in v1.1 if utility helpers grow.
- **Web Components / Custom Elements** — Svelte 5 supports compiling to CEs but unnecessary for a single SPA.
- **Service Worker** — explicitly out (would interfere with in-place upgrade).
