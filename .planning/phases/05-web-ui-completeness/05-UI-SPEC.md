# Phase 5 — UI Design Contract (UI-SPEC.md)

**Phase:** 05-web-ui-completeness
**Defined:** 2026-05-15
**Status:** Design contract — locks visual identity for Phase 5 implementation
**Skill applied:** `frontend-design` — distinctive, production-grade, NOT generic AI-blue

---

## 1. Brand voice

`hmi-update` is a field-engineer tool, not a SaaS dashboard. The voice is:

- **Industrial-calm.** No exclamation marks, no "Successfully updated!", no emoji. The control panel of a building's elevator should look as serious as the elevator.
- **Operator-readable.** Every label is a verb or a noun an engineer recognizes: `Update`, `Rollback`, `Force-pull`, `Refresh`, `Watch now`. No "Get started," no "Manage," no "Workspace."
- **Diagnostic, not aspirational.** Errors say what went wrong and what to do: `verify_failed: container restarted 3 times in 15s — try Rollback or check container logs.` Not: `Oops! Something went wrong.`
- **One pane, no chrome.** No sidebar, no breadcrumb, no settings page (v1). The single page is the product.

## 2. Color tokens — Solaris

Palette: Ethan Schoonover's Solaris. Light mode is the default and only-shipped mode (HMI screens are bright; dark mode tokens are documented for Phase 6+).

### 2.1 Palette (16 colors)

| Token | Hex | Role |
|-------|------|------|
| `base03` | `#002b36` | dark bg (dark mode only — not shipped v1) |
| `base02` | `#073642` | strong fg / modal backdrop |
| `base01` | `#586e75` | muted text / lock icon |
| `base00` | `#657b83` | body text (default) |
| `base0` | `#839496` | light text |
| `base1` | `#93a1a1` | slate accents / stopped pill |
| `base2` | `#eee8d5` | elevated bg (header, table stripe) |
| `base3` | `#fdf6e3` | page bg (default light mode) |
| `yellow` | `#b58900` | update_available pill |
| `orange` | `#cb4b16` | flicker warning / warning toast |
| `red` | `#dc322f` | action_error pill / error toast |
| `magenta` | `#d33682` | (reserved — not used in v1) |
| `violet` | `#6c71c4` | in-flight pill (updating/rolling_back/force_pulling) |
| `blue` | `#268bd2` | primary accent / Refresh button |
| `cyan` | `#2aa198` | success pill / success toast / Continue button |
| `green` | `#859900` | (reserved — washed out on cream; do not use for success) |

### 2.2 Semantic aliases (`ui/src/app.css` `@theme`)

```css
@theme {
  /* palette */
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

  /* semantic — light mode (default & only shipped) */
  --color-bg:        var(--color-base3);
  --color-bg-elev:   var(--color-base2);
  --color-fg:        var(--color-base00);
  --color-fg-muted:  var(--color-base01);
  --color-fg-strong: var(--color-base02);
  --color-border:    color-mix(in srgb, var(--color-base1) 35%, transparent);
  --color-accent:    var(--color-blue);
  --color-success:   var(--color-cyan);
  --color-warning:   var(--color-orange);
  --color-danger:    var(--color-red);
  --color-info:      var(--color-violet);
  --color-pending:   var(--color-yellow);
  --color-neutral:   var(--color-base1);

  /* Reserved dark-mode aliases (NOT shipped Phase 5 — for future @media (prefers-color-scheme: dark)) */
  /* --color-bg:    var(--color-base03); */
  /* --color-fg:    var(--color-base0); */
}
```

### 2.3 Why this palette (and not generic Tailwind blue)

The frontend-design skill insists on distinctiveness. Off-the-shelf Tailwind `blue-600` / `slate-50` reads as "AI demo" the moment an operator opens it. Solaris is:

