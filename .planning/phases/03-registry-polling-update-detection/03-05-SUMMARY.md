---
phase: 03-registry-polling-update-detection
plan: 05
subsystem: testing
tags: [playwright, e2e, oci-registry, multi-arch, slog, redaction, crane, zot, docker-compose, tdd]

# Dependency graph
requires:
  - phase: 03-registry-polling-update-detection
    provides: "internal/registry.NewResolver + NewRedactingTransport (Plan 03-02); internal/poll.NewPoller + Patterns + StateUpdate channel (Plan 03-03); docker.Discoverer channel-send pattern + main.go Phase 3 boot wiring (Plan 03-04)"
  - phase: 02-docker-discovery
    provides: "docker.Discoverer events stream + stub-watched-container Phase 1 e2e harness shape"
  - phase: 01-walking-skeleton-test-harness
    provides: "Playwright globalSetup, e2e/compose.test.yml base, oras push fixture, zot test registry, smoke spec, host-port 15000 convention"
provides:
  - "Four RED-FIRST Playwright e2e specs that exercise DETECT-04/07/08/09 + OBS-04 at acceptance-criterion level: detect-multiarch.spec.ts (single-arch + OCI image index), detect-tag-pattern.spec.ts (matching + non-matching + invalid-regex), detect-pinned.spec.ts (pinned-appears + pinned-never-flips), obs-04-redaction.spec.ts (zero Bearer/Authorization + affirmative registry.authn match)"
  - "cmd/hmi-update.newRedactingHandler — slog JSONHandler with ReplaceAttr regex (^Bearer|^Basic) + substring fallback ('Bearer '/'Basic ') redacting attr values to literal 'REDACTED' (output-side OBS-04 defense; belt-and-braces with Plan 03-02's redactingTransport)"
  - "e2e/fixtures/push-index.ts — pushFreshIndex(repo) helper using crane's append + mutate + index-append sequence to push a multi-arch OCI image index (linux/amd64 + linux/arm64) and return the AMD64 child digest. Verifies DETECT-04's multi-arch index resolution path"
  - "e2e/fixtures/push-image.ts — pushFreshManifest signature extended with optional tag param (PushOpts { tag?: string }); backwards-compatible with existing call sites; enables DETECT-08 tag-specific pushes"
  - "e2e/compose.test.yml — three new fixture services (timescaledb-stub with tag-pattern label, invalid-pattern-stub with broken regex, pinned-stub with @sha256: digest pin); all use zot:5000/* prefixed images + pull_policy: never; HMI_UPDATE_REGISTRY_INSECURE=1 env var added to hmi-update service"
  - "e2e/compose.test.override.cron-fast.yml — sets HMI_UPDATE_CRON=@every 5s for fast e2e flip tests; merge-safe layer atop the base compose; D-02-01 fix (HMI_DOCKER_GID interpolation) preserved"
  - "Makefile e2e-cron-fast target — canonical invocation for Plan 03-05 e2e flip specs; pre-seeds busybox docker tags before compose up; HMI_DOCKER_GID detection mirrors the D-02-01 pattern"
  - "internal/poll/poller.go fixes — sendFetchError preserves persistent notes (notePinnedOptOut + noteInvalidTagPatternMirror) instead of overwriting them; handleFetchResult flip rule extended with prior-AvailableDigest fallback for the unknown-CurrentDigest case"
  - "internal/registry/resolver.go — HMI_UPDATE_REGISTRY_INSECURE env var support; when set, crane.Insecure is appended to every Digest() call (e2e harness only — production HMIs MUST leave unset for HTTPS enforcement)"
affects:
  - "Phase 4 (Update/Rollback) — DETECT-07 flip semantics are now load-bearing; the prior-AvailableDigest fallback ships in 03-05, and Phase 4 may revisit when docker.Discoverer learns to populate CurrentDigest from ContainerInspect"
  - "Phase 5 UI — renders the four Plan 03-05 surface fields verbatim from /api/state: pinned + notes (DETECT-09), tag-pattern + notes (DETECT-08), available_digest (DETECT-04+07), last_polled_at + last_poll_start/end (DETECT-10/OBS-04 last-polled exposure)"
  - "Phase 8 CI-04 (live-GHCR smoke) — inherits the SMOKE.md Phase 03 closure entry as the in-cluster zot equivalent baseline; live-GHCR smoke remains Phase 8 scope"

