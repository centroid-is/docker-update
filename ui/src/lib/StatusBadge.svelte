<script lang="ts">
  /**
   * StatusBadge — colored pill rendering the 8 row-status kinds defined by
   * 05-UI-SPEC.md §4.3. Status is a literal union typed inline; no import
   * from tygo'd types because the wire layer carries booleans + strings
   * (action_in_flight, action_error, pinned, stopped, update_available)
   * which Row.svelte composes into this enum via $derived.
   *
   * Color matrix (per UI-SPEC.md §4.3 and 05-01 SUMMARY's "Open Notes for
   * 05-02"):
   *   current          → --color-success  (cyan)
   *   update_available → --color-pending  (yellow)   ← NOT --color-warning
   *   updating         → --color-info     (violet) + spinner
   *   rolling_back     → --color-info     (violet) + spinner
   *   force_pulling    → --color-info     (violet) + spinner
   *   action_error     → --color-danger   (red) + title=errorReason
   *   pinned           → --color-fg-muted (base01) + lock icon
   *   stopped          → --color-neutral  (base1)
   *
   * The bg/border are derived per-pill via color-mix(in srgb, <accent> X%,
   * transparent) so a single CSS var drives text + 12% bg + 40% border —
   * Tailwind v4 + modern Chromium support this everywhere we ship.
   */
  export type StatusKind =
    | 'current'
    | 'update_available'
    | 'updating'
    | 'rolling_back'
    | 'force_pulling'
    | 'action_error'
    | 'pinned'
    | 'stopped';

  type Props = {
    status: StatusKind;
    errorReason?: string;
  };

  let { status, errorReason }: Props = $props();

  // Map status → semantic CSS variable that drives text, bg-mix, border-mix.
  const accentVar = $derived.by(() => {
    switch (status) {
      case 'current':          return '--color-success';
      case 'update_available': return '--color-pending';
      case 'updating':
      case 'rolling_back':
      case 'force_pulling':    return '--color-info';
      case 'action_error':     return '--color-danger';
      case 'pinned':           return '--color-fg-muted';
      case 'stopped':          return '--color-neutral';
    }
  });

  // Inline alpha-mix expressions. UI-SPEC.md §4.3: 12% bg, 40% border.
  const bgMix     = $derived(`color-mix(in srgb, var(${accentVar}) 12%, transparent)`);
  const borderMix = $derived(`color-mix(in srgb, var(${accentVar}) 40%, transparent)`);
  const fg        = $derived(`var(${accentVar})`);

  // Human-facing label per UI-SPEC.md §4.3 (lowercase, snake→space for
  // multi-word, ellipsis for in-flight states).
  const label = $derived.by(() => {
    switch (status) {
      case 'current':          return 'current';
      case 'update_available': return 'update available';
      case 'updating':         return 'updating…';
      case 'rolling_back':     return 'rolling back…';
      case 'force_pulling':    return 'force-pulling…';
      case 'action_error':     return 'error';
      case 'pinned':           return 'pinned';
      case 'stopped':          return 'stopped';
    }
  });

  const isInFlight = $derived(
    status === 'updating' || status === 'rolling_back' || status === 'force_pulling'
  );
  const showLock = $derived(status === 'pinned');
  // action_error title= attribute lets operators read the full server reason
  // on hover (UI-SPEC.md §4.3). Falsy errorReason → no title.
  const titleAttr = $derived(status === 'action_error' && errorReason ? errorReason : undefined);
</script>

<span
  class="inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium border whitespace-nowrap"
  style:color={fg}
  style:background={bgMix}
  style:border-color={borderMix}
  title={titleAttr}
>
  {#if isInFlight}
    <!-- 12 px spinner; CSS-driven rotation via [data-spinner] so the
         prefers-reduced-motion exemption in app.css keeps it spinning. -->
    <svg
      data-spinner
      class="h-3 w-3 animate-spin"
      style:animation="spin 800ms linear infinite"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      aria-hidden="true"
    >
      <circle cx="12" cy="12" r="9" stroke-opacity="0.25" />
      <path d="M21 12a9 9 0 0 0-9-9" stroke-linecap="round" />
    </svg>
  {/if}
  {#if showLock}
    <!-- 12 px lock — Heroicons outline, lock-closed. -->
    <svg
      class="h-3 w-3"
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
  {/if}
  {label}
</span>

<style>
  /* Keyframes scoped to the component so the inline style:animation reference
     above resolves. Global @keyframes 'spin' is not guaranteed; declaring
     here keeps StatusBadge self-contained. */
  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
