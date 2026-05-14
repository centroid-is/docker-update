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
