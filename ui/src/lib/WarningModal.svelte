<script lang="ts">
  /**
   * WarningModal — flutter/weston pre-action confirmation modal.
   *
   * Renders when `open` is true; otherwise renders nothing (early {#if}
   * gate). Per UI-SPEC.md §4.7:
   *   - 480 px wide, centered, bg-base3 cream, border, rounded-md, shadow-xl
   *   - Backdrop: fixed inset-0, bg-base02/40, click → onCancel
   *   - Title row: 20 px orange triangle-warning + "Display may flicker."
   *   - Body: verbatim 5–30 seconds copy with bold {service} interpolation
   *   - Buttons: Cancel (secondary) + Continue (primary, cyan, data-primary)
   *   - Focus trap + body scroll lock while open
   *   - Esc = Cancel (via focusTrap's cancel CustomEvent)
   *
   * The `oncancel` attribute on the focus-trapped <div> consumes the
   * CustomEvent that focus-trap.ts dispatches on Escape; Svelte 5
   * delegates `oncancel` as a DOM event listener (not a Svelte component
   * event), which is what we want because focus-trap dispatches a real
   * DOM CustomEvent('cancel').
   *
   * Body scroll lock per 05-RESEARCH.md §G.2 — set
   * document.body.style.overflow='hidden' while open; restore prior on
   * cleanup. Threat T-05-03-05 mitigation: never restore to empty
   * unconditionally (cleanup captures the prior value at effect entry).
   */
  import { focusTrap } from './focus-trap';

  type Props = {
    open: boolean;
    service: string;
    action: 'update' | 'rollback';
    onConfirm: () => void;
    onCancel: () => void;
  };

  // `action` is declared in the props contract for forward compatibility
  // (Plan 05-04 may surface "Update" vs "Rollback" in a tooltip on
  // Continue), but the current modal copy is identical for both per
  // UI-SPEC.md §11 (the warning is about the recreate, which both actions
  // cause). Destructure with a leading underscore to keep TS + svelte-check
  // happy without referencing the rune locally.
  let { open, service, action: _action, onConfirm, onCancel }: Props = $props();

  // Body scroll lock while modal is open. UI-SPEC.md §4.7 + 05-RESEARCH.md §G.2.
  $effect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = prev;
    };
  });

  // Backdrop click cancels. The backdrop is a sibling at z-40, not a
  // parent of the panel — the panel is in its own z-50 wrapper — so
  // panel-internal clicks never bubble to the backdrop. No stopPropagation
  // dance required.
  function handleBackdropClick() {
    onCancel();
  }

</script>

