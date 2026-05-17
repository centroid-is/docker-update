<script lang="ts">
  /**
   * Table.svelte — REPLACES the Phase 1 scaffold.
   *
   * 8-column data table mapping `containers: Container[]` to <Row> instances.
   * The thead structure + empty-state row wording are preserved verbatim
   * from Phase 1 — load-bearing for ui-table.spec.ts assertions in
   * Plan 05-05.
   *
   * Props:
   *   containers    — full container slice from /api/state, after Object.values
   *   onAction      — bubbles up from each Row; App.svelte (Plan 05-04) wires
   *                   to the postAction helper
   *   busyServices  — Set of service names currently mid-flight at the UI
   *                   level; per-row `isBusy` flag derived via Set.has()
   */
  import type { Container } from './types';
  import type { ActionKind } from './ActionButton.svelte';
  import Row from './Row.svelte';

  type Props = {
    containers: Container[];
    onAction: (svc: string, kind: ActionKind) => void;
    busyServices: Set<string>;
  };

  let { containers, onAction, busyServices }: Props = $props();
</script>

<div class="overflow-x-auto rounded-md border border-[color:var(--color-border)]">
  <!--
    No width class on the table: it sizes to its content's natural
    width thanks to `whitespace-nowrap` on every cell. If that width
    fits inside the wrapper, the table appears narrower than the
    wrapper (a thin gap on the right of the bordered card — fine for
    a small-N data table). If it exceeds the wrapper width, the
    wrapper's overflow-x-auto kicks in and scrolls. This avoids the
    `min-w-full + overflow` distribution conflict that left status +
    actions columns scrolled off-screen with empty space in their
    place.
  -->
  <table>
    <thead class="bg-[color:var(--color-bg-elev)] border-b border-[color:var(--color-border)]">
      <tr>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">service</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">image</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">current</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">available</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">rollback</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">last change</th>
        <th class="px-4 py-2 text-left text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">status</th>
        <th class="px-4 py-2 text-right text-sm font-semibold whitespace-nowrap" style:color="var(--color-fg-strong)">actions</th>
      </tr>
    </thead>
    <tbody>
      {#if containers.length === 0}
        <tr>
          <td colspan="8" class="px-4 py-8 text-center text-sm italic" style:color="var(--color-fg-muted)">
            <p class="font-medium not-italic mb-2" style:color="var(--color-fg-strong)">No watched containers yet</p>
            <p>Label a service in your compose file with <code class="font-mono text-xs px-1 py-0.5 rounded" style:background="var(--color-bg-elev)">hmi-update.watch=true</code> and it will appear here on the next poll.</p>
          </td>
        </tr>
      {:else}
        {#each containers as c (c.service)}
          <Row container={c} {onAction} isBusy={busyServices.has(c.service)} />
        {/each}
      {/if}
    </tbody>
  </table>
</div>
