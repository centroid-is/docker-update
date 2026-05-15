/**
 * display-warning — frontend-only predicate that flags services whose
 * recreate (Update or Rollback) will blank the HMI display for a few
 * seconds. Matches case-insensitive substring against a small list of
 * known display-drawing services.
 *
 * Per 05-CONTEXT.md Area 4 and 05-RESEARCH.md §J, the list is hardcoded
 * here intentionally — the server has no opinion about which services
 * draw to the framebuffer; this UI is the right layer to add an
 * operator-protective warning before firing the action POST.
 *
 * Phase 6 UX-01 may promote this to a server-side label
 * (`hmi-update.display-drawing=true`); deferred from v1.
 *
 * Examples (case-insensitive substring):
 *   requiresWarning('flutter-app')   === true
 *   requiresWarning('Weston-Server') === true
 *   requiresWarning('hmi-weston')    === true
 *   requiresWarning('grafana')       === false
 *
 * Caller: ui/src/App.svelte::handleAction (Plan 05-04) — branches on this
 * predicate to either fire the POST directly or open WarningModal first.
 */

export const DISPLAY_DRAWING_SERVICES = ['flutter', 'weston'] as const;

export function requiresWarning(service: string): boolean {
  const s = service.toLowerCase();
  return DISPLAY_DRAWING_SERVICES.some((name) => s.includes(name));
}
