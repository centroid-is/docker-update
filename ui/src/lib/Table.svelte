<script lang="ts">
  import type { Container } from './types';

  type Props = { containers: Container[] };
  let { containers }: Props = $props();
</script>

<table class="w-full border border-zinc-200 rounded-md overflow-hidden">
  <thead class="bg-zinc-100 border-b border-zinc-200">
    <tr>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">container</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">image:tag</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">current digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">available digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700 font-mono text-xs">previous digest</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">status</th>
      <th class="px-4 py-2 text-left text-sm font-semibold text-zinc-700">actions</th>
    </tr>
  </thead>
  <tbody>
    {#if containers.length === 0}
      <tr>
        <td colspan="7" class="px-4 py-8 text-center text-sm text-zinc-500 italic">
          <p class="font-medium not-italic text-zinc-700 mb-2">No watched containers yet</p>
          <p>Label a service in your compose file with <code class="font-mono text-xs bg-zinc-100 px-1 py-0.5 rounded">hmi-update.watch=true</code> and it will appear here on the next poll.</p>
        </td>
      </tr>
    {:else}
      {#each containers as c (c.service)}
        <tr>
          <td class="px-4 py-2 text-sm">{c.service}</td>
          <td class="px-4 py-2 text-sm">{c.image}:{c.tag}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.current_digest ?? ''}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.update_available ? '...' : ''}</td>
          <td class="px-4 py-2 font-mono text-xs">{c.previous_digest ?? ''}</td>
          <td class="px-4 py-2 text-sm">{c.update_available ? 'update-available' : 'up-to-date'}</td>
          <td class="px-4 py-2 text-sm"><!-- Phase 5 --></td>
        </tr>
      {/each}
    {/if}
  </tbody>
</table>
