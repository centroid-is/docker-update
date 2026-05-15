<script lang="ts">
  /*
   * App.svelte — Plan 05-02 transitional shell.
   *
   * Plan 05-02 ships the visual component tree (Header, Table, Row,
   * StatusBadge, ActionButton, CopyButton). Plan 05-04 will replace this
   * shell with full polling + action wiring + Toast + WarningModal hosting.
   * Until then App.svelte uses the new Header + Table with placeholder
   * callbacks so the build stays runnable and a manual visual smoke works.
   *
   * Placeholders here are deliberately inert (no-op callbacks, empty
   * busyServices). Plan 05-04 replaces this file in full.
   */
  import { onMount } from 'svelte';
  import Header from './lib/Header.svelte';
  import Table from './lib/Table.svelte';
  import type { ActionKind } from './lib/ActionButton.svelte';
  import type { State, Container } from './lib/types';

  let containers = $state<Container[]>([]);
  let lastPollEnd = $state<string | undefined>(undefined);
  // Empty Set — Plan 05-04 populates as actions fire.
  const busyServices = new Set<string>();

  // Inert callbacks; Plan 05-04 replaces with postAction + poll triggers.
  function noopAction(_svc: string, _kind: ActionKind) {
    // intentional no-op — wired in Plan 05-04
  }
  function noopRefresh() {
    // intentional no-op — wired in Plan 05-04
  }
  function noopWatchNow() {
    // intentional no-op — wired in Plan 05-04
  }

  onMount(async () => {
    try {
      const res = await fetch('/api/state');
      if (res.ok) {
        const s: State = await res.json();
        containers = Object.values(s.containers ?? {});
        lastPollEnd = s.last_poll_end;
      }
    } catch {
      // empty list is fine for Phase 5 Plan 05-02 transitional shell
    }
  });
</script>

<Header {lastPollEnd} onRefresh={noopRefresh} onWatchNow={noopWatchNow} />

<main class="max-w-screen-xl mx-auto px-6 py-6">
  <Table containers={containers} onAction={noopAction} {busyServices} />
</main>
