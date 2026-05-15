# Phase 6 — Deferred Items

Items discovered during plan 06-01 execution that are out of scope for
this plan (per the SCOPE BOUNDARY rule in the executor playbook) but
were observed while running the plan's `make e2e` verify gate.

## Pre-existing e2e failures unrelated to Phase 6 Task 2

`make e2e-cron-fast` (the only working e2e target on `main`; `make e2e`
itself fails in `global-setup.ts` because it requires
`HMI_UPDATE_CRON=@every 5s` which only the cron-fast override provides)
shows 4 pre-existing failures. All 4 failed before my changes landed and
none touch the new `weston-warning.spec.ts` or the `weston-stub`
fixture I added — both of my tests pass on the first attempt
(lines `✓ 35` and `✓ 36` in the Playwright output, 128 ms + 820 ms).

| # | Spec | Failure | Likely owner |
|---|------|---------|--------------|
| 1 | `tests/detect-multiarch.spec.ts:73` (single-arch flip within cron+5s) | Flip-detection timing | Phase 3 (poll/registry) |
| 2 | `tests/discovery.spec.ts:76` (events-path: new container visible within 5s) | New container not appearing within the 5s window | Phase 2 (discovery events) |
| 3 | `tests/healthz-negative.spec.ts:109` (eacces returns Pitfall 9 hint) | Response now says "docker daemon unreachable" instead of "docker socket permission denied" | Phase 2 (healthz wiring) — copy regression |
| 4 | `tests/smoke.spec.ts:37` (empty-state row with colspan="7") | Table is no longer empty (the e2e stack has 7+ watched stubs) — the empty-state row is correctly absent | Phase 1 (smoke spec assumes empty fixture) |

### Why not fixed here

These failures are unrelated to the UX-01 / UX-02 documentation
deliverables and to UI-08's substring-detection contract — they fall
into Phase 2 / Phase 3 / Phase 5 cleanup. Fixing them here would
violate the SCOPE BOUNDARY rule (auto-fix only what the current
task's changes broke) and would inflate Plan 06-01 well past its
"two-task / documentation-only" charter.

### Suggested follow-ups

- **(4) smoke.spec.ts** is the most likely to be triggered by a Phase
  5 rewrite: Plan 05-04 rewrote App.svelte's table-rendering path. The
  smoke assertion about `td[colspan="7"]` predates the wired
  state-fetching path and was authored against a fixture that produced
  zero watched containers. The fixture now has many. Either update the
  spec to skip when containers are present, or split the empty-state
  assertion into a dedicated spec that uses a stripped-down compose
  override.
- **(3) healthz-negative.spec.ts** is a Pitfall 9 copy regression: the
  response body's `reason` field used to say "docker socket permission
  denied" and now says "docker daemon unreachable". Either the wording
  changed intentionally (in which case the spec must be updated) or
  the underlying handler logic regressed (in which case the handler
  is the fix site).
- **(1) and (2)** look like flake / timing — the events-path 5s window
  may be too tight; the cron flip window may be too tight at @every 5s.

## `make e2e` target itself is broken on `main`

`make e2e` fails before any test runs because `global-setup.ts`'s
`waitForPollAdvance(15_000)` expects the cronPoller to advance within
15 s. At the project default `HMI_UPDATE_CRON=0 * * * *` (hourly) this
deadline is unreachable, which is by design — the file's own comment
says "globalSetup running against production cron should fail loudly".
The Makefile `e2e` target does NOT set `HMI_UPDATE_CRON=@every 5s` —
only `make e2e-cron-fast` does (via
`e2e/compose.test.override.cron-fast.yml`). Either `make e2e` should be
deleted (it always fails) or it should inherit the cron-fast override
the same way `e2e-cron-fast` does. Tracked as a Phase 7 deployment-target
cleanup candidate.