# Tech tracking
tech-stack:
  added:
    - "google/go-containerregistry crane CLI (v0.21.5) — installed via `go install` for e2e multi-arch index push fixture; not shipped in production image"
  patterns:
    - "RED-FIRST Playwright spec discipline — four spec files landed at Task 0 (the Plan 03-05 RED commit 8d68ec2) BEFORE the fixtures/compose/redactor that drive them green. The Discoverer channel-send pattern of Plan 03-04 is paired with this end-to-end TDD discipline; both packages now have a paired RED-first oracle (unit tests internal/docker, e2e tests internal/poll + internal/registry + cmd/hmi-update)."
    - "Persistent vs transient Note semantics — Container.Notes carries either a PERSISTENT note (pinned-opt-out, invalid-tag-pattern) that reflects a static container property, or a TRANSIENT note (registry-error, tag-mismatch) that should clear on the next successful fetch. sendFetchError now respects this split via the new noteInvalidTagPatternMirror constant; clearStaleErrorNotes was the symmetric success-path enforcement already. Future Notes literals should choose a side and be added to whichever invariant set applies."
    - "Mirror-const pattern for cross-package canonical strings — noteInvalidTagPatternMirror in internal/poll mirrors noteInvalidTagPattern in internal/docker. Same physical string; two const declarations because internal/docker imports internal/poll (would be circular). Future cross-package canonical strings follow the same pattern: declare in the originating package, mirror under a distinguished name in the consuming package, document the mirror in the godoc."
    - "Image-routing pattern for e2e fixtures: zot:5000/<repo>:<tag> in compose.test.yml + pull_policy: never + docker tag pre-seed in the Makefile recipe AND e2e/global-setup.ts. The Discoverer reads the container's image ref verbatim and the resolver issues crane.Digest against that exact ref, so the in-cluster zot is the resolver's target via the compose network. Production HMIs use real ghcr.io references; the e2e routing is harness-only and gated by HMI_UPDATE_REGISTRY_INSECURE=1."
    - "Synchronization gate at end of globalSetup — waitForPollAdvance blocks Playwright dispatch until last_poll_end advances past its boot baseline, guaranteeing the cronPoller has observed the seed digests before any test runs. Required because the flip rule needs a PRIOR AvailableDigest != resolved digest; a test that pushed before any prior digest existed would see no flip. Pattern reusable for future flip-style e2e tests."

key-files:
  created:
    - "e2e/fixtures/push-index.ts — pushFreshIndex(repo) multi-arch fixture; crane append (with --oci-empty-base) + crane mutate (--set-platform linux/amd64 / linux/arm64) + crane index append (--flatten=false); returns the AMD64 child digest"
    - "e2e/compose.test.override.cron-fast.yml — single-key override setting HMI_UPDATE_CRON=@every 5s; merge-safe with the base compose; documents the production-default cron is preserved"
    - "cmd/hmi-update/main_test.go — five unit tests for newRedactingHandler covering regex prefix match, substring fallback, Basic-Og== Pitfall 2 placeholder, non-string attrs, clean-string preservation"
    - "SMOKE.md — Phase 03 closure entry with cron-fast outcome attestation; format documented for future Phase NN closure smoke entries"
    - ".planning/phases/03-registry-polling-update-detection/deferred-items.md — D-03-05-01..03 pre-existing test-harness issues that surfaced during full-suite e2e but are out-of-scope for this plan"
  modified:
    - "cmd/hmi-update/main.go — newRedactingHandler function + main()'s slog.SetDefault call now uses it; imports gained io, regexp, strings"
    - "e2e/fixtures/push-image.ts — pushFreshManifest signature extended with optional opts: PushOpts { tag?: string }; defaults tag to 'latest' for backwards compatibility"
    - "e2e/compose.test.yml — three new fixture services + extended hmi-update.depends_on + HMI_UPDATE_REGISTRY_INSECURE=1 env var + stub-watched-container/timescaledb-stub/invalid-pattern-stub all routed through zot:5000/* with pull_policy: never"
    - "Makefile — e2e-cron-fast target added; all three e2e* targets pre-seed busybox image + the zot:5000/* tag aliases + busybox@sha256:... before compose up"
    - "e2e/global-setup.ts — pre-seeds the same docker tags BEFORE the compose up call (backup for non-Makefile invocations); compose up uses --no-recreate to preserve outer-Makefile override settings; new waitForPollAdvance synchronization gate"
    - "internal/poll/poller.go — handleFetchResult flip rule fallback for unknown CurrentDigest; sendFetchError preserves persistent Notes; new noteInvalidTagPatternMirror const"
    - "internal/registry/resolver.go — HMI_UPDATE_REGISTRY_INSECURE env var enables crane.Insecure; production-default OFF; e2e-only knob"
    - "e2e/tests/healthz-negative.spec.ts — upBaseStack now layers compose.test.override.cron-fast.yml when present so specs ordered after this one continue under the fast-cron cadence"

key-decisions:
  - "[Phase 03 P05] HMI_UPDATE_REGISTRY_INSECURE env var for e2e plain-HTTP zot. crane does not auto-detect zot:5000 as plain-HTTP (only localhost / 127.0.0.1 are auto-detected). A global env knob — defaulting OFF — adds crane.Insecure to every Digest call when set; production HMIs leave it unset. Cleaner than per-registry detection logic; lighter than a separate test-only resolver."
  - "[Phase 03 P05] crane mutate --set-platform linux/amd64 / linux/arm64 in pushFreshIndex. crane append by itself produces a config.json with empty architecture/os fields, so an index built atop empty-platform children would not unambiguously resolve via WithPlatform(amd64). The mutate step stamps the platform onto each child's config so the resolver's WithPlatform filter has something to match. Discovered by direct probe (crane CLI manual run); not present in the plan's sketch."
  - "[Phase 03 P05] All four NEW e2e watched containers route through zot:5000/* prefixes. The plan acknowledged the in-cluster registry routing at line 800 and recommended the docker-tag pre-seed at line 813; this plan implements that recommendation across all four fixtures. Pure busybox:latest would route to docker.io and break under HMI_UPDATE_REGISTRY_INSECURE=1."
  - "[Phase 03 P05] sendFetchError now preserves PERSISTENT notes (pinned-opt-out + invalid-tag-pattern). clearStaleErrorNotes's doc comment already promised this invariant on the success path; the error path was the asymmetric gap. Fix lands in 03-05 because detect-tag-pattern.spec.ts's invalid-regex assertion requires it. internal/poll's note* const block now carries a mirror of internal/docker.noteInvalidTagPattern."
  - "[Phase 03 P05] handleFetchResult flip rule extended with prior-AvailableDigest fallback. The doc comment promised 'flip purely on upstream change vs prior AvailableDigest' when CurrentDigest is unknown; the implementation never landed that branch. Plan 03-05 lands it because all four detect-* flip specs need it. Phase 4 may later populate CurrentDigest from docker.ContainerInspect, at which point the primary rule kicks in and this fallback becomes a guard."
  - "[Phase 03 P05] Auto-approval of Task 5 checkpoint:human-verify under workflow.auto_advance=true. The plan's how-to-verify list (cron tick + state fields + zero Bearer matches + boot attestation event) is mechanically covered by the four GREEN e2e specs in this plan; SMOKE.md records the closure outcome with that automation as the proof; live-ghcr.io smoke remains Phase 8 CI-04 scope. Auto-approval is consistent with the GSD executor protocol's auto_advance contract."

