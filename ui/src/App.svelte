<script lang="ts">
  import { onMount } from 'svelte';
  import Table from './lib/Table.svelte';
  import type { State, Container } from './lib/types';

  let containers = $state<Container[]>([]);

  onMount(async () => {
    try {
      const res = await fetch('/api/state');
      if (res.ok) {
        const s: State = await res.json();
        containers = Object.values(s.containers ?? {});
      }
    } catch {
      // empty list is fine for Phase 1
    }
  });
</script>

<header class="bg-zinc-100 border-b border-zinc-200 px-6 py-4">
  <div class="max-w-screen-xl mx-auto flex items-center justify-between">
    <h1 class="text-2xl font-semibold tracking-tight">hmi-update</h1>
  </div>
</header>

<main class="max-w-screen-xl mx-auto px-6 py-8">
  <Table containers={containers} />
</main>
