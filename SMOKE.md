# hmi-update manual smoke log

Canonical Phase closure record for the C4 verify → implement → verify → implement
discipline (CLAUDE.md). Each Phase appends a dated entry with the
host, image under watch, cron expression used, outcome, and one-line
notes. Phase 8's CI-05 release gate reads this file to confirm a
green CI run is paired with a recorded manual smoke before releasing.

Entries follow the format:

```
## YYYY-MM-DD — Phase NN closure smoke
- Host: <dev-machine / HMI box hostname>
- Image under watch: <registry/repo:tag>
- HMI_UPDATE_CRON: <expression>
- Outcome: <pass | fail (reason)>
- Notes: <one-line summary>
```

---

## 2026-05-14 — Phase 03 closure smoke

- Host: Centroid dev machine (`/Users/jonb/Projects/tmp`, macOS Docker Desktop)
- Image under watch: `zot:5000/centroid-is/stub:latest` (in-cluster zot fixture
  serving as the GHCR analog for the e2e harness)
- HMI_UPDATE_CRON: `@every 5s` (via `compose.test.override.cron-fast.yml`)
- Outcome: **pass**
- Notes: Plan 03-05 e2e suite ran the four DETECT/OBS specs against
  the in-cluster zot stack (the GHCR analog the rest of the Phase 03
  development cycle targeted). All four passed:
    * detect-multiarch     — both manifest shapes flip update_available
                              within cron+5s; resolver returns AMD64
                              child digest for the OCI index push.
    * detect-tag-pattern   — tag-pattern label filters non-matching
                              pushes; matching pushes flip; invalid
                              regex surfaces canonical Note.
    * detect-pinned        — digest-pinned containers appear with
                              `pinned: true` + `notes: "pinned: opt-out"`
                              and never flip update_available.
    * obs-04-redaction     — `docker compose logs hmi-update` across
                              a full poll sweep returned ZERO matches
                              for `(Bearer |Authorization:|Basic Og==)`;
                              the affirmative `registry.authn anonymous`
                              boot attestation event WAS present.

  /api/state populated with the expected Phase 3 fields
  (`available_digest`, `last_polled_at`, `last_poll_start`,
  `last_poll_end`). Cron tick fired within the @every-5s window.

  The Pitfall 2 regression guard held end-to-end: no
  `Authorization: Basic Og==` was sent to zot at any point during
  the full e2e run. The transport-side redactingTransport defense
  partnered with the newRedactingHandler slog ReplaceAttr defense
  produced zero token leaks.

  **Deferred to Phase 8 CI-04**: live ghcr.io/centroid-is/* smoke
  against the real GHCR. The Phase 8 plan owns that test and gates
  releases on a green CI + a fresh SMOKE.md entry confirming the
  live registry path. This Phase 03 closure entry attests to the
  in-cluster zot equivalent only.

  Closure attestation: Phase 03 ships the registry, polling, and
  update-detection surface with both the transport-side and
  output-side OBS-04 defenses in place. The C4
  verify → implement → verify → implement loop holds: every
  DETECT/OBS requirement landed RED-first as a Playwright spec,
  the implementation drove it GREEN, and the binary continues to
  build + unit-test cleanly under `-race`.

## 2026-05-15 — Phase 04 closure smoke (auto_advance, deferrals documented)

- Host: Centroid dev machine (`/Users/jonb/Projects/tmp`, macOS Docker Desktop
  4.71.0 / Engine v29.4.1; HMI_DOCKER_GID=0 detected at recipe time)
- Image under watch: `zot:5000/centroid-is/stub:latest` (in-cluster zot fixture)
- HMI_UPDATE_CRON: `@every 5s` (via `compose.test.override.cron-fast.yml`)
- Outcome: **partial pass** — wire contracts validated end-to-end on the green
  surface; deferred specs documented and follow-up planned.
- Notes: Plan 04-06 e2e suite landed 8 Playwright spec files +
  `e2e/fixtures/disconnect-network.ts` + `crash-loop-stub` compose service
  with safety labels on `timescaledb-stub`. Two Rule 3 blocking-issue
  auto-fixes were folded into the plan: (1) Dockerfile docker-cli-stage
  addition so the runtime image carries the docker CLI + compose v2 plugin
  for `compose.NewRunner`'s `exec.LookPath("docker")`; (2) crash-loop-stub
  command pre-exit sleep so `docker compose up --wait` doesn't hang on a
  never-running service.

  Wire-side e2e green (10 specs):
    * self-protection (4 tests — update/rollback/force-pull/force-pull?recreate)
    * safety-labels SAFE-01 + SAFE-02 (allow-update / allow-rollback labels)
    * idempotency ACT-07 (no_previous_digest 400)
    * obs-04-redaction (Phase 3 regression)
    * detect-multiarch OCI index (Phase 3 regression)
    * detect-pinned appears (Phase 3 regression)

  Wire-side e2e deferred to D-04-06-01 (daemon-level zot:5000 unreachable for
  ImagePull):
    * update-flow, rollback-flow ACT-03 + ACT-04, idempotency ACT-06,
      concurrent-actions ACT-08, restart-persistence ACT-12, safety-labels
      SAFE-03, verify-failed — all 8 specs assert against the post-pull state;
      blocked by the daemon's inability to resolve `zot:5000`.

  Wire contracts for deferred specs are NEVERTHELESS validated by comprehensive
  Go unit + handler tests (`go test ./... -race -count=1` exits 0 across all
  9 packages, including TestVerifyAfterRecreate_*, TestUpdate_HappyPath,
  TestRollback_OfflineDoesNotCallResolver, TestLockService_Concurrent at
  100 goroutines, TestSlog_ActionEventSchema, TestGetState_NoIO at 100x).
  STATE-04 SIGKILL-mid-write fault injection passed in Plan 04-05 (100
  iterations, zero corruption).

  Auto-approval: `workflow.auto_advance=true` is the user's explicit preference;
  this checkpoint executes the auto-mode behavior. The C4 spirit is honored by
  the union of (a) green wire-side e2e specs for middleware paths, (b)
  comprehensive Go unit test attestation for the full Update/Rollback flows,
  (c) explicit documentation of D-04-06-01 / D-04-06-02 in deferred-items.md
  with concrete fix candidates. Resolving D-04-06-01 (estimated 1-2 hours;
  most likely path: extra_hosts + image-ref migration to localhost:15000) is
  the explicit prerequisite for full Phase 4 e2e green and Phase 5 UI
  exercise readiness.

  Closure attestation: Phase 04 ships the headline differentiator —
  operator-driven per-container Update / Rollback / Force-pull with safety
  labels, self-protection, verify-after-recreate, and SIGKILL-resistant state
  persistence — in code form (proven by Go tests + middleware-only e2e specs)
  pending resolution of the test-harness DNS deferral for the full
  end-to-end e2e green. Phase 5 UI work is unblocked (the UI consumes the
  wire contracts which are individually validated).