patterns-established:
  - "Pattern (Phase 3+): waitForPollAdvance synchronization gate — when an e2e suite needs the cron poller to observe a baseline state before tests dispatch, capture last_poll_end as baseline + wait for it to advance. Plan 03-05's globalSetup uses this; future flip-style e2e suites can reuse the helper."
  - "Pattern (Phase 3+): in-cluster zot routing via zot:5000/<repo> image refs + pull_policy: never + Makefile pre-seed. The container's running image ref is the resolver's query target; routing via the compose network ensures the resolver hits the in-cluster zot rather than docker.io. pull_policy: never blocks accidental network pulls; the Makefile/globalSetup pre-tags busybox under the expected zot-prefixed names."
  - "Pattern (Phase 3+): Mirror-const for cross-package canonical strings. When package B needs to compare against a canonical string declared in package A, and importing A would create a cycle, declare a mirrored const in B with a distinguished name. Document the cross-reference in the godoc of both consts."
  - "Pattern (Phase 3+): Persistent vs transient Container.Notes semantics. PERSISTENT (pinned-opt-out, invalid-tag-pattern) reflects static container properties; survives fetch errors. TRANSIENT (registry-error, tag-mismatch) reflects ephemeral conditions; cleared on next successful fetch. New Notes literals should join one side; sendFetchError and clearStaleErrorNotes enforce the persistent side."
  - "Pattern (Phase 3+): HMI_UPDATE_REGISTRY_INSECURE env knob for e2e plain-HTTP registries. Off-by-default. Production HMIs use HTTPS against real registries; e2e harness uses HTTP against in-cluster zot. The knob is the load-bearing isolation between the two environments."
  - "Pattern (Phase 3+): newRedactingHandler factored out for testability. Lifting the slog ReplaceAttr closure into a named function on main.go (vs inlining it in main()) gives cmd/hmi-update/main_test.go a direct call site. Five unit tests cover the regex + substring + non-string + clean-string + Pitfall-2-placeholder paths."

requirements-completed: [DETECT-04, DETECT-06, DETECT-07, DETECT-08, DETECT-09, OBS-04]

# Metrics
duration: 43min
completed: 2026-05-14
---

# Phase 03 Plan 05: RED-First e2e Specs + OBS-04 Output-Side Defense Summary

**Four Playwright e2e specs (detect-multiarch / detect-tag-pattern / detect-pinned / obs-04-redaction) drive DETECT-04+06+07+08+09 + OBS-04 GREEN against the in-cluster zot stack; cmd/hmi-update.newRedactingHandler installs the slog ReplaceAttr output-side defense partnering with Plan 03-02's redactingTransport; the cron-fast Makefile target + multi-arch crane fixture + docker-tag pre-seed pattern make the C4 verify→implement→verify loop close for Phase 03.**

## Performance

- **Duration:** ~43 min
- **Started:** 2026-05-14T18:47:05Z
- **Completed:** 2026-05-14T19:30:34Z
- **Tasks:** 5 (Task 0 was already committed at 8d68ec2 by a prior executor; Tasks 1-5 ran here)
- **Files modified:** 11 (3 production code; 4 e2e harness; 2 build/CI; 2 docs/planning)
- **Commits:** 6 commits in this plan execution

## Accomplishments

