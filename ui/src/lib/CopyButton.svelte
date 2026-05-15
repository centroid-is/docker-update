<script lang="ts">
  /**
   * CopyButton — 20×20 icon-only button that writes `value` to the
   * clipboard via navigator.clipboard.writeText. On success swaps to a
   * cyan check icon for 1500 ms; on failure swaps to a red ✕ for the same
   * window. Accessibility: aria-label "Copy {label || 'digest'}";
   * aria-live polite announces "Copied" / "Copy failed".
   *
   * Per UI-SPEC.md §4.5. The clipboard API is gated by browser permissions
   * — on http://localhost (the test stack) Chromium permits without prompt;
   * Playwright tests grant 'clipboard-read'/'clipboard-write' explicitly.
   */
  type Props = {
    value: string;
    label?: string;
  };

  let { value, label }: Props = $props();

  type CopyState = 'idle' | 'copied' | 'failed';
  let copyState = $state<CopyState>('idle');
  let resetTimer: ReturnType<typeof setTimeout> | null = null;

  async function copy() {
    // Clear any prior reset timer — rapid double-click should not flicker.
    if (resetTimer !== null) {
      clearTimeout(resetTimer);
      resetTimer = null;
    }
    try {
      await navigator.clipboard.writeText(value);
      copyState = 'copied';
    } catch {
      // Permission denied, no clipboard API, etc. Surface the failure
      // visually rather than silently swallowing — operators expect feedback.
      copyState = 'failed';
    }
    resetTimer = setTimeout(() => {
      copyState = 'idle';
      resetTimer = null;
    }, 1500);
  }

  // Live-region announcement text for screen readers.
  const liveText = $derived.by(() => {
    if (copyState === 'copied') return 'Copied';
    if (copyState === 'failed') return 'Copy failed';
    return '';
  });

  const ariaLabel = $derived(`Copy ${label ?? 'digest'}`);
</script>

<button
  type="button"
  class="copy-btn inline-flex items-center justify-center h-5 w-5 rounded focus-visible:outline-none focus-visible:ring-2"
  aria-label={ariaLabel}
  title={ariaLabel}
  onclick={copy}
>
  {#if copyState === 'copied'}
    <!-- Heroicons: check-circle (cyan = --color-success) -->
    <svg
      class="h-3.5 w-3.5"
      style:color="var(--color-success)"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      aria-hidden="true"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M9 12.75 11.25 15 15 9.75M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0Z"
      />
    </svg>
  {:else if copyState === 'failed'}
    <!-- Heroicons: x-mark (red = --color-danger) -->
    <svg
      class="h-3.5 w-3.5"
      style:color="var(--color-danger)"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      aria-hidden="true"
    >
      <path
        stroke-linecap="round"
        stroke-linejoin="round"
        d="M6 18 18 6M6 6l12 12"
      />
    </svg>
  {:else}
    <!-- Heroicons: clipboard-document (outline, base01) -->
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
        d="M9 12h3.75M9 15h3.75M9 18h3.75m3 .75H18a2.25 2.25 0 0 0 2.25-2.25V6.108c0-1.135-.845-2.098-1.976-2.192a48.424 48.424 0 0 0-1.123-.08m-5.801 0c-.065.21-.1.433-.1.664 0 .414.336.75.75.75h4.5a.75.75 0 0 0 .75-.75 2.25 2.25 0 0 0-.1-.664m-5.8 0A2.251 2.251 0 0 1 13.5 2.25H15c1.012 0 1.867.668 2.15 1.586m-5.8 0c-.376.023-.75.05-1.124.08C9.095 4.01 8.25 4.973 8.25 6.108V8.25m0 0H4.875c-.621 0-1.125.504-1.125 1.125v11.25c0 .621.504 1.125 1.125 1.125h9.75c.621 0 1.125-.504 1.125-1.125V9.375c0-.621-.504-1.125-1.125-1.125H8.25ZM6.75 12h.008v.008H6.75V12Zm0 3h.008v.008H6.75V15Zm0 3h.008v.008H6.75V18Z"
      />
    </svg>
  {/if}
</button>

<!-- Polite live region; non-visual; announces copy result to screen readers. -->
<span class="sr-only" role="status" aria-live="polite">{liveText}</span>

<style>
  .copy-btn {
    color: var(--color-fg-muted);
    background: transparent;
  }
  .copy-btn:hover {
    color: var(--color-fg-strong);
    background-color: color-mix(in srgb, var(--color-fg-muted) 10%, transparent);
  }
  .copy-btn:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 1px;
  }
  /* Local sr-only fallback in case Tailwind v4 doesn't ship it by default. */
  :global(.sr-only) {
    position: absolute;
    width: 1px;
    height: 1px;
    padding: 0;
    margin: -1px;
    overflow: hidden;
    clip: rect(0, 0, 0, 0);
    white-space: nowrap;
    border: 0;
  }
</style>