{#if open}
  <!-- Backdrop — fixed full-viewport, semi-transparent base02. Click or
       keyboard activation cancels the action per UI-SPEC.md §4.7
       (operators expect the backdrop to behave like a dismiss). Rendered
       as a real <button> so svelte-check a11y rules are satisfied:
       interactive role + native Enter/Space keyboard activation.
       tabindex=-1 keeps it out of the Tab cycle (focus-trap controls
       focus inside the panel; Escape is the keyboard dismiss path). -->
  <button
    type="button"
    class="modal-backdrop fixed inset-0 z-40"
    style:background="color-mix(in srgb, var(--color-base02) 40%, transparent)"
    aria-label="Dismiss warning"
    tabindex="-1"
    onclick={handleBackdropClick}
  ></button>

  <!-- Centering wrapper + focus-trapped panel. Svelte 5: `oncancel` on
       this element captures the CustomEvent that focus-trap dispatches on
       Escape. The panel itself stops click propagation so clicks inside
       don't bubble to the backdrop dismiss. -->
  <div
    class="fixed inset-0 z-50 flex items-center justify-center pointer-events-none"
    role="dialog"
    aria-modal="true"
    aria-labelledby="warn-title"
    aria-describedby="warn-body"
    use:focusTrap
    oncancel={onCancel}
  >
    <div
      class="warn-panel pointer-events-auto w-[480px] max-w-[calc(100vw-2rem)] p-6 rounded-md border shadow-xl"
      style:background="var(--color-bg)"
      style:border-color="var(--color-border)"
    >
      <!-- Title row — orange triangle-warning + bold heading. -->
      <div class="flex items-start gap-2.5">
        <!-- Heroicons: exclamation-triangle (outline, 20px) -->
        <svg
          class="h-5 w-5 shrink-0 mt-0.5"
          style:color="var(--color-warning)"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
          aria-hidden="true"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126ZM12 15.75h.007v.008H12v-.008Z"
          />
        </svg>
        <h2
          id="warn-title"
          class="text-base font-semibold"
          style:color="var(--color-fg-strong)"
        >Display may flicker.</h2>
      </div>

      <!-- Body — verbatim copy from UI-SPEC.md §11. The en-dash in
           "5–30 seconds" is load-bearing for ui-flutter-warning.spec.ts. -->
      <p
        id="warn-body"
        class="mt-3 text-sm leading-relaxed"
        style:color="var(--color-fg)"
      >
        Recreating <strong style:color="var(--color-fg-strong)">{service}</strong> on a new image will blank the HMI display for 5–30 seconds. The web UI will return as soon as the container is healthy. Continue?
      </p>

      <!-- Buttons row — right-aligned, 8px gap. Continue carries
           data-primary so focus-trap.ts focuses it on mount. -->
      <div class="mt-5 flex items-center justify-end gap-2">
        <button
          type="button"
          class="warn-btn warn-btn-secondary inline-flex items-center justify-center h-9 px-4 rounded-md border text-sm font-medium"
          onclick={onCancel}
        >Cancel</button>
        <button
          type="button"
          data-primary
          class="warn-btn warn-btn-primary inline-flex items-center justify-center h-9 px-4 rounded-md border text-sm font-semibold"
          onclick={onConfirm}
        >Continue</button>
      </div>
    </div>
  </div>
{/if}

<style>
  .modal-backdrop {
    /* Reset native button chrome so the backdrop reads as a translucent
       surface, not a button. The interaction (click/Enter/Space)
       behaviour stays — only the visual is reset. */
    border: 0;
    padding: 0;
    margin: 0;
    cursor: pointer;
    appearance: none;
  }

  .modal-backdrop:focus-visible {
    outline: none;
  }

  .warn-btn {
    cursor: pointer;
    transition: background-color 120ms ease-out, border-color 120ms ease-out;
  }

  .warn-btn:focus-visible {
    outline: 2px solid var(--color-accent);
    outline-offset: 2px;
  }

  .warn-btn-secondary {
    background: transparent;
    color: var(--color-fg);
    border-color: var(--color-border);
  }

  .warn-btn-secondary:hover {
    background: color-mix(in srgb, var(--color-fg-muted) 8%, transparent);
    border-color: color-mix(in srgb, var(--color-fg-muted) 50%, transparent);
  }

  .warn-btn-primary {
    background: var(--color-success);
    color: #fff;
    border-color: var(--color-success);
  }

  .warn-btn-primary:hover {
    background: color-mix(in srgb, var(--color-success) 85%, black);
    border-color: color-mix(in srgb, var(--color-success) 85%, black);
  }

  .warn-btn-primary:active {
    background: color-mix(in srgb, var(--color-success) 75%, black);
  }

  /* Panel reveal — opacity + slight scale per UI-SPEC.md §9. */
  .warn-panel {
    animation: warn-in 150ms ease-out;
  }

  @keyframes warn-in {
    from {
      opacity: 0;
      transform: scale(0.98);
    }
    to {
      opacity: 1;
      transform: scale(1);
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .warn-panel {
      animation-duration: 0ms;
    }
    .warn-btn {
      transition-duration: 0ms;
    }
  }
</style>