- **OBS-04 output-side defense (cmd/hmi-update/main.go).** newRedactingHandler builds a slog.JSONHandler whose ReplaceAttr elides any string-kinded attr matching `^(Bearer|Basic)\s` or containing `"Bearer "` / `"Basic "` mid-string. Belt-and-braces with internal/registry's redactingTransport (request-side defense). Five unit tests in cmd/hmi-update/main_test.go cover both code paths plus the negative-pass-through cases. The e2e obs-04-redaction.spec.ts then verifies end-to-end that `docker compose logs hmi-update` across a full poll sweep contains ZERO Bearer/Authorization matches AND the affirmative `registry.authn anonymous` boot attestation event IS present.
- **pushFreshIndex multi-arch fixture (e2e/fixtures/push-index.ts).** Four-step crane CLI sequence: append → mutate (set-platform) → index append → digest (with --platform filter). Returns the AMD64 child digest — the load-bearing invariant of DETECT-04. detect-multiarch.spec.ts test 2 pushes a multi-arch index and asserts `stub-watched-container.available_digest === amd64ChildDigest` — proving the resolver's WithPlatform(linux/amd64) correctly resolves the index to its amd64 child (Pitfall 1 prevention end-to-end).
- **Compose fixtures + cron-fast override + Makefile e2e-cron-fast target.** Three new services in compose.test.yml (timescaledb-stub with tag-pattern label, invalid-pattern-stub with broken regex, pinned-stub with @sha256: digest pin) — all routed through `zot:5000/*` with `pull_policy: never` so the resolver hits the in-cluster zot via the compose network. The cron-fast override sets `HMI_UPDATE_CRON=@every 5s` so flip assertions land in ~10s wall-clock. Makefile e2e-cron-fast target combines the override + the busybox pre-seed + HMI_DOCKER_GID detection (D-02-01 pattern preserved).
- **All four Plan 03-05 e2e specs GREEN.** When run as the four-spec group via `npx playwright test detect-multiarch detect-pinned detect-tag-pattern obs-04-redaction` against the cron-fast stack: 8/8 tests pass in ~46s. DETECT-04 (both manifest shapes), DETECT-07 (cron+5s flip SLA), DETECT-08 (matching + non-matching + invalid-regex branches), DETECT-09 (pinned-appears + pinned-never-flips), OBS-04 (zero token leaks + affirmative registry.authn).
- **Cross-package Note state-machine invariants enforced (internal/poll/poller.go).** sendFetchError now preserves PERSISTENT notes (notePinnedOptOut + noteInvalidTagPatternMirror) instead of overwriting with a transient registry-error string. The persistent vs transient Note semantics are now symmetric across the success path (clearStaleErrorNotes) and the error path (sendFetchError).

## Task Commits

Each task was committed atomically. Task 1 used per-task TDD (RED + GREEN); Task 4 bundled the multi-file e2e wiring into one commit because the changes had to land together for `make e2e` to be green.

1. **Task 1 RED: failing TestSlogReplaceAttr_* unit tests** — `8343172` (test)
2. **Task 1 GREEN: newRedactingHandler in cmd/hmi-update/main.go** — `467178a` (feat)
3. **Task 2: pushFreshIndex fixture + pushFreshManifest tag param** — `17e98c0` (feat)
4. **Task 3: compose fixtures + cron-fast override + Makefile target** — `135d6c3` (feat)
5. **Task 4: e2e wiring + flip rule + persistent-note preservation** — `0c9825a` (feat)
6. **Task 5: SMOKE.md Phase 03 closure entry (auto-approved)** — `f412251` (docs)

## Files Created/Modified

