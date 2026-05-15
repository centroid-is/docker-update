<script lang="ts">
  /**
   * ToastContainer — fixed bottom-right region that hosts the page-level
   * `toasts: Toast[]` state slice (Plan 05-04 wires it from App.svelte).
   *
   * Per UI-SPEC.md §4.6:
   *   - Position: fixed bottom-right corner (16 px from edges)
   *   - Stack: flex-col, 8 px gap
   *   - z-index: above table, below modal (z-50; modal uses z-40 backdrop
   *     + z-50 panel — they share z-50 but modal panel is rendered after
   *     toasts in DOM order, so the modal stacks on top by paint order
   *     when both are visible. Acceptable since toasts during a modal
   *     should be rare; modal interactions don't fire toasts.)
   *
   * The container is the SINGLE aria-live region (WR-06 in 05-REVIEW.md):
   *   - role/aria-live derived from the highest-priority toast — assertive
   *     on error, polite otherwise. UI-SPEC.md §6 calls for "one region
   *     with conditional aria-live", not nested regions. The inner Toast
   *     components carry no role so screen readers (NVDA, JAWS) do not
   *     double-announce. 05-RESEARCH.md §F.2.
   *
   * If `toasts` is empty the container renders nothing — no empty region
   * eating clicks, no announcement region.
   */
  import Toast, { type Toast as ToastEntry } from './Toast.svelte';

  type Props = {
    toasts: ToastEntry[];
    onDismiss: (id: string) => void;
  };

  let { toasts, onDismiss }: Props = $props();

  // Highest-priority kind drives the live-region semantics. Any error
  // in the queue elevates the entire region to assertive; otherwise the
  // polite default reads new entries during the next idle moment.
  const hasError = $derived(toasts.some((t) => t.kind === 'error'));
</script>

{#if toasts.length > 0}
  <div
    role={hasError ? 'alert' : 'status'}
    aria-live={hasError ? 'assertive' : 'polite'}
    aria-atomic="false"
    class="fixed bottom-4 right-4 flex flex-col gap-2 z-50 pointer-events-none"
  >
    {#each toasts as t (t.id)}
      <Toast id={t.id} kind={t.kind} title={t.title} body={t.body} {onDismiss} />
    {/each}
  </div>
{/if}
