/**
 * actions.ts — the SINGLE module that talks to the Phase 4 action
 * endpoints (`POST /api/containers/{service}/{update|rollback|force-pull}`)
 * and the optional `POST /api/poll-now` kick.
 *
 * Centralizing the fetch dance here:
 *   1. Keeps App.svelte free of HTTP plumbing — it consumes a typed
 *      `postAction(svc, kind): Promise<ActionResult>` and catches
 *      `ActionError` to translate the structured error body into a
 *      toast (per 05-CONTEXT.md Area 5 + 05-RESEARCH.md §C.2).
 *   2. Pins the wire contract in ONE place — the Phase 4 action
 *      response envelope (`current_digest`, `previous_digest`,
 *      `no_op`, `pulled`) is documented in API.md and pinned here
 *      via the `ActionResult` type.
 *   3. Provides the belt-and-braces `encodeURIComponent` on the
 *      service name in the POST URL. The server's ACT-10
 *      `^[a-zA-Z0-9._-]+$` regex is the authoritative gate; this is
 *      defense in depth (T-05-04-08).
 *
 * Wire contract reference: API.md §"POST /api/containers/{service}/…"
 * (success bodies, idempotency bodies, error matrix).
 *
 * Caller: ui/src/App.svelte::executeAction (Plan 05-04) — invokes
 * `postAction(...)` inside a try/catch keyed on `ActionError`.
 */

/**
 * ActionKind is the discriminated union of per-row actions; mirrors
 * the URL path-tail on the server (`/update`, `/rollback`,
 * `/force-pull`). Re-exported from ActionButton.svelte's module scope
 * elsewhere; this file declares it locally to avoid an import cycle
 * (actions.ts must not depend on a Svelte component for a type).
 */
export type ActionKind = 'update' | 'rollback' | 'force-pull';

/**
 * ActionResult mirrors the success envelope documented in API.md.
 * All four fields are optional because:
 *   - `current_digest` / `previous_digest` are present on every 2xx
 *     from Phase 4 (ACT-11), but a future endpoint might omit them
 *     on a force-pull-no-recreate where the digest didn't move.
 *   - `no_op: true` only appears on the idempotency branch (ACT-06 /
 *     ACT-07).
 *   - `pulled` is reserved for force-pull responses; safe to omit
 *     elsewhere.
 * Keeping all four optional lets a caller narrow on `result.no_op`
 * without a non-null assertion.
 */
export type ActionResult = {
  current_digest?: string;
  previous_digest?: string;
  no_op?: boolean;
  pulled?: boolean;
};

/**
 * ActionError is thrown by `postAction` on any non-2xx response. It
 * carries the HTTP status plus the structured error body (`error`
 * code and human-readable `reason`) so App.svelte can route to the
 * right toast (warning for 409 service_busy, error otherwise) and
 * surface the server's reason verbatim.
 *
 * Per 05-RESEARCH.md §C.2 — the constructor uses `${code}: ${reason}`
 * for `super(...)` so a stray `console.error(err)` produces an
 * operator-readable line without a custom .toString().
 */
export class ActionError extends Error {
  constructor(
    public status: number,
    public code: string,
    public reason: string,
  ) {
    super(`${code}: ${reason}`);
    this.name = 'ActionError';
  }
}

/**
 * postAction fires the per-row action POST and returns the parsed
 * success body, or throws an ActionError on a non-2xx response.
 *
 * Body parse failure is tolerated via `.catch(() => ({}))` — a
 * malformed JSON body must still surface as an ActionError carrying
 * the HTTP status (so the operator sees "Update failed" with the
 * empty-reason fallback below), not as a generic SyntaxError.
 *
 * The Content-Type header is courtesy — the server (Phase 4) accepts
 * an empty request body on all three action endpoints. Including it
 * keeps the request log consistent and lets future server changes
 * (e.g. accept `{recreate: true}` for force-pull) negotiate by
 * Content-Type without breaking older clients.
 */
export async function postAction(
  service: string,
  kind: ActionKind,
): Promise<ActionResult> {
  const url = `/api/containers/${encodeURIComponent(service)}/${kind}`;
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
  });
  // Tolerate a malformed/empty body — the HTTP status is the load-bearing
  // signal on the error path; the structured body is best-effort context.
  const body: { error?: string; reason?: string; detail?: string } & ActionResult =
    await r.json().catch(() => ({}));
  if (!r.ok) {
    throw new ActionError(
      r.status,
      body.error ?? 'unknown',
      // Phase 4 error bodies use `reason` on verify_failed and `detail`
      // on invalid_service_name; fall through both so the toast body
      // shows the most specific string the server provided.
      body.reason ?? body.detail ?? '',
    );
  }
  return body;
}

/**
 * PollNowResult distinguishes the failure modes so the caller can
 * surface an honest toast (WR-03 in 05-REVIEW.md). The prior boolean
 * return collapsed three distinct failures — 404, 5xx, network — into
 * a single "Poll-now endpoint not available" message that lied to
 * operators about transient server errors and connectivity blips.
 *
 * The 'not_implemented' branch corresponds to the Phase 3
 * forward-compat shape (POST /api/poll-now may or may not exist on a
 * given build); the other two are honest reports of real failure.
 */
export type PollNowResult =
  | { ok: true }
  | { ok: false; reason: 'not_implemented' | 'server_error' | 'network' };

/**
 * pollNow is the optional "Watch now" kick. Phase 3 may or may not
 * ship `POST /api/poll-now`; the contract here lets App.svelte degrade
 * gracefully:
 *   - 2xx                 → { ok: true } (cron sweep kicked).
 *   - 404                 → { reason: 'not_implemented' } — endpoint
 *     absent on this build; caller surfaces an info toast.
 *   - Other non-2xx (5xx) → { reason: 'server_error' } — endpoint
 *     exists but failed; caller surfaces a warning toast so the
 *     operator knows the kick didn't land.
 *   - Network failure     → { reason: 'network' } — fetch threw
 *     (offline, DNS, etc.); caller surfaces a warning toast.
 *
 * Per T-05-04-05: defending against forward-compat drift between this
 * UI and the backend is the point of this helper. App.svelte never
 * "loses" the Watch-now affordance even if /api/poll-now disappears.
 */
export async function pollNow(): Promise<PollNowResult> {
  try {
    const r = await fetch('/api/poll-now', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
    });
    if (r.ok) return { ok: true };
    if (r.status === 404) return { ok: false, reason: 'not_implemented' };
    return { ok: false, reason: 'server_error' };
  } catch {
    return { ok: false, reason: 'network' };
  }
}
