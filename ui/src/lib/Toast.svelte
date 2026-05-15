<script lang="ts" module>
  /**
   * ToastKind is exported at module-scope so ToastContainer + App.svelte
   * (Plan 05-04) consume a single source of truth. Distinct from
   * StatusBadge's StatusKind — different palette anchors (UI-SPEC.md §4.6
   * vs §4.3), different semantics (transient feedback vs row state).
   */
  export type ToastKind = 'success' | 'error' | 'warning' | 'info';

  /**
   * Toast — the shape of a single toast entry in App.svelte's toasts
   * state slice (Plan 05-04). Exported so ToastContainer can import it
   * without re-declaring the union.
   */
  export type Toast = {
    id: string;
    kind: ToastKind;
    title: string;
    body?: string;
  };
</script>

<script lang="ts">
  /**
   * Toast — single toast row positioned by ToastContainer. Renders a
   * 360px-max card with a 4px kind-colored border-left and a 12px x-mark
   * dismiss control.
   *
   * Auto-dismiss: 5s for success/info/warning; error toasts are sticky
   * (operators need to read failure reasons). Per UI-SPEC.md §4.6 and
   * 05-RESEARCH.md §F.1.
   *
   * Dismissal model (WR-06 + WR-07 in 05-REVIEW.md): the explicit x-mark
   * button is the SOLE dismiss control — both pointer and keyboard
   * activate it. The prior "click anywhere on toast" wrapper-level
   * onclick had no keyboard equivalent and clashed with role=alert,
   * which svelte-check a11y rules flag. ToastContainer owns the single
   * aria-live region; this component carries no role/aria-live so
   * screen readers don't double-announce.
   */
  type Props = {
    id: string;
    kind: ToastKind;
    title: string;
    body?: string;
    onDismiss: (id: string) => void;
  };

  let { id, kind, title, body, onDismiss }: Props = $props();

  // Map ToastKind → CSS var that drives the 4px border-left color.
  const accentVar = $derived.by(() => {
    switch (kind) {
      case 'success': return '--color-success';
      case 'error':   return '--color-danger';
      case 'warning': return '--color-warning';
      case 'info':    return '--color-info';
    }
  });

  const borderLeft = $derived(`var(${accentVar})`);

  // Auto-dismiss for non-error toasts after 5s. UI-SPEC.md §4.6: error
  // toasts stay until the operator dismisses them via the x button.
  $effect(() => {
    if (kind === 'error') return;
    const t = setTimeout(() => onDismiss(id), 5000);
    return () => clearTimeout(t);
  });

  function handleClose() {
    onDismiss(id);
  }
</script>

<!-- No role / aria-live on this wrapper: the parent ToastContainer
     carries the single live region (assertive on error, polite
     otherwise), so nesting another would cause NVDA/JAWS to announce
     twice. WR-06 in 05-REVIEW.md. -->
<!-- No outer onclick: the x-mark button is the sole dismiss control,
     keyboard-and-pointer reachable. Dropping the wrapper-level onclick
     resolves WR-07 — svelte-check a11y rules flag a <div> with onclick
     and no keydown/tabindex, and we already provide the X button as
     the documented dismiss affordance. -->
<div
  class="toast pointer-events-auto flex items-start gap-3 px-3 py-2.5 rounded-md shadow-md border min-w-[280px] max-w-[360px]"
  style:border-left-color={borderLeft}
  style:background="var(--color-bg)"
>
  <div class="flex-1 min-w-0">
    <div class="text-sm font-semibold" style:color="var(--color-fg-strong)">{title}</div>
    {#if body}
      <div class="text-[13px] mt-0.5 break-words" style:color="var(--color-fg)">{body}</div>
    {/if}
  </div>
  <button
    type="button"
    class="toast-close shrink-0 inline-flex items-center justify-center h-5 w-5 rounded -mr-0.5 -mt-0.5"
    aria-label="Dismiss notification"
    onclick={handleClose}
  >
    <!-- Heroicons: x-mark (outline, 12px) -->
    <svg
      class="h-3 w-3"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      aria-hidden="true"
    >
      <path stroke-linecap="round" stroke-linejoin="round" d="M6 18 18 6M6 6l12 12" />
    </svg>
  </button>
</div>

<style>
  .toast {
    /* 4px solid border-left; the other three sides use the standard
       --color-border. The style:border-left-color above overrides the
       left side to the kind-colored accent. */
    border-color: var(--color-border);
    border-left-width: 4px;
    border-left-style: solid;
    /* No cursor:pointer on the wrapper — the x-button is the sole
       interactive surface (WR-07 in 05-REVIEW.md). */
    /* Slide-up + fade entry per UI-SPEC.md §9 (180ms ease-out). The
       prefers-reduced-motion @media in app.css zeroes transition-duration
       globally; this keyframe path is one-shot so we mirror it manually. */
    animation: toast-in 180ms ease-out;
  }

  .toast-close {
    color: var(--color-fg-muted);
    background: transparent;
    border: 0;
    cursor: pointer;
  }

  .toast-close:hover {
    color: var(--color-fg-strong);
  }

  .toast-close:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 1px;
  }

  @keyframes toast-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .toast {
      animation-duration: 0ms;
    }
  }
</style>
