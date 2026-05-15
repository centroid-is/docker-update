<script lang="ts">
  /**
   * App.svelte — Plan 05-04 page brain.
   *
   * Owns the single-instance UI state (per 05-CONTEXT.md Areas 5+6 — no
   * Svelte stores; everything is module-local to this component):
   *
   *   - `state`           — current `/api/state` snapshot, refreshed every
   *                          5s and after every action.
   *   - `toasts`          — append-only queue rendered by ToastContainer;
   *                          non-error entries auto-dismiss at the Toast
   *                          component (5s).
   *   - `pendingAction`   — populated when a flutter/weston row asks for
   *                          Update/Rollback; consumed by WarningModal.
   *   - `busyServices`    — Set of services with an in-flight POST;
   *                          drives the per-row spinner in ActionButton
   *                          via Table.busyServices prop, and pauses the
   *                          5s poll loop to prevent clobbering optimistic
   *                          UI (T-05-04-03).
   *
   * Polling contract (05-CONTEXT.md Area 5 + 05-RESEARCH.md §C.1):
   *   - 5 000 ms interval via $effect + setInterval; cleanup clears the
   *     timer (HMR + future teardown safety).
   *   - `cache: 'no-store'` to prevent a stale `/api/state` after an
   *     in-place upgrade (Pitfall 8 client-side leg; the server-side
   *     hardening lands in Plan 05-05).
   *   - Early-return while `isActing` is true so an in-flight action
   *     does not race against a poll that overwrites optimistic state.
   *     The server's per-service mutex (Phase 4 ACT-08) is the actual
   *     race resolver — UI gating is purely UX.
   *
   * Action wiring (handleActionRequest → executeAction):
   *   - Update/Rollback on flutter/weston routes through WarningModal
   *     first (requiresWarning gate). force-pull does NOT (no recreate
   *     in the default mode, so no display flicker).
   *   - executeAction tracks per-service busy state, calls postAction,
   *     translates ActionError into a toast keyed on HTTP status
   *     (409 service_busy → warning; everything else → error), and
   *     always re-polls /api/state on completion to converge the UI
   *     with the server's authoritative snapshot.
   *
   * Watch-now degradation (05-CONTEXT.md Area 6, T-05-04-05):
   *   - pollNow() returns a PollNowResult discriminated union; the
   *     caller routes to a plain poll() + an honest toast (info for
   *     not_implemented, warning for server_error or network) so the
   *     operator never loses the "kick the poll" affordance and never
   *     hears a misleading "endpoint not available" message on a 5xx
   *     or network blip. WR-03 in 05-REVIEW.md.
   */
  import { untrack } from 'svelte';
  import Header from './lib/Header.svelte';
  import Table from './lib/Table.svelte';
  import ToastContainer from './lib/ToastContainer.svelte';
  import WarningModal from './lib/WarningModal.svelte';
  import { requiresWarning } from './lib/display-warning';
  import {
    postAction,
    pollNow,
    ActionError,
    type ActionKind,
    type ActionResult,
  } from './lib/actions';
  import type { Toast, ToastKind } from './lib/Toast.svelte';
  import type { State, Container } from './lib/types';

  // ─── State slices (per 05-CONTEXT.md Area 5; interfaces block in PLAN.md) ───
  //
  // The /api/state snapshot is bound to `appState` (not `state`) because
  // TypeScript's parser misreads `let state = $state<...>(...)` as a
  // self-referential declaration on the rune-magic `$state` identifier
  // and emits a spurious "used before its declaration" diagnostic.
  // Renaming the local sidesteps the shadow without changing semantics.

  let appState = $state<State | null>(null);
  let toasts = $state<Toast[]>([]);
  let pendingAction = $state<{ service: string; kind: ActionKind } | null>(null);
  // busyServices is reassigned on add/remove to keep the reactivity model
  // explicit (Svelte 5 tracks deep mutations on $state collections, but a
  // reassignment is the most legible pattern for "new snapshot of the
  // in-flight set"). See executeAction below.
  let busyServices = $state<Set<string>>(new Set());
  // Monotonically increasing toast id — module-local; cheaper than crypto.randomUUID()
  // for an interaction-rate identifier. Not security-relevant.
  let nextToastId = 0;

  // ─── Derivations ───

  // isActing pauses the 5s poll loop while any per-service action is mid-flight;
  // the early-return in poll() reads this flag. busyServices.size is the source
  // of truth so adding/removing services updates isActing reactively.
  const isActing = $derived(busyServices.size > 0);

  // Object.values on the keyed map → Container[] for Table consumption.
  // Sort by service name so the row order is stable across polls regardless of
  // map iteration order; the server's state.json is a JSON object (insertion-
  // ordered in modern runtimes) but we don't rely on that.
  const containers = $derived.by<Container[]>(() =>
    Object.values(appState?.containers ?? {}).sort((a, b) =>
      a.service.localeCompare(b.service),
    ),
  );

  // Header's "Xs ago" indicator reads this directly; undefined → "never".
  const lastPollEnd = $derived(appState?.last_poll_end);

  // ─── Toast helpers ───

  /**
   * addToast appends a new toast entry. Toast.svelte handles auto-dismiss
   * (5s for non-error; sticky for error) — this helper just enqueues.
   * Reassigning the array (not push) keeps the $state reactivity model
   * explicit; Svelte 5 also tracks array mutations but the reassignment
   * is the most legible pattern.
   */
  function addToast(kind: ToastKind, title: string, body?: string): void {
    const id = `t${nextToastId++}`;
    toasts = [...toasts, { id, kind, title, body }];
  }

  /**
   * dismissToast removes a toast by id. Called by Toast.svelte's auto-
   * dismiss timer AND by the operator clicking the toast. Idempotent
   * (filtering a non-matching id is a no-op).
   */
  function dismissToast(id: string): void {
    toasts = toasts.filter((t) => t.id !== id);
  }

  // ─── Polling ───

  /**
   * poll fetches /api/state and stores the result. Pauses while isActing
   * so a mid-flight action's optimistic UI is not clobbered by a stale
   * snapshot. Network errors are swallowed silently — the operator-facing
   * signal is Header's "Xs ago" turning red as the timestamp ages
   * (driven by Header.svelte's relativeTime derivation, UI-SPEC.md §10).
   */
  async function poll(): Promise<void> {
    if (isActing) return;
    try {
      const r = await fetch('/api/state', { cache: 'no-store' });
      if (r.ok) {
        const s = (await r.json()) as State;
        appState = s;
      }
    } catch {
      // Network failure — Header.svelte's relativeTime drift is the
      // operator-facing signal. Swallow to keep the interval alive.
    }
  }

  // Kick off the first poll immediately and re-poll every 5 000 ms. Cleanup
  // clears the interval on teardown (HMR safety + threat T-05-04-03).
  //
  // Reactivity note (WR-02 in 05-REVIEW.md): poll() synchronously reads
  // isActing (a $derived over busyServices.size). If the synchronous
  // initial poll() call is invoked inside the effect's tracking scope,
  // every busyServices mutation re-runs the effect — tearing down the
  // setInterval and recreating it on every Update/Rollback click, which
  // breaks the documented 5s cadence. Wrap the initial call in
  // untrack() so the read does not register the effect as a dependent.
  // The setInterval callback runs outside the tracking boundary anyway
  // (microtask), so reads inside it never track.
  $effect(() => {
    untrack(() => {
      void poll();
    });
    const t = setInterval(() => {
      void poll();
    }, 5000);
    return () => clearInterval(t);
  });

  // ─── Header callbacks ───

  function handleRefresh(): void {
    void poll();
  }

  async function handleWatchNow(): Promise<void> {
    // pollNow now returns a structured result so we can differentiate
    // 404 (endpoint absent — info, the documented graceful-degrade
    // path from T-05-04-05) from server / network failure (warning —
    // the operator should know the kick did not land). Misleading
    // diagnostics is a worse failure mode than a generic error, so
    // each branch gets honest copy (WR-03 in 05-REVIEW.md).
    const result = await pollNow();
    if (!result.ok) {
      if (result.reason === 'not_implemented') {
        addToast(
          'info',
          'Watch now',
          'Poll-now endpoint not available; refreshed instead.',
        );
      } else if (result.reason === 'server_error') {
        addToast(
          'warning',
          'Watch now',
          'Server error triggering poll; refreshed instead.',
        );
      } else {
        addToast(
          'warning',
          'Watch now',
          'Could not reach docker-update; refreshed instead.',
        );
      }
    }
    await poll();
  }

  // ─── Action wiring ───

  /**
   * handleActionRequest is the Row → App entry point. Branches on the
   * flutter/weston gate:
   *   - Update or Rollback on a display-drawing service → open the
   *     WarningModal first by stashing { service, kind } in
   *     pendingAction. Modal Continue calls confirmAction below.
   *   - Anything else (force-pull on ANY service, OR Update/Rollback
   *     on a non-display service) → fires postAction directly.
   *
   * Note: force-pull intentionally skips the warning gate even on
   * flutter/weston. The default force-pull mode does NOT recreate the
   * container (API.md SAFE-03 carve-out), so no display flicker — the
   * warning would be a lie.
   */
  function handleActionRequest(service: string, kind: ActionKind): void {
    if (kind !== 'force-pull' && requiresWarning(service)) {
      pendingAction = { service, kind };
      return;
    }
    void executeAction(service, kind);
  }

  /**
   * confirmAction is the WarningModal's onConfirm callback. Copy the
   * pending payload to a local before clearing the state slice so a
   * second click on Continue (rare; modal closes immediately after
   * onConfirm fires) can't re-fire the same action.
   */
  function confirmAction(): void {
    const pending = pendingAction;
    pendingAction = null;
    if (pending) void executeAction(pending.service, pending.kind);
  }

  /**
   * cancelAction is the WarningModal's onCancel + Escape + backdrop-click
   * path. No POST fires; the row's busy state is never set.
   */
  function cancelAction(): void {
    pendingAction = null;
  }

  /**
   * executeAction is the single place the wire-protocol-aware logic
   * lives. Phases:
   *
   *   1. Mark the service busy (drives Row spinner; pauses poll).
   *   2. POST /api/containers/{service}/{kind} via postAction.
   *   3. On success: route to one of three toasts:
   *        a. no_op:true → info "No change needed"
   *        b. update/rollback → success "Updated svc → sha256:abc1234…"
   *           with the first 8 hex chars of result.current_digest
   *           (API.md ACT-11 guarantees current_digest on success).
   *        c. force-pull → info "Re-pulled svc"
   *   4. On ActionError: 409 service_busy → warning toast; everything
   *      else → error toast with server's reason verbatim.
   *   5. Finally: clear busy + re-poll to converge UI with server state.
   *
   * All toast text strings here are operator-facing; keep them short and
   * imperative per UI-SPEC.md §11. The verbatim server `reason` flows
   * into the toast body — the Phase 4 trust boundary (T-05-04-04) treats
   * this as an accepted exposure on a LAN-only deployment.
   */
  async function executeAction(service: string, kind: ActionKind): Promise<void> {
    // Reassign (not Set.add) so the reactivity model is explicit.
    busyServices = new Set([...busyServices, service]);
    try {
      const result: ActionResult = await postAction(service, kind);
      // Idempotent path (ACT-06 / ACT-07 — registry hasn't moved, or
      // previous_digest already equals current_digest).
      if (result.no_op) {
        addToast(
          'info',
          'No change needed',
          `${service} is already at the current digest.`,
        );
      } else if (kind === 'force-pull') {
        // force-pull-no-recreate: running container unaffected, only the
        // cached available_digest changed. Don't try to render a digest
        // diff here (it'd be misleading); a short info confirmation is
        // the right shape.
        addToast('info', 'Re-pulled', `Image cache refreshed for ${service}.`);
      } else {
        // Update or Rollback success: surface the new digest's first
        // 8 hex chars so the operator can sanity-check against the
        // registry. API.md ACT-11 guarantees current_digest is set.
        const digest = result.current_digest ?? '';
        // sha256:<64hex> — show "sha256:" + first 8 hex chars + ellipsis.
        const prefix = digest.startsWith('sha256:')
          ? digest.slice(0, 'sha256:'.length + 8) + '…'
          : digest;
        const verb = kind === 'update' ? 'Updated' : 'Rolled back';
        addToast('success', `${verb} ${service}`, prefix);
      }
    } catch (e) {
      if (e instanceof ActionError) {
        if (e.status === 409 && e.code === 'service_busy') {
          // Two-layer race defense: UI busyServices Set is the first gate;
          // the server's per-service mutex (Phase 4 ACT-08) is the second.
          // This branch fires when the user triggered an action just as a
          // cron-driven action started server-side. Warning, not error —
          // operator-actionable by waiting + retrying.
          addToast(
            'warning',
            'Service busy',
            e.reason || 'Another action is already in flight for this service.',
          );
        } else {
          // Title encodes the action kind so the toast reads naturally
          // ("Update failed" / "Rollback failed" / "Force-pull failed").
          const verb =
            kind === 'update'
              ? 'Update'
              : kind === 'rollback'
                ? 'Rollback'
                : 'Force-pull';
          addToast('error', `${verb} failed`, e.reason || e.code);
        }
      } else {
        // Network failure or unexpected throw — addToast 'error' with a
        // generic message so the operator knows the click landed but
        // the POST didn't complete cleanly.
        addToast(
          'error',
          'Network error',
          'Could not reach docker-update; check the LAN connection.',
        );
      }
    } finally {
      // Drop the service from busy regardless of outcome, then re-poll
      // so the UI converges with the server's authoritative snapshot.
      // Reassign with filter (rather than Set.delete) for the same
      // reactivity-explicit reason as the add above.
      busyServices = new Set([...busyServices].filter((s) => s !== service));
      // After the busy clear, isActing flips false and the poll proceeds.
      await poll();
    }
  }
</script>

<!-- The <noscript> fallback is required by UI-SPEC.md §12 (Claude's
     Discretion). The UI is entirely client-rendered (Svelte 5 + Vite
     build); operators with JS disabled would otherwise see a blank page.
     Single short message; intentionally minimal styling so it renders
     legibly even without app.css. -->
<noscript>
  <div style="padding: 1rem; font-family: system-ui; color: #586e75;">
    <strong>JavaScript is required.</strong>
    docker-update's web UI is a single-page Svelte app and needs JavaScript
    enabled in your browser. Re-enable it and reload the page.
  </div>
</noscript>

<Header
  lastPollEnd={lastPollEnd}
  onRefresh={handleRefresh}
  onWatchNow={handleWatchNow}
/>

<main class="max-w-screen-xl mx-auto px-6 py-6">
  <Table
    containers={containers}
    onAction={handleActionRequest}
    busyServices={busyServices}
  />
</main>

<ToastContainer toasts={toasts} onDismiss={dismissToast} />

<WarningModal
  open={pendingAction !== null}
  service={pendingAction?.service ?? ''}
  action={pendingAction?.kind === 'rollback' ? 'rollback' : 'update'}
  onConfirm={confirmAction}
  onCancel={cancelAction}
/>
