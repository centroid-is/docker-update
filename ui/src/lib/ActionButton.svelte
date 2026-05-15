<script lang="ts">
  /**
   * ActionButton — square 28×28 icon-only button for per-row actions.
   * Renders one of three Heroicons (outline) based on `kind`; swaps to a
   * spinner when `busy`. Disabled state is visual only (40% opacity,
   * cursor-not-allowed) and short-circuits onClick.
   *
   * Per UI-SPEC.md §4.4:
   *   - Default:   transparent bg, --color-fg-muted icon
   *   - Hover:     accent-10% bg, --color-accent icon, --color-accent border
   *   - Disabled:  40% opacity, cursor-not-allowed
   *   - In-flight: accent-10% bg, spinner replaces icon
   *
   * aria-label format is "{kind} {service}" so screen readers say e.g.
   * "Update grafana"; UI-SPEC.md §4.4 + §6 (accessibility).
   */
  export type ActionKind = 'update' | 'rollback' | 'force-pull';

  type Props = {
    kind: ActionKind;
    service: string;
    disabled: boolean;
    busy: boolean;
    onClick: () => void;
  };

  let { kind, service, disabled, busy, onClick }: Props = $props();

  // Heroicons (outline, 24px viewBox, currentColor stroke). Inline SVG
  // per the no-extra-deps ethos — 3 icons total in this file.
  // Hover bg is driven via the `hover-fill` CSS custom property below
  // because color-mix in :hover utilities is verbose; this scopes the
  // accent-10% recipe locally and keeps the markup quiet.

  function handleClick() {
    if (disabled || busy) return;
    onClick();
  }
</script>

<button
  type="button"
  class="action-btn inline-flex items-center justify-center h-7 w-7 rounded-md border transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-1"
  aria-label={`${kind} ${service}`}
  title={`${kind} ${service}`}
  disabled={disabled || busy}
  aria-busy={busy ? 'true' : undefined}
  onclick={handleClick}
>
  {#if busy}
    <!-- 16 px spinner; data-spinner hook keeps animation alive under
         prefers-reduced-motion (app.css exemption from Plan 05-01). -->
    <svg
      data-spinner
      class="h-4 w-4"
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
  {:else if kind === 'update'}
    <!-- Heroicons: cloud-arrow-down — semantically "pull new image from registry"
         (replaces arrow-up-tray which read as an upload action). -->
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
        d="M12 9.75v6.75m0 0-3-3m3 3 3-3m-8.25 6a4.5 4.5 0 0 1-1.41-8.775 5.25 5.25 0 0 1 10.233-2.33 3 3 0 0 1 3.758 3.848A3.752 3.752 0 0 1 18 19.5H6.75Z"
      />
    </svg>
  {:else if kind === 'rollback'}
    <!-- Heroicons: arrow-uturn-left -->
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
        d="M9 15 3 9m0 0 6-6M3 9h12a6 6 0 0 1 0 12h-3"
      />
    </svg>
  {:else}
    <!-- Heroicons: arrow-path (force-pull / refresh) -->
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
        d="M16.023 9.348h4.992V4.356M2.985 19.644l4.992-4.992m0 0H3.075m4.902 0V19.5M19.5 14.598a8.25 8.25 0 0 1-15.357 1.658m0 0L4.5 12m-1.357 4.256L7.5 12m12-4.598a8.25 8.25 0 0 0-15.357-1.658m0 0L4.5 7.5m1.357-4.598L7.5 7.5"
      />
    </svg>
  {/if}
</button>

<style>
  /* Local accent-driven recipe per UI-SPEC.md §4.4. Using a scoped <style>
     keeps the 4 color states (idle / hover / active / disabled) declarative
     and out of the markup. The selectors compose with Tailwind utilities
     on the <button> above. */
  .action-btn {
    color: var(--color-fg-muted);
    border-color: var(--color-border);
    background-color: transparent;
  }

  .action-btn:hover:not(:disabled) {
    color: var(--color-accent);
    border-color: var(--color-accent);
    background-color: color-mix(in srgb, var(--color-accent) 10%, transparent);
  }

  .action-btn:active:not(:disabled) {
    background-color: color-mix(in srgb, var(--color-accent) 20%, transparent);
  }

  .action-btn:disabled {
    opacity: 0.4;
    cursor: not-allowed;
  }

  /* In-flight state — when busy, give the button the accent-tinted look so
     it visibly differs from a plain disabled button. */
  .action-btn[aria-busy="true"] {
    opacity: 1; /* override the .action-btn:disabled 40% above */
    color: var(--color-accent);
    background-color: color-mix(in srgb, var(--color-accent) 10%, transparent);
    border-color: color-mix(in srgb, var(--color-accent) 40%, transparent);
    cursor: progress;
  }

  .action-btn:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  @keyframes spin {
    to { transform: rotate(360deg); }
  }
</style>