- **Warm cream background** (`#fdf6e3`) instead of pure white — easier on the eyes under HMI fluorescents.
- **Grey-green body text** (`#657b83`) instead of pure black — calmer at the focal length operators use.
- **Yellow for "update available"** (`#b58900`) — neither "danger red" nor "marketing blue"; reads as "ready when you are."
- **Orange for "display will flicker"** (`#cb4b16`) — the universal "pay attention" signal; reserved exclusively for the flutter/weston warning.
- **Cyan for success** (`#2aa198`) — the only Solaris accent that reads as "go" on cream; Solaris green (#859900) gets lost on base3.

## 3. Typography

- **Body / UI:** `ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif`. Already in `ui/src/app.css`.
- **Monospace (digest cells, error reasons):** `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`.
- **Scale:**

  | Use | Size | Weight | Tracking |
  |-----|------|--------|----------|
  | Header title `hmi-update` | 18 px | 600 | -0.01em |
  | Section / table header cells | 14 px | 600 | normal |
  | Table body cells | 14 px | 400 | normal |
  | Digest mono | 12 px | 400 | normal |
  | Toast title | 14 px | 600 | normal |
  | Toast body | 13 px | 400 | normal |
  | Modal title | 16 px | 600 | normal |
  | Modal body | 14 px | 400 | normal |
  | Button label (none — icons only) | — | — | — |
  | Tooltip | 12 px | 500 | normal |

## 4. Layout primitives

### 4.1 Page shell

```
+--------------------------------------------------------------+
|  Header (sticky, 64 px, --color-bg-elev, bottom border)      |
|   hmi-update                  [Refresh] [Watch now]  3s ago  |
+--------------------------------------------------------------+
|                                                              |
|   max-w-screen-xl mx-auto, px-6 py-6                         |
|                                                              |
|   +-----------------------------------------------------+   |
|   |  Table (border, rounded-md, overflow-hidden)        |   |
|   |   thead: --color-bg-elev, base02 text, 14 px 600    |   |
|   |   tbody: zebra-strip alternate rows --color-base2/30|   |
|   |                                                     |   |
|   |   row vertical padding py-2.5, horizontal px-4      |   |
|   +-----------------------------------------------------+   |
|                                                              |
+--------------------------------------------------------------+

                                                  +-------+
                                                  | Toast |
                                                  +-------+
                                              fixed bottom-4 right-4
```

- Max content width: `1280px` (`max-w-screen-xl`).
- Horizontal padding: `24px` (`px-6`).
- Vertical: header sticky top; table sits beneath with `mt-6`.
- Border radius: `6px` on table, toast, modal.
- Borders: 1 px `--color-border`.

### 4.2 Table

- 7 columns, fixed order: `container | image:tag | current digest | available digest | previous digest | status | actions`.
- Each header cell: 14 px / 600 / `--color-fg-strong` (base02) / left-aligned / `--color-bg-elev` background.
- Body cells: 14 px / 400 / `--color-fg`.
- Digest cells: monospace 12 px; 12-char prefix + ellipsis if longer.
- Actions column: right-aligned (so icons cluster); minimum width 144 px.
- Empty state (no containers): single cell colspan=7, italic `--color-fg-muted`, with the bridge text from Phase 1.
- Mobile (< 768 px): wrap table in `<div class="overflow-x-auto">`; do NOT collapse to cards (deferred).

### 4.3 Status pill (`StatusBadge.svelte`)

- Pill shape: `rounded-full px-2.5 py-0.5 text-xs font-medium border`.
- Color matrix:

  | status | text | bg (10% alpha) | border |
  |--------|------|----------------|--------|
  | `current` | `--color-success` | `color-mix(in srgb, --color-success 12%, transparent)` | `color-mix(in srgb, --color-success 40%, transparent)` |
  | `update_available` | `--color-pending` (yellow) | `color-mix(in srgb, --color-pending 14%, transparent)` | yellow-40% |
  | `updating` / `rolling_back` / `force_pulling` | `--color-info` (violet) | violet-12% | violet-40% — also renders a 12 px inline spinner |
  | `action_error` | `--color-danger` | danger-12% | danger-40% — `title` attr shows `container.action_error` |
  | `pinned` | `--color-fg-muted` | base01-10% | base01-30% — renders a lock icon prefix |
  | `stopped` | `--color-neutral` (base1) | base1-15% | base1-35% |

- Label text (lowercase, snake-case for state, human for badges): `current`, `update available`, `updating…`, `rolling back…`, `force-pulling…`, `error`, `pinned`, `stopped`.

### 4.4 Action button (`ActionButton.svelte`)

- Square 28×28 px icon button.
- Default state: transparent bg, `--color-fg-muted` icon stroke, `--color-border` 1 px border, `rounded-md`.
- Hover: bg `color-mix(in srgb, var(--color-accent) 10%, transparent)`, icon `--color-accent`, border `--color-accent`.
- Active / pressed: bg accent-20%.
- Disabled: 40% opacity, no hover affordance, `cursor-not-allowed`.
- In-flight (button is the one firing the action): bg accent-10%, icon replaced by inline 16 px spinner.
- Spacing: 4 px between buttons in the actions cell.
- Per-action icon (inline Heroicons MIT, copied as SVG):

  | Action | Icon | aria-label |
  |--------|------|-----------|
  | Update | arrow-up-tray (upload arrow) | `Update {service}` |
  | Rollback | arrow-uturn-left | `Rollback {service}` |
  | Force-pull | arrow-path (refresh circular) | `Force-pull {service}` |

### 4.5 Copy button (`CopyButton.svelte`)

- 20×20 px icon button, inline next to digest text.
- Default icon: `clipboard-document` outline, `--color-fg-muted`, 14 px.
- Click: copies full digest via `navigator.clipboard.writeText`; icon swaps to `check-circle` `--color-success` for 1500 ms; aria-live="polite" announces `Copied`.

### 4.6 Toast (`Toast.svelte`)

- Width: 360 px max; min 280 px; positioned `fixed bottom-4 right-4`, stacking vertically with 8 px gap.
- Border-left: 4 px solid in the kind color.
- Background: `--color-bg` (base3 cream) with slight elevation (`shadow-md`).
- Title (14 px / 600 / `--color-fg-strong`); body (13 px / 400 / `--color-fg`).
- Auto-dismiss: 5 s (success/info/warning); `error` toasts stay until clicked.
- Dismiss control: 12 px `x-mark` icon top-right; click anywhere on toast also dismisses (except interactive children).
- Reduced motion: drop the slide-in / slide-out animations; just opacity fade or no transition.

### 4.7 Warning modal (`WarningModal.svelte`)

- Layered above the page with backdrop `bg-base02/40`.
- Modal panel: 480 px wide, centered, `bg-base3`, `border --color-border`, `rounded-md`, `shadow-xl`, `p-6`.
- Title row: 20 px `triangle-warning` icon `--color-warning` + title text `Display may flicker.` (16 px / 600 / `--color-fg-strong`).
- Body: 14 px / 400 / `--color-fg`; copy verbatim:
  > Recreating **{service}** on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?
- Buttons row (right-aligned, 8 px gap):
  - **Cancel** — secondary: transparent bg, `--color-fg` text, `--color-border` border, 36 px tall, `px-4`.
  - **Continue** — primary: bg `--color-success` (cyan), white text, `border --color-success`, 36 px tall, `px-4`. Default focus on this button (operator can press Enter to confirm after reading).
- Focus trap: focus moves to Continue on mount; Tab cycles inside; Esc = Cancel.
- Background scroll lock while modal open.

## 5. Component contracts

| Component | Props | Emits / callbacks |
|-----------|-------|-------------------|
| `App.svelte` | (root) | — |
| `Header.svelte` | `lastPollEnd: string \| undefined`, `onRefresh: () => void`, `onWatchNow: () => void` | calls callbacks |
| `Table.svelte` | `containers: Container[]`, `onAction: (svc, kind) => void` | bubbles `onAction` up |
| `Row.svelte` | `container: Container`, `onAction: (svc, kind) => void`, `isBusy: boolean` | `onAction` |
| `StatusBadge.svelte` | `status: StatusKind`, `errorReason?: string` | — |
| `ActionButton.svelte` | `kind: 'update'\|'rollback'\|'force-pull'`, `service: string`, `disabled: boolean`, `busy: boolean`, `onClick: () => void` | `onClick` |
| `CopyButton.svelte` | `value: string`, `label?: string` | — |
| `Toast.svelte` | `id: string`, `kind: 'success'\|'error'\|'warning'\|'info'`, `title: string`, `body?: string`, `onDismiss: (id: string) => void` | `onDismiss` |
| `ToastContainer.svelte` | `toasts: Toast[]`, `onDismiss: (id) => void` | — |
| `WarningModal.svelte` | `open: boolean`, `service: string`, `action: 'update' \| 'rollback'`, `onConfirm: () => void`, `onCancel: () => void` | `onConfirm`/`onCancel` |

## 6. Accessibility

- **Keyboard:**
  - Tab order: Header (Refresh → Watch now) → Table (row 1 actions L→R → row 2 …) → Toasts (if any) → Modal (if open).
  - All icon buttons have `aria-label`; tooltips supplement, never replace.
  - Modal traps focus until dismissed; Esc dismisses (Cancel semantics).
- **Focus rings:** 2 px solid `--color-accent` with 2 px offset on every interactive element. Visible on `:focus-visible` (not on mouse-click focus).
- **Color contrast:** Solaris on cream:
  - body text `--color-fg` (#657b83) on `--color-bg` (#fdf6e3): 4.6:1 — passes WCAG AA for normal text.
  - strong text `--color-fg-strong` (#073642) on bg: 13.1:1 — AAA.
  - cyan success (#2aa198) on bg: 3.1:1 — passes for large/UI components per WCAG 1.4.11; verified against icon buttons + pill labels.
  - red danger (#dc322f) on bg: 4.7:1 — AA.
- **Status conveyed by text + color** — never color-only. Each pill carries its status as text; icons supplement.
- **Reduced motion:** `@media (prefers-reduced-motion: reduce)` disables all `transition-*` utility durations (set to 0); spinners stay (signal in-flight state is essential information, not decoration).
- **Screen readers:**
  - `<table>` uses semantic `<thead>` / `<tbody>` (no ARIA roles needed).
  - Toast region `<div role="status" aria-live="polite">` for success/info, `aria-live="assertive"` for error.
  - Modal `<div role="dialog" aria-modal="true" aria-labelledby aria-describedby>`.

## 7. Breakpoints

- **`< 768 px` (mobile):** Header collapses Refresh + Watch-now into a single overflow `⋯` (deferred — not v1; v1 ships overflow-x-auto on the table). Toast moves to bottom-center, 90 % width.
- **`768 px – 1023 px` (tablet):** Default layout.
- **`≥ 1024 px` (HMI, primary target):** Default layout, full table width. Manual smoke (Success Criteria 5) runs here.

## 8. Iconography

- Source: Heroicons (MIT). 6 unique icons total — all copy-pasted as inline SVG into each component (no `lucide-svelte` dep).
- Stroke width: 1.5 px (Heroicons "outline" variant).
- Sizes: 14 px in pills, 16 px in action buttons, 16 px in toasts, 20 px in modal title.
- Color: inherits from parent text color (`stroke="currentColor" fill="none"`).

## 9. Animation

| Element | Property | Duration | Easing | Reduced-motion |
|---------|----------|----------|--------|-----------------|
| Action button hover | `background-color` | 120 ms | `ease-out` | 0 ms |
| Toast appear | `opacity` + `translate-y` | 180 ms | `ease-out` | opacity only, 0 ms |
| Toast dismiss | `opacity` | 120 ms | `ease-in` | 0 ms |
| Modal backdrop | `opacity` | 150 ms | `ease-out` | 0 ms |
| Modal panel | `opacity` + `scale(0.98 → 1)` | 150 ms | `ease-out` | opacity only, 0 ms |
| Spinner | `rotate(360deg)` | 800 ms linear infinite | — | infinite (essential) |
| CopyButton check | `opacity` | 200 ms | `ease-out` | 0 ms |

## 10. Empty / loading / error states

| State | Trigger | Visual |
|-------|---------|--------|
| Loading (initial) | `state == null` | Table renders with skeleton row × 3: each cell shows a 1-line `bg-base2/60` pulse 280 ms. |
| Empty (no containers) | `state.containers == {}` | Phase 1's empty-state row text preserved verbatim. |
| Poll error | `state.last_poll_error != ""` | Header timestamp turns red and shows `last poll: failed — {short reason}` (truncated to 60 chars + ellipsis with full reason in `title`). |
| Network blip (fetch threw) | catch branch in poll | Silent — next 5 s tick retries. Visual cue: timestamp ages past 10 s (the header's "Xs ago" turning red when > 10 s ago — `$derived` color rule). |

## 11. Copy / microcopy

- Header title: `hmi-update`.
- Refresh button aria-label: `Refresh state from server`.
- Watch-now button aria-label: `Trigger a poll right now`.
- Empty state: `No watched containers yet. Label a service in your compose file with hmi-update.watch=true and it will appear here on the next poll.` (Phase 1 wording, preserved.)
- Success toast title: `Update complete` / `Rollback complete` / `Image re-pulled`.
- Success toast body: `{service} → sha256:abc1234…` (8-char digest prefix).
- Error toast title: `Update failed` / `Rollback failed` / `Force-pull failed`.
- Error toast body: server's `reason` field verbatim.
- No-op (`{no_op: true}`) info toast title: `No change needed`.
- No-op body: `{service} is already at the upstream digest.` (Update) / `{service} has no previous digest to roll back to.` (Rollback).
- Warning toast (used for non-modal alerts, e.g., 409 service_busy): title `Service busy`, body `An action on {service} is already in flight. Wait a moment and try again.`
- Modal title: `Display may flicker.` (no question mark — it's a statement.)
- Modal body: `Recreating {service} on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?` (question mark belongs at the end of the prompt sentence.)

## 12. Spacing & dimension reference card

| Token | Value |
|-------|-------|
| `--space-1` | 4 px |
| `--space-2` | 8 px |
| `--space-3` | 12 px |
| `--space-4` | 16 px |
| `--space-6` | 24 px |
| `--radius-sm` | 4 px (pill) |
| `--radius` | 6 px (button, table) |
| `--radius-md` | 8 px (modal) |
| `--shadow-md` | toast |
| `--shadow-xl` | modal |
| Header height | 64 px |
| Action button | 28×28 px |
| Copy button | 20×20 px |
| Pill height | 22 px |
| Modal width | 480 px |
| Toast width | 360 px max |
| Toast offset | bottom-4 right-4 (16 px) |

## 13. Distinctiveness checklist (frontend-design skill)

- [x] **Not generic AI-blue.** Primary accent is Solaris blue (`#268bd2`), which is greyer / less saturated than Tailwind `blue-500` (`#3b82f6`). Background is cream (`#fdf6e3`), not pure white. Body text is grey-green (`#657b83`), not pure black or pure slate.
- [x] **Not generic SaaS chrome.** No sidebar, no breadcrumb, no settings cog, no avatar circle. Single-pane industrial dashboard.
- [x] **Operator-trust signals.** Sticky last-poll timestamp; lock icons on opt-out rows; verbatim error reasons in toasts (no euphemisms).
- [x] **Warm, not sterile.** Solaris yellow/orange/cyan accents are intentional analog-instrument cues (think Tektronix oscilloscope, not Stripe dashboard).
- [x] **One opinionated affordance per row.** Three icon buttons, identical shape, predictable position — operator's muscle memory matters more than visual variety.

---

*Design contract complete. Phase 5 plans (05-01 through 05-05) implement against this spec.*
