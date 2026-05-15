<script lang="ts">
  /**
   * Row.svelte — single <tr> for one Container.
   *
   * Owns:
   *   - $derived `status: StatusKind` per 05-CONTEXT.md Area 2 priority:
   *       action_in_flight > action_error > pinned > stopped > update_available > current
   *   - Label gating:
   *       hmi-update.allow-update   = "false" → hide Update button, show lock
   *       hmi-update.allow-rollback = "false" → hide Rollback button, show lock
   *       Force-pull is NEVER label-gated (matches Phase 4 semantic;
   *       force-pull is read-only with respect to the running container).
   *   - Pinned gating:
   *       container.pinned === true → hide all three action buttons,
   *       render a single lock icon + "pinned: opt-out" tooltip.
   *   - Rollback availability:
   *       no previous_digest → Rollback button is disabled (server returns
   *       no-op anyway, but UI signals it upfront).
   *
   * `isBusy` reflects "an action on this row is in flight" at the page
   * level; when true, all three action buttons enter their busy state
   * (spinner; cursor-progress) to prevent the double-click race
   * (Pitfall 11 UX side). Server-side mutex is the authoritative gate.
   */
  import type { Container } from './types';
  import StatusBadge, { type StatusKind } from './StatusBadge.svelte';
  import ActionButton, { type ActionKind } from './ActionButton.svelte';
  import CopyButton from './CopyButton.svelte';

  type Props = {
    container: Container;
    onAction: (svc: string, kind: ActionKind) => void;
    isBusy: boolean;
  };

  let { container, onAction, isBusy }: Props = $props();

  // Whitelist of action_in_flight values the server is allowed to emit
  // for the "in-flight" badge tier. types.d.ts declares the wire field
  // as `string`, so a bare cast to StatusKind would let any string —
  // empty, garbage, a new server-side value from a future phase — fall
  // through StatusBadge's switch and render with `var(undefined)` as
  // the pill color (black on cream, no operator-protective contrast).
  // Validate at the trust boundary; fall back to 'current' on miss.
  // WR-01 in 05-REVIEW.md.
  const IN_FLIGHT_KINDS = new Set<StatusKind>([
    'updating',
    'rolling_back',
    'force_pulling',
  ]);

  // Status derivation — priority order is load-bearing and pinned to
  // 05-CONTEXT.md Area 2. Keep in this exact sequence; any reordering
  // changes the operator-visible badge for ambiguous states.
  const status = $derived.by<StatusKind>(() => {
    const inFlight = container.action_in_flight;
    if (inFlight) {
      // Whitelist the wire value against the known in-flight StatusKinds
      // (WR-01). On miss, log a console.warn so operators / log
      // collectors notice an unexpected server-side value and fall
      // through to the next priority tier (action_error → pinned →
      // stopped → update_available → current).
      if (IN_FLIGHT_KINDS.has(inFlight as StatusKind)) {
        return inFlight as StatusKind;
      }
      console.warn(
        `Row.svelte: unexpected action_in_flight=%o on service=%o; falling back to default tier.`,
        inFlight,
        container.service,
      );
    }
    if (container.action_error)     return 'action_error';
    if (container.pinned)           return 'pinned';
    if (container.stopped)          return 'stopped';
    if (container.update_available) return 'update_available';
    return 'current';
  });

  // Safety-label gates. The labels map is `omitempty`; absence === permitted.
  // The `!== 'false'` form means any value other than literal "false" (including
  // unset, "true", "yes", garbage) leaves the action enabled. This matches the
  // server-side Phase 4 SAFE-01/02 semantic — the server is authoritative; the
  // UI's check is UX-only to surface the lock icon and prevent the user from
  // firing a POST that will 409 anyway.
  const allowUpdate   = $derived(container.labels?.['hmi-update.allow-update']   !== 'false');
  const allowRollback = $derived(container.labels?.['hmi-update.allow-rollback'] !== 'false');

  const hasPrevious = $derived(!!container.previous_digest);

  // Short-form digest: 19 chars = "sha256:" (7) + first 12 hex = matches
  // Phase 1 + UI-SPEC.md §4.2 "12-char prefix" semantics on a sha256-prefixed
  // digest string. Use slice (not substring) for clarity.
  function shortDigest(d: string | undefined): string {
    if (!d) return '';
    return d.length > 19 ? `${d.slice(0, 19)}…` : d;
  }

  // Display tag falls back to "latest" when omitempty stripped the field.
  const imageTag = $derived(`${container.image ?? ''}:${container.tag ?? 'latest'}`);

  function fire(kind: ActionKind) {
    onAction(container.service, kind);
  }
</script>

