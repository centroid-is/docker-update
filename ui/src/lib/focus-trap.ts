/**
 * focus-trap — Svelte action that traps Tab/Shift-Tab focus inside a
 * modal panel and dispatches a `cancel` CustomEvent on Escape.
 *
 * Per 05-RESEARCH.md §G.1 and UI-SPEC.md §4.7:
 *   - Tab cycles within focusable descendants
 *   - Shift-Tab wraps backward at the first element
 *   - Escape dispatches `cancel` (consumed by oncancel attr on the
 *     <div use:focusTrap> in WarningModal.svelte)
 *   - On mount: focus the element with [data-primary] (the Continue
 *     button — operators can press Enter to confirm after reading)
 *
 * Returns a Svelte action's standard destroy() handle so the keydown
 * listener is removed when the modal unmounts.
 *
 * Caller pattern (WarningModal.svelte):
 *   <div use:focusTrap oncancel={onCancel}>
 *     <button>Cancel</button>
 *     <button data-primary>Continue</button>
 *   </div>
 *
 * The Escape→cancel CustomEvent flow uses the DOM's bubbling so the
 * modal's <div> parent receives the `cancel` event and invokes the
 * caller's onCancel callback via Svelte 5's `oncancel` attribute.
 */
export function focusTrap(node: HTMLElement) {
  // Capture the element that had focus immediately before the modal
  // mounted so the destroy handler can restore it (UI-SPEC.md §4.7 and
  // WCAG 2.4.3 — Focus Order). Without this, dismissing the modal
  // snaps focus to <body> and keyboard operators lose their place in
  // the row's button cluster.
  //
  // document.activeElement defaults to <body> when nothing is focused;
  // the destroy guard below handles that benign no-op case.
  const previouslyFocused = document.activeElement as HTMLElement | null;

  const focusables = () =>
    node.querySelectorAll<HTMLElement>(
      'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])',
    );

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      // Bubbling CustomEvent — the host element with `oncancel` handles it.
      // preventDefault keeps Escape from also closing native modal-like
      // surfaces below (e.g., a <dialog> ancestor); we already trap here.
      e.preventDefault();
      node.dispatchEvent(new CustomEvent('cancel'));
      return;
    }
    if (e.key !== 'Tab') return;
    const f = Array.from(focusables());
    if (f.length === 0) return;
    const first = f[0];
    const last = f[f.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      last.focus();
      e.preventDefault();
    } else if (!e.shiftKey && document.activeElement === last) {
      first.focus();
      e.preventDefault();
    }
  }

  node.addEventListener('keydown', onKeydown);

  // Initial focus: the [data-primary] button (Continue) — UI-SPEC.md §4.7.
  // Defer one microtask so the element is actually painted + focusable
  // (Svelte mounts children before the action runs in some compile
  // modes; queueMicrotask is the cheap belt-and-braces).
  queueMicrotask(() => {
    const primary = node.querySelector<HTMLElement>('[data-primary]');
    primary?.focus();
  });

  return {
    destroy() {
      node.removeEventListener('keydown', onKeydown);
      // Restore focus to the pre-modal element if it's still in the
      // DOM and focusable. Guards:
      //   - previouslyFocused may be null (no prior activeElement).
      //   - The trigger could have unmounted while the modal was open
      //     (rare in this app; flutter rows persist), in which case
      //     document.contains returns false and we skip the focus call.
      //   - HTMLElement.focus() on <body> is a benign no-op.
      // CR-03 in 05-REVIEW.md: priority #5 of the modal a11y contract.
      if (previouslyFocused && document.contains(previouslyFocused)) {
        previouslyFocused.focus?.();
      }
    },
  };
}