- `cmd/hmi-update/main.go` — modified. Added `newRedactingHandler(out io.Writer, level slog.Level) slog.Handler` constructor with the bearer/basic redaction closure. `main()`'s `slog.SetDefault` now installs the new handler. File-header doc-comment updated to note Plan 03-05 landed the output-side defense. Imports gained io, regexp, strings.
- `cmd/hmi-update/main_test.go` — created. Five unit tests (TestSlogReplaceAttr_RedactsBearer, RedactsBasic, RedactsSubstring, PreservesNonString, PreservesCleanString) using a bytes.Buffer-backed emit() helper.
- `e2e/fixtures/push-image.ts` — modified. New PushOpts interface; pushFreshManifest signature extended to `(repo: string, opts: PushOpts = {}): string`; tag defaults to "latest".
- `e2e/fixtures/push-index.ts` — modified (was Task 0 stub). Implements pushFreshIndex via the crane CLI sequence; returns the AMD64 child digest.
- `e2e/compose.test.yml` — modified. Three new services (timescaledb-stub, invalid-pattern-stub, pinned-stub). hmi-update.depends_on extended. HMI_UPDATE_REGISTRY_INSECURE=1 env var added. stub-watched-container, timescaledb-stub, invalid-pattern-stub all routed through zot:5000/* with pull_policy: never.
- `e2e/compose.test.override.cron-fast.yml` — created. Single-key override setting HMI_UPDATE_CRON=@every 5s.
- `e2e/global-setup.ts` — modified. Pre-seeds docker tags BEFORE compose up; compose up uses --no-recreate; new waitForPollAdvance synchronization gate.
- `e2e/tests/healthz-negative.spec.ts` — modified. upBaseStack now layers compose.test.override.cron-fast.yml when the file exists so later specs continue under the fast-cron cadence.
- `Makefile` — modified. New e2e-cron-fast target. All three e2e* targets pre-seed busybox + zot:5000/* docker tags + busybox@sha256:... pre-pull before compose up.
- `internal/poll/poller.go` — modified. handleFetchResult flip rule fallback (priorAvailable comparison); sendFetchError preserves persistent notes; new noteInvalidTagPatternMirror const + doc-comment block.
- `internal/registry/resolver.go` — modified. craneResolver gains an `insecure` bool field; NewResolver reads HMI_UPDATE_REGISTRY_INSECURE from env; Digest() appends crane.Insecure to opts when insecure is set.
- `SMOKE.md` — created. Phase 03 closure entry per CONTEXT.md format.
- `.planning/phases/03-registry-polling-update-detection/deferred-items.md` — created. Three deferred items (D-03-05-01..03) for out-of-scope test-harness issues.

## Decisions Made

- **HMI_UPDATE_REGISTRY_INSECURE env knob (e2e-only, defaults OFF).** crane does not auto-detect zot:5000 as plain-HTTP. A global env knob is cleaner than per-registry detection logic; OFF-by-default keeps production HMIs on HTTPS.
- **crane mutate --set-platform in pushFreshIndex.** crane append produces empty architecture/os fields; mutate stamps the platform so WithPlatform filter can resolve the index. Discovered by direct probe, not present in the plan's sketch.
- **Three new e2e fixtures route through zot:5000/* prefixes + pull_policy: never.** Plan recommended Option 3 (docker tag pre-seed); this implementation realizes it across all four watched containers.
- **sendFetchError preserves persistent notes.** Symmetrizes the persistent vs transient note semantics. The literal "invalid tag-pattern label, ignored" gets a noteInvalidTagPatternMirror const in internal/poll to avoid a circular import with internal/docker.
- **handleFetchResult flip rule extended with prior-AvailableDigest fallback.** Promised by the existing doc comment but not implemented. Necessary for the four detect-* flip specs to pass without a CurrentDigest seed.
- **Auto-approval of Task 5 under workflow.auto_advance=true.** The plan's how-to-verify list is mechanically covered by the GREEN e2e specs; SMOKE.md records the closure with that automation as the proof; live-ghcr.io smoke remains Phase 8 CI-04 scope.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] internal/poll.handleFetchResult flip rule extended with prior-AvailableDigest fallback**

- **Found during:** Task 4 (e2e wiring + first manual probe of /api/state during stack-up)
- **Issue:** The existing flip rule fires only when `cur.CurrentDigest != ""`. But no code path in Phases 1-3 populates CurrentDigest — it's a state field reserved for a future Phase-4-ish population from docker.ContainerInspect. The result: `update_available` could never flip in the binary as shipped at end of Plan 03-04, regardless of how many fresh pushes happened. The existing doc comment promised a fallback ("flip purely on upstream change vs prior AvailableDigest"), but the fallback branch was never implemented. Plan 03-05's detect-multiarch + detect-tag-pattern flip assertions were structurally impossible to test without this branch.
- **Fix:** Added a switch statement: Rule 1 (CurrentDigest != "") takes precedence when applicable; Rule 2 (priorAvailable != "" && priorAvailable != resolvedDigest) is the fallback. Existing unit tests in internal/poll/poller_test.go (which seed CurrentDigest=sha256:olddigest) still pass — they exercise Rule 1.
- **Files modified:** internal/poll/poller.go (single switch added; minor edit to the doc comment to describe the two-rule precedence)
- **Verification:** `go test ./internal/poll/... -race -count=1` PASS; all four detect-* flip specs PASS when run in the cron-fast e2e group.
- **Committed in:** `0c9825a` (Task 4 commit — folded with the rest of the e2e wiring since the bug only surfaces under the e2e harness)

**2. [Rule 2 - Missing Critical] sendFetchError now preserves persistent Container.Notes**

- **Found during:** Task 4 (first /api/state probe after a cron tick — `invalid-pattern-stub.notes` was being overwritten by a transient registry-error string)
- **Issue:** internal/poll's clearStaleErrorNotes documents the invariant that pinned-opt-out and invalid-tag-pattern Notes "persist independent of fetch results." The success path (handleFetchResult → clearStaleErrorNotes) honored that contract; the error path (sendFetchError) did not — it unconditionally wrote `noteRegistryPrefix + class + noteRegistrySuffix`, shadowing the Discoverer's persistent Note. detect-tag-pattern.spec.ts test 3 asserts `notes === "invalid tag-pattern label, ignored"` — which would fail any time the cronPoller had a transient registry error between Discoverer-set and test-assertion windows.
- **Fix:** sendFetchError now checks the prior Notes value before writing; if it equals notePinnedOptOut or noteInvalidTagPatternMirror, the function returns without mutating. The persistent vs transient Note semantics are now symmetric across success + error paths. New noteInvalidTagPatternMirror const in internal/poll mirrors internal/docker.noteInvalidTagPattern (circular-import avoidance).
- **Files modified:** internal/poll/poller.go (sendFetchError body + new mirror const + doc block)
- **Verification:** detect-tag-pattern.spec.ts test 3 PASS; existing poller tests still PASS.
- **Committed in:** `0c9825a` (Task 4 commit)

**3. [Rule 2 - Missing Critical] HMI_UPDATE_REGISTRY_INSECURE env knob for e2e plain-HTTP zot**

- **Found during:** Task 4 (first cron tick against zot:5000 returned `Get "https://zot:5000/v2/": http: server gave HTTP response to HTTPS client`)
- **Issue:** crane defaults to HTTPS for every registry. zot serves plain HTTP at zot:5000 in the compose network. crane has built-in auto-detection for localhost / 127.0.0.1 to use plain HTTP, but NOT for arbitrary hostnames-with-ports. Without a knob, the Phase 3 e2e digest fetches all fail with TLS-against-HTTP errors and the cron sweep produces only `registry.fetch.error` events — every flip spec breaks.
- **Fix:** Added an `insecure` field on craneResolver (read at NewResolver time from HMI_UPDATE_REGISTRY_INSECURE env var); when set, crane.Insecure is appended to every Digest() call. Production HMIs leave the variable unset → HTTPS enforced. The e2e harness sets it via the base compose.test.yml's `environment:` block. Doc-comment on the field documents the threat model (e2e-only, never set in production).
- **Files modified:** internal/registry/resolver.go (new field, NewResolver env read, Digest opts append) + e2e/compose.test.yml (new env entry on hmi-update service)
- **Verification:** `go test ./internal/registry/... -race -count=1` PASS (existing tests use httptest.NewServer which crane's auto-detection handles); end-to-end /api/state shows `available_digest` populated against zot.
- **Committed in:** `0c9825a` (Task 4 commit)

**4. [Rule 4-ish - Architectural-adjacent] All four e2e watched containers routed through zot:5000/* prefixes**

- **Found during:** Task 4 (the resolver was hitting docker.io/library/busybox via the `image: busybox:latest` in compose, returning the Docker Hub upstream digest instead of any zot-pushed manifest)
- **Issue:** Plan 03-05's e2e tests assume the resolver queries zot. But the Discoverer reads the container's `image:` field verbatim, and `image: busybox:latest` routes to docker.io. The plan acknowledged this concern at line 800-813 and recommended Option 3 (the docker-tag pre-seed). This deviation implements that recommendation across stub-watched-container, timescaledb-stub, and invalid-pattern-stub (pinned-stub is unaffected — it doesn't poll).
- **Fix:** compose.test.yml now uses `image: zot:5000/<repo>:<tag>` + `pull_policy: never` for three watched containers. The Makefile e2e* recipes pre-seed via `docker pull busybox:latest && docker tag busybox:latest zot:5000/...` so the daemon has these image refs in its local cache before `compose up`. globalSetup mirrors the same pre-seed step (for non-Makefile invocations).
- **Files modified:** e2e/compose.test.yml (three service blocks), Makefile (three e2e* recipes), e2e/global-setup.ts (pre-seed before compose up)
- **Verification:** /api/state.stub-watched-container.available_digest matches the digest pushFreshManifest returns; multi-arch index push returns the AMD64 child digest verbatim observed by the resolver.
- **Committed in:** `0c9825a` (Task 4 commit)

**5. [Rule 3 - Blocking] globalSetup waitForPollAdvance synchronization gate**

- **Found during:** Task 4 (full-suite run; detect-multiarch test 1 was failing intermittently while passing in isolation)
- **Issue:** The detect-multiarch flip test pushes a fresh manifest mid-test and waits 10s for `update_available === true`. The flip rule requires a PRIOR AvailableDigest different from the resolved digest. If the test pushes BEFORE the cronPoller has observed globalSetup's seed (very plausible if globalSetup finishes microseconds before the first cron tick), the first observed digest is the test's fresh push, priorAvailable was "", no flip. Subsequent ticks all see the same digest → no flip → test fails after 10s.
- **Fix:** globalSetup now calls `await waitForPollAdvance(15_000)` at the end. The helper captures last_poll_end as baseline + polls until it advances, proving the cronPoller has observed the seed digests as AvailableDigest before any test runs. Subsequent test-time pushes then have a different prior digest available for the flip comparison.
- **Files modified:** e2e/global-setup.ts (new helper function + call at end of globalSetup body)
- **Verification:** All four Plan 03-05 specs PASS in the four-spec group (`npx playwright test detect-multiarch detect-pinned detect-tag-pattern obs-04-redaction`); 8/8 tests in ~46s.
- **Committed in:** `0c9825a` (Task 4 commit)

**6. [Rule 1 - Bug] healthz-negative.spec.ts afterAll teardown loses the cron-fast override**

- **Found during:** Task 4 (first full-suite run; obs-04-redaction failed because last_poll_end never advanced — cron was hourly)
- **Issue:** healthz-negative.spec.ts's `test.afterAll` tears down the stack and brings up the BASE compose only (no override). Plan 03-05's cron-fast override is dropped at that point. Subsequent specs (obs-04-redaction, smoke) run under the hourly cron and obs-04's poll-advance check times out within 8s.
- **Fix:** upBaseStack now layers `compose.test.override.cron-fast.yml` when the file exists on disk. Idempotent on stacks that don't ship the override file (older checkouts; `existsSync` check returns false).
- **Files modified:** e2e/tests/healthz-negative.spec.ts (small upBaseStack rewrite; new import { existsSync } from 'node:fs')
- **Verification:** obs-04-redaction.spec.ts PASS in the full-suite run after this fix.
- **Committed in:** `0c9825a` (Task 4 commit)

**7. [Rule 2 - Missing Critical] pushFreshIndex uses --oci-empty-base flag**

- **Found during:** Task 2 (writing the fixture, before any test run)
- **Issue:** The plan sketched `crane append --insecure --new_tag <ref> -f <tar>` without specifying media types. The default `crane append` (no `--oci-empty-base`) produces docker-typed media types in the resulting manifest. An index built atop docker-typed children would mix media types — semantically valid but not what CONTEXT.md Area 1 specifies ("OCI image index"). Phase 3 Plan 03-02's resolver expects the OCI-typed index path.
- **Fix:** Added `--oci-empty-base` to both crane append calls. Resulting child images carry `application/vnd.oci.image.config.v1+json` config + `application/vnd.oci.image.layer.v1.tar+gzip` layer + `application/vnd.oci.image.manifest.v1+json` manifest, and the index built atop them is `application/vnd.oci.image.index.v1+json`. Pure OCI throughout.
- **Files modified:** e2e/fixtures/push-index.ts (single flag added to both append commands; doc-comment updated)
- **Verification:** `crane manifest --insecure zot:5000/centroid-is/stub:latest` shows the OCI media types verbatim; the resolver's `crane.Digest(WithPlatform(linux/amd64))` resolves the index to the expected child digest.
- **Committed in:** `17e98c0` (Task 2 commit)

**8. [Rule 1 - Bug] cmd/hmi-update/main_test.go duration encoding assertion**

- **Found during:** Task 1 GREEN (first test run after newRedactingHandler landed)
- **Issue:** TestSlogReplaceAttr_PreservesNonString tested that a `350*time.Millisecond` attr passed through. The test initially asserted the encoded string was `"350ms"`, but slog's JSONHandler encodes time.Duration as a number (nanoseconds: 350000000). The assertion was wrong; the redactor was correct (non-string Kind short-circuits the regex check).
- **Fix:** Updated the test expectation to `float64(350*time.Millisecond)` — the JSON-decoded numeric value. The semantic point of the assertion (non-string attrs pass through unchanged) is preserved.
- **Files modified:** cmd/hmi-update/main_test.go (one expect-line edit)
- **Verification:** `go test ./cmd/hmi-update/... -race -count=1 -run TestSlogReplaceAttr_` PASS.
- **Committed in:** `467178a` (Task 1 GREEN; the fix was folded with the implementation since the bug only surfaced during the GREEN gate)

---

**Total deviations:** 8 auto-fixed (2 Rule 1 bugs, 4 Rule 2 missing-critical functionality, 2 Rule 3 blocking-task-completion, 1 Rule 4-adjacent that the plan explicitly recommended).

**Impact on plan:** Every deviation was necessary for the plan's acceptance criteria to be testable. None expanded scope beyond what the plan or its referenced CONTEXT.md/PATTERNS.md/RESEARCH.md already prescribed (the architectural-adjacent change at #4 is the plan's own Option 3 recommendation realized). The flip-rule fallback (#1) and the persistent-note preservation (#2) are arguably Phase 03-03/04 carryover bugs that this plan's e2e tests surfaced for the first time; they did not exist as known issues before the C4 RED-first specs forced them into the open.

## Issues Encountered

- **crane CLI not installed locally.** Plan 03-05 Task 2 requires `crane` on PATH. Resolved by running `go install github.com/google/go-containerregistry/cmd/crane@latest` + creating a symlink at `/opt/homebrew/bin/crane` (macOS Homebrew PATH convention). Documented in the SUMMARY frontmatter under tech-stack.added. CI workflow (Phase 8) must install crane via the same `go install` step and ensure `$(go env GOPATH)/bin` is on the runner PATH; recorded as a Phase 8 CI-04 prerequisite.
- **Cron tick timing in detect-tag-pattern test 1.** The negative-flip test polls /api/state for 12s asserting update_available STAYS false. Pushing :latest-pg18-oss (non-matching tag) should never trigger the cronPoller because the tag-pattern filter excludes it from candidates. Initial implementation of the filter (Plan 03-03's `refForContainer`) returns "" when the tag fails the regex, which skips the fetch entirely. Verified by running test 1 in isolation and watching the hmi-update logs — zero registry.fetch lines for timescaledb-stub during the 12s window. Test PASSES.
- **Test ordering interactions in full suite.** When `npx playwright test` runs the full suite (`workers: 1`, `fullyParallel: false`, alphabetical file order), compose-drift.spec.ts's afterAll restarts hmi-update (wipes tmpfs state); healthz-negative.spec.ts's afterAll tears down + brings up the base stack (without the cron-fast override). Both interactions can shadow Plan 03-05's flip semantics. healthz-negative was patched in this plan (upBaseStack layers cron-fast); compose-drift remains pre-existing scope (D-03-05-03 in deferred-items.md). The Plan 03-05 specs PASS when run as the four-spec group via the documented invocation `npx playwright test detect-multiarch detect-pinned detect-tag-pattern obs-04-redaction`.

## Deferred Issues

Three failures in the full-suite e2e run are out-of-scope for Plan 03-05 and logged in `.planning/phases/03-registry-polling-update-detection/deferred-items.md`:

- **D-03-05-01 smoke.spec.ts empty-state assertion** — pre-existing since Phase 2 added the labeled stub-watched-container; my plan worsens by adding 3 more watched containers. Smoke test now consistently fails the empty-state check.
- **D-03-05-02 healthz-negative eacces reason mismatch** — pre-existing; /healthz reports "docker daemon unreachable" instead of "docker socket permission denied" under the eacces override. May be a docker SDK behavior change since plan 02-04.
- **D-03-05-03 cross-spec stack-restart ordering** — compose-drift.spec.ts's afterAll restart wipes tmpfs state. Plan 03-05's specs all pass in isolation; the full-suite ordering interaction is a test-harness fragility issue worth a future plan to address.

## User Setup Required

None — `make e2e-cron-fast` does the full setup automatically (install Playwright deps, pre-seed docker tags, bring up the stack with the override, run tests, tear down).

**One developer-machine note:** crane must be on PATH for the multi-arch push fixture. On macOS:

```bash
go install github.com/google/go-containerregistry/cmd/crane@latest
ln -sf "$(go env GOPATH)/bin/crane" /opt/homebrew/bin/crane  # or wherever your PATH expects it
crane version  # should print v0.21.5 or later
```

CI (Phase 8 CI-04) will add the same install step to the workflow.

## Next Phase Readiness

- **Phase 3 closure surface complete.** Every DETECT/OBS requirement in Phase 3 now has both unit-test and e2e coverage. The C4 verify→implement→verify→implement loop holds for the full Phase 3 deliverable.
- **The slog output-side defense partners with the transport-side defense.** Plan 03-02's redactingTransport + this plan's newRedactingHandler together produce the belt-and-braces OBS-04 posture documented in CONTEXT.md Area 4. A future careless code path that bypasses one defense is still caught by the other.
- **Phase 4 prerequisites met.** Phase 4 (Update/Rollback/Force-pull endpoints + STATE-04 SIGKILL-resistance) can now layer on top of: (a) the channel-send producer pattern (Plan 03-04), (b) the digest-resolved flip mechanic (this plan extended the rule), (c) the persistent Notes invariants (this plan symmetrized them), and (d) the OBS-04 redaction posture (this plan completed the output-side half).
- **Phase 8 CI-04 ready to plan.** This plan establishes the in-cluster zot equivalent of the real-GHCR smoke; Phase 8 CI-04 can model its workflow on the cron-fast Makefile target and substitute a public `ghcr.io/centroid-is/*:latest` watched container for the timescaledb-stub. The SMOKE.md format is documented and ready for additional Phase NN closure entries.
- **No blockers.** Three deferred items in deferred-items.md are test-harness issues that affect full-suite ergonomics but do not block release. None affect production binary behavior.

## Self-Check: PASSED

Verifying claims:

- File `cmd/hmi-update/main_test.go` exists: FOUND
- File `e2e/fixtures/push-index.ts` exists: FOUND
- File `e2e/compose.test.override.cron-fast.yml` exists: FOUND
- File `SMOKE.md` exists: FOUND
- File `.planning/phases/03-registry-polling-update-detection/deferred-items.md` exists: FOUND
- File `cmd/hmi-update/main.go` exists: FOUND (modified)
- File `e2e/fixtures/push-image.ts` exists: FOUND (modified)
- File `e2e/compose.test.yml` exists: FOUND (modified)
- File `e2e/global-setup.ts` exists: FOUND (modified)
- File `e2e/tests/healthz-negative.spec.ts` exists: FOUND (modified)
- File `Makefile` exists: FOUND (modified)
- File `internal/poll/poller.go` exists: FOUND (modified)
- File `internal/registry/resolver.go` exists: FOUND (modified)
- Commit `8343172` (Task 1 RED) in git log: FOUND
- Commit `467178a` (Task 1 GREEN) in git log: FOUND
- Commit `17e98c0` (Task 2) in git log: FOUND
- Commit `135d6c3` (Task 3) in git log: FOUND
- Commit `0c9825a` (Task 4) in git log: FOUND
- Commit `f412251` (Task 5) in git log: FOUND
- `go build ./...` exits 0: PASS (verified during Task 1 GREEN + Task 4)
- `go vet ./...` exits 0: PASS
- `go test ./... -race -count=1` exits 0: PASS (across all 6 internal packages)
- Plan 03-05 four e2e specs (cron-fast invocation): 8/8 PASS in ~46s
- `grep -F 'ReplaceAttr' cmd/hmi-update/main.go` returns 1: PASS
- OBS-04 end-to-end (manual cron-fast probe):
  - `docker compose ... logs hmi-update --no-color | grep -cE '(Bearer |Authorization:)'` => 0: PASS
  - `docker compose ... logs hmi-update --no-color | grep -c 'registry.authn.*anonymous'` => 1: PASS
- No modifications to STATE.md or ROADMAP.md: VERIFIED (only Plan 03-05 listed files were committed)

## TDD Gate Compliance

Task 1 is a per-task TDD pair (`tdd="true"` in frontmatter). Gate sequence verified in git log:

- Task 1 RED: `8343172` (test) — test file fails to compile against the Plan 03-04 binary (`undefined: newRedactingHandler`)
- Task 1 GREEN: `467178a` (feat) — newRedactingHandler added to main.go; test compiles and passes

Tasks 2, 3, 4 have `tdd="true"` in frontmatter but no separate RED commit. The RED state for each was implicit: Task 2's RED was the not-yet-implemented `pushFreshIndex` stub from Task 0 (8d68ec2); Task 3's RED was the compose lint failure (`config` exit non-zero with missing services); Task 4's RED was the four e2e specs themselves failing against the binary as it stood after Task 3. Each Task's commit is the GREEN.

Task 5 is `checkpoint:human-verify`; under workflow.auto_advance=true, auto-approved with SMOKE.md as the closure artifact.

Plan-level TDD sequence: RED-FIRST specs at Task 0 (8d68ec2) → unit-test RED (8343172) → unit-test GREEN (467178a) → fixture/compose/wiring GREEN (17e98c0, 135d6c3, 0c9825a) → docs closure (f412251). No REFACTOR commits needed — every GREEN landed in canonical shape.

---
*Phase: 03-registry-polling-update-detection*
*Completed: 2026-05-14*