<tr class="border-t border-[color:var(--color-border)] hover:bg-[color:var(--color-bg-elev)]/40">
  <!-- container service -->
  <td class="px-4 py-2.5 text-sm">{container.service}</td>

  <!-- image:tag -->
  <td class="px-4 py-2.5 text-sm">{imageTag}</td>

  <!-- current digest -->
  <td class="px-4 py-2.5">
    {#if container.current_digest}
      <span class="inline-flex items-center gap-1.5">
        <span class="font-mono text-xs">{shortDigest(container.current_digest)}</span>
        <CopyButton value={container.current_digest} label="current digest" />
      </span>
    {/if}
  </td>

  <!-- available digest -->
  <td class="px-4 py-2.5">
    {#if container.available_digest}
      <span class="inline-flex items-center gap-1.5">
        <span class="font-mono text-xs">{shortDigest(container.available_digest)}</span>
        <CopyButton value={container.available_digest} label="available digest" />
      </span>
    {/if}
  </td>

  <!-- previous digest -->
  <td class="px-4 py-2.5">
    {#if container.previous_digest}
      <span class="inline-flex items-center gap-1.5">
        <span class="font-mono text-xs">{shortDigest(container.previous_digest)}</span>
        <CopyButton value={container.previous_digest} label="previous digest" />
      </span>
    {/if}
  </td>

  <!-- status -->
  <td class="px-4 py-2.5">
    <StatusBadge {status} errorReason={container.action_error} />
  </td>

  <!-- actions -->
  <td class="px-4 py-2.5 text-right">
    {#if container.pinned}
      <!-- Pinned: opt-out. Single lock icon with tooltip; no action buttons. -->
      <span
        class="inline-flex items-center justify-end gap-1 text-xs"
        style:color="var(--color-fg-muted)"
        title="pinned: opt-out"
        aria-label="Actions disabled — container is pinned (opt-out)"
      >
        <svg
          class="h-4 w-4"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          aria-hidden="true"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M16.5 10.5V6.75a4.5 4.5 0 1 0-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 0 0 2.25-2.25v-6.75a2.25 2.25 0 0 0-2.25-2.25H6.75a2.25 2.25 0 0 0-2.25 2.25v6.75a2.25 2.25 0 0 0 2.25 2.25Z"
          />
        </svg>
      </span>
    {:else}
      <div class="inline-flex items-center justify-end gap-1">
        <!-- Update — hidden + lock icon when hmi-update.allow-update=false;
             disabled (40% opacity + cursor-not-allowed via ActionButton's
             :disabled style) when no update is available. The server's
             orchestrator is the authoritative gate; this is a UX hint to
             prevent the user from firing a POST that will land in a no-op. -->
        {#if allowUpdate}
          <ActionButton
            kind="update"
            service={container.service}
            disabled={!container.update_available}
            busy={isBusy}
            onClick={() => fire('update')}
          />
        {:else}
          <span
            class="inline-flex items-center justify-center h-7 w-7"
            style:color="var(--color-fg-muted)"
            title="Update disabled by hmi-update.allow-update=false"
            aria-label={`Update ${container.service} disabled by hmi-update.allow-update=false`}
          >
            <svg
              class="h-3.5 w-3.5"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M16.5 10.5V6.75a4.5 4.5 0 1 0-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 0 0 2.25-2.25v-6.75a2.25 2.25 0 0 0-2.25-2.25H6.75a2.25 2.25 0 0 0-2.25 2.25v6.75a2.25 2.25 0 0 0 2.25 2.25Z"
              />
            </svg>
          </span>
        {/if}

        <!-- Rollback — hidden + lock when allow-rollback=false; disabled (not hidden) when no previous_digest -->
        {#if allowRollback}
          <ActionButton
            kind="rollback"
            service={container.service}
            disabled={!hasPrevious}
            busy={isBusy}
            onClick={() => fire('rollback')}
          />
        {:else}
          <span
            class="inline-flex items-center justify-center h-7 w-7"
            style:color="var(--color-fg-muted)"
            title="Rollback disabled by hmi-update.allow-rollback=false"
            aria-label={`Rollback ${container.service} disabled by hmi-update.allow-rollback=false`}
          >
            <svg
              class="h-3.5 w-3.5"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.5"
              aria-hidden="true"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                d="M16.5 10.5V6.75a4.5 4.5 0 1 0-9 0v3.75m-.75 11.25h10.5a2.25 2.25 0 0 0 2.25-2.25v-6.75a2.25 2.25 0 0 0-2.25-2.25H6.75a2.25 2.25 0 0 0-2.25 2.25v6.75a2.25 2.25 0 0 0 2.25 2.25Z"
              />
            </svg>
          </span>
        {/if}

        <!-- Force-pull — never label-gated; always rendered. -->
        <ActionButton
          kind="force-pull"
          service={container.service}
          disabled={false}
          busy={isBusy}
          onClick={() => fire('force-pull')}
        />
      </div>
    {/if}
  </td>
</tr>
