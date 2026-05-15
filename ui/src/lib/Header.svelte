<script lang="ts">
  /**
   * Header.svelte — sticky top page header.
   *
   * Renders:
   *   - title "docker-update"
   *   - [Refresh] button — calls onRefresh()
   *   - [Watch now] button — calls onWatchNow()  (App.svelte degrades to
   *     Refresh when /api/poll-now is not implemented; see 05-CONTEXT.md
   *     Area 6)
   *   - last-poll relative timestamp ("3s ago" / "never") that ticks every
   *     1 s via a local $state(now). Effect cleanup clears the interval to
   *     prevent timer leaks across HMR or future teardown (threat
   *     T-05-02-03 mitigation).
   *
   * Per UI-SPEC.md §4.1 (page shell) the header is sticky 64 px,
   * --color-bg-elev background, 1 px bottom border, max-w-screen-xl
   * content row.
   *
   * Header buttons are labeled text (not icon-only ActionButton style) —
   * UI-SPEC.md §11 microcopy: aria-label "Refresh state from server" /
   * "Trigger a poll right now"; visible text "Refresh" / "Watch now".
   */
  import { relativeTime } from './relative-time';

  type Props = {
    lastPollEnd: string | undefined;
    onRefresh: () => void;
    onWatchNow: () => void;
  };

  let { lastPollEnd, onRefresh, onWatchNow }: Props = $props();

  // Local 1 s clock. Drives the relativeTime() derivation below; rerenders
  // the "Xs ago" text without coupling the parent to the tick rate.
  // Initial value uses Date.now() so the first paint after mount is correct
  // without waiting for the first interval fire.
  let now = $state(Date.now());

  $effect(() => {
    const t = setInterval(() => {
      now = Date.now();
    }, 1000);
    return () => clearInterval(t);
  });

  const ago = $derived(relativeTime(lastPollEnd, now));
</script>

<header
  class="sticky top-0 z-10 border-b"
  style:background="var(--color-bg-elev)"
  style:border-color="var(--color-border)"
>
  <div class="max-w-screen-xl mx-auto px-6 h-16 flex items-center justify-between">
    <h1 class="text-lg font-semibold tracking-tight" style:color="var(--color-fg-strong)">
      docker-update
    </h1>
    <div class="flex items-center gap-2">
      <button
        type="button"
        class="header-btn h-9 px-4 rounded-md border text-sm font-medium focus-visible:outline-none"
        aria-label="Refresh state from server"
        onclick={onRefresh}
      >
        Refresh
      </button>
      <button
        type="button"
        class="header-btn h-9 px-4 rounded-md border text-sm font-medium focus-visible:outline-none"
        aria-label="Trigger a poll right now"
        onclick={onWatchNow}
      >
        Watch now
      </button>
      <span
        class="text-xs ml-2 tabular-nums"
        style:color="var(--color-fg-muted)"
        aria-label="Last poll relative time"
      >
        {ago}
      </span>
    </div>
  </div>
</header>

<style>
  /* Header buttons share a recipe with ActionButton but are labeled and
     slightly larger (36 px tall, padded). Local scope keeps the markup
     calm and the recipe colocated. */
  .header-btn {
    color: var(--color-fg-strong);
    background: transparent;
    border-color: var(--color-border);
    transition: background-color 120ms ease-out, color 120ms ease-out, border-color 120ms ease-out;
  }
  .header-btn:hover {
    color: var(--color-accent);
    border-color: var(--color-accent);
    background-color: color-mix(in srgb, var(--color-accent) 10%, transparent);
  }
  .header-btn:active {
    background-color: color-mix(in srgb, var(--color-accent) 20%, transparent);
  }
  .header-btn:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }
</style>
