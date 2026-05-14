# Deferred items from Phase 3

Pre-existing failures or scope-boundary discoveries the executor surfaced
during plan execution but did NOT fix because they are not directly
caused by the in-flight plan's changes (per the scope-boundary rule in
the GSD executor protocol).

---

## D-03-05-01 — `smoke.spec.ts` empty-state assertion fails when state has containers

**Source plan:** 03-05 Task 4 (executor noticed during full-suite e2e run).

**Symptom:**
```
Error: locator('table tbody td[colspan="7"]') resolved to 0 elements
  (Empty state row should use colspan="7" for the seven columns)
```

**Root cause:** `e2e/tests/smoke.spec.ts` line 70-72 asserts the UI's
empty state — `No watched containers yet` row — is rendered. This was
true in Phase 1 when `e2e/compose.test.yml` had no `hmi-update.watch`
labels. Phase 2 added `stub-watched-container` with the label; Phase 3
Plan 03-05 added three more. By the time `smoke.spec.ts` runs (the
file sorts alphabetically last among the e2e tests; `workers: 1`,
`fullyParallel: false` means specs share one stack and run sequentially),
the table is fully populated. The empty-state row never renders.

**Pre-existing?** Yes. The test was likely failing silently since Phase
2 added the labeled container. Phase 3 Plan 03-05 did not introduce the
bug — it only multiplied the number of populated rows. Per the
scope-boundary rule, the executor did not fix it.

**Suggested fix:** Update `smoke.spec.ts` to assert either (a) the
empty state OR (b) at least one container row, depending on what the
spec was designed to validate. Alternative: bring up `smoke.spec.ts`
against a dedicated minimal compose override that has zero watched
containers (mirrors the `compose.test.override.eacces.yml` pattern).

**Owner:** Next Phase 1/2 maintenance pass or a future plan that
audits the e2e suite.

---

## D-03-05-02 — `healthz-negative.spec.ts` "eacces" branch reports "docker daemon unreachable" instead of "docker socket permission denied"

**Source plan:** 03-05 Task 4 (executor noticed during full-suite e2e run).

**Symptom:**
```
Expected substring: "docker socket permission denied"
Received string:    "docker daemon unreachable"
```

**Root cause:** The `eacces` test brings up the stack with
`compose.test.override.eacces.yml` (pins `user: 65532:65532`) and
asserts `/healthz`'s `body.reason` contains "docker socket permission
denied" (Pitfall 9). On the test environment used during Plan 03-05
execution, `/healthz` instead reports "docker daemon unreachable".
This suggests either (a) the docker SDK now returns a different error
class when EACCES happens at connect-time, or (b) the underlying
classification logic in `internal/api/handlers_healthz.go` changed.

**Pre-existing?** Possibly — the test was last touched in plan
01-04; subsequent SDK updates (Phase 3 introduced
`github.com/moby/moby/client` via Plan 03-02) may have changed the
error path. Plan 03-05 did not modify `handlers_healthz.go` or the
moby client wrapper.

**Suggested fix:** Inspect the actual `body.reason` string under
`compose.test.override.eacces.yml`; update either the healthz
classifier in `internal/api/handlers_healthz.go` to return "docker
socket permission denied" again, OR update the test's expected
substring to match the current reality. Either is a single-line
change but needs a deliberate decision about which side is canonical
(CONTEXT.md "Healthz Remediation Hints" prescribes the verbatim
string — favors fixing the classifier).

**Owner:** Phase 2 maintenance pass or a focused `/gsd-quick` fix.

---

## D-03-05-03 — Plan 03-05 introduced multiple test-ordering interactions

**Source plan:** 03-05 Task 4 (executor noticed during full-suite e2e run).

**Observation:** With the four new spec files in place
(`detect-multiarch`, `detect-tag-pattern`, `detect-pinned`,
`obs-04-redaction`), the e2e suite has 8 spec files and 15 tests.
Specs share one docker stack (`workers: 1`, `fullyParallel: false`)
and run in alphabetical order by file name. The interaction surface
between specs (each spec's after-effects on the shared state) is now
non-trivial.

Examples:
- `healthz-negative.spec.ts`'s `afterAll` tears down and re-brings-up
  the BASE stack — without the cron-fast override. Plan 03-05 fixed
  this by teaching the spec to layer `compose.test.override.cron-fast.yml`
  when the file exists on disk.
- `detect-*` specs leave the registry seeded with their pushed
  manifests; subsequent specs see those digests in `/api/state`.
- The cron-fast override (`@every 5s`) keeps the cronPoller busy
  through the entire suite; specs that don't expect new state mid-test
  can race against an arriving cron tick.

**Suggested follow-up:** A future plan should establish a clearer
per-spec setup/teardown discipline. Options:
- `test.beforeEach` that re-stamps the registry seeds to known
  digests so each test starts from a deterministic state.
- A test.skip() guard that detects when a spec is running outside
  its intended stack composition (e.g.
  obs-04-redaction.spec.ts could skip with a clear message when
  `last_poll_end` never advances past baseline — though that
  defeats the spec's purpose).
- Promote single-spec invocation (`npx playwright test foo.spec.ts`)
  as the canonical CI shape, with `make e2e` running each spec in
  isolation.

**Owner:** Future Phase 8 (CI consolidation) or a follow-up phase
focused on e2e harness ergonomics.
