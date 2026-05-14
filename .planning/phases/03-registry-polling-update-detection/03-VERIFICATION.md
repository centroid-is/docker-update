---
phase: 3
phase_name: Registry, Polling & Update Detection
verified: 2026-05-14
status: passed
score: 5/5 must-haves verified
overrides_applied: 0
re_verification:
  previous_status: none
  previous_score: n/a
deferred:
  - truth: "Manual smoke against a real ghcr.io/centroid-is/* image confirms 200 OK from a local crane.Digest() call"
    addressed_in: "Phase 8"
    evidence: "ROADMAP.md Phase 8 SC #3: 'Real-GHCR smoke job runs a single read-only crane.Digest() against a frozen public image (e.g. a stable ghcr.io/centroid-is/* reference) and asserts 200 — fails loudly if anonymous token flow regresses (Pitfall 2 belt-and-braces; note: this smoke targets a Phase 3 concern but lives in the CI surface, hence its placement here)'."
human_verification:
  - test: "Optional: re-run `make e2e-cron-fast` on this checkout to reproduce the green 8/8 attestation that SMOKE.md records"
    expected: "All four Plan 03-05 specs PASS (detect-multiarch, detect-tag-pattern, detect-pinned, obs-04-redaction); 8/8 in ~46s"
    why_human: "Verifier ran the static code/test checks and unit-test suite (go test -race PASS); a live docker-compose e2e takes ~46s of operator wall-clock and is intentionally manual under C4 verify→implement→verify→implement. Phase 8 will fold this into CI."
---

# Phase 3: Registry, Polling & Update Detection Verification Report

**Phase Goal:** Implement digest detection that is correct for both multi-arch indices and direct single-arch manifests, anonymous-token-flow safe against GHCR/Docker Hub, and serialized through a single-consumer poll channel — the WUD 8.2.2 bug class is designed out from the first red test.

**Verified:** 2026-05-14
**Status:** passed (with one deferred sub-clause routed to Phase 8 per ROADMAP)
**Re-verification:** No — initial verification

## Goal Achievement

### Observable Truths (ROADMAP.md Success Criteria)

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Playwright e2e test (red-first) pushes BOTH OCI image index AND direct single-arch manifests; affected container flips to `update_available` within `cron + 5 s` | VERIFIED | `e2e/tests/detect-multiarch.spec.ts` lines 73-126 contains two tests: (a) `pushFreshManifest` (single-arch) → expects `update_available===true` and `available_digest===newDigest` within 10s; (b) `pushFreshIndex` (multi-arch OCI image index from `e2e/fixtures/push-index.ts` with `--oci-empty-base` + `crane mutate --set-platform linux/amd64/arm64`) → expects `available_digest===amd64ChildDigest` (NOT the index digest). Cron-fast SLA is `@every 5s` per `compose.test.override.cron-fast.yml`. Resolver applies `crane.WithPlatform(amd64Platform)` at `internal/registry/resolver.go:212`. |
| 2 | `timescaledb` with `hmi-update.tag-pattern=^latest-pg17$` does NOT flip when `:latest-pg18-oss` is pushed | VERIFIED | `e2e/tests/detect-tag-pattern.spec.ts` test 1 (lines 63-96): pushes `latest-pg18-oss` and asserts `update_available` STAYS false for 12s wall-clock (≥2 cron ticks at @every 5s); also asserts notes never reads `/invalid tag-pattern/`. Test 2 (lines 98-124) is the positive counterpart proving the matching tag DOES flip. `cronPoller.refForContainer` returns "" when tag fails the regex, skipping the fetch (`internal/poll/poller.go` Patterns + refForContainer). |
| 3 | A container with `image: ...@sha256:...` digest-pinned reference is excluded from the watched list with a `pinned: opt-out` note in `/api/state` | VERIFIED | `e2e/tests/detect-pinned.spec.ts` test 1 (lines 61-77): asserts `pinned-stub.pinned===true` AND `notes==="pinned: opt-out"`. Test 2 (lines 79-109): asserts the pinned container never flips `update_available` after an upstream push. Production code: `internal/docker/discovery.go:454` sets `Pinned = strings.Contains(imageRef, "@sha256:")`; `internal/poll/poller.go:308-313` in `eligibleContainers` skips pinned + calls `sendPinnedNote` which writes the canonical `state.NotePinnedOptOut="pinned: opt-out"` literal (`internal/state/notes.go:45`). |
| 4 | `grep "Bearer "` and `grep "Authorization"` against captured slog output across a full test run return zero matches | VERIFIED | `e2e/tests/obs-04-redaction.spec.ts` lines 79-99 captures `docker compose logs --no-color hmi-update` across a full poll sweep (waits for `last_poll_end` to advance past baseline) and asserts the logs do NOT match `/Bearer /`, `/Authorization:/i`, or `/Basic Og==/` (Pitfall 2 literal). Affirmative check (line 97-99): the boot attestation `registry.authn.*anonymous` MUST be present so the spec cannot false-green on an empty stream. Double defense: `internal/registry/transport.go` redactingTransport strips Authorization+WWW-Authenticate+X-Registry-Auth+Proxy-Authorization headers from any logged copy; `cmd/hmi-update/main.go:90-118` `newRedactingHandler` slog `ReplaceAttr` redacts (a) `^(Bearer|Basic)\s` regex, (b) `"Bearer "`/`"Basic "` substring, (c) bare base64-encoded `username:password` shape (CR-03 fix). Unit tests in `cmd/hmi-update/main_test.go` cover all three branches and PASS under `go test -race`. |
| 5 | Manual smoke on an HMI-like stack with a real `ghcr.io/centroid-is/*` image confirms the anonymous token flow does not send `Authorization: Basic Og==` (Pitfall 2 prevention; one local `crane.Digest()` call succeeds 200) | VERIFIED (partial; sub-clause deferred to Phase 8) | `SMOKE.md` Phase 03 closure entry (2026-05-14) records cron-fast `make e2e-cron-fast` run against the in-cluster zot (the documented "GHCR analog" for the Phase 03 development cycle): all four DETECT/OBS specs PASS, ZERO `Bearer/Authorization/Basic Og==` matches, and the `registry.authn anonymous` boot attestation event is present. The Pitfall 2 prevention half of SC#5 is fully verified by the zot smoke + transport_test.go regression guard. The literal "one local `crane.Digest()` call against real ghcr.io/centroid-is/*" half is explicitly deferred to Phase 8 SC#3 by ROADMAP language ("note: this smoke targets a Phase 3 concern but lives in the CI surface"). SMOKE.md documents the deferral. |

**Score:** 5/5 truths verified (with criterion 5's "real-GHCR call" sub-clause routed to Phase 8 by explicit ROADMAP placement).

### Deferred Items

| # | Item | Addressed In | Evidence |
|---|------|-------------|----------|
| 1 | One local `crane.Digest()` call against a real `ghcr.io/centroid-is/*` image succeeds 200 (the live-network half of SC#5) | Phase 8 | ROADMAP.md Phase 8 SC #3 verbatim: "Real-GHCR smoke job runs a single read-only `crane.Digest()` against a frozen public image (e.g. a stable `ghcr.io/centroid-is/*` reference) and asserts 200 — fails loudly if anonymous token flow regresses (Pitfall 2 belt-and-braces; note: this smoke targets a Phase 3 concern but lives in the CI surface, hence its placement here)." SMOKE.md Phase 03 entry also documents the deferral in its closing paragraph. |

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/registry/resolver.go` | `craneResolver` w/ WithPlatform(amd64), authn.Anonymous, ctx, redacting transport | VERIFIED | All four options applied at lines 209-214. `authn.Anonymous` literal at line 211; `crane.WithPlatform(amd64Platform)` at 212. HMI_UPDATE_REGISTRY_INSECURE knob for e2e at 215-220. |
| `internal/registry/transport.go` | `redactingTransport` wraps DefaultTransport; strips Authorization/WWW-Authenticate/X-Registry-Auth/Proxy-Authorization | VERIFIED | Constant list at lines 45-48; wraps `http.DefaultTransport` at line 72; `NewRedactingTransport()` exposed. |
| `internal/registry/errors.go` | Sentinel `ErrPermanent`/`ErrTransient` + classify() | VERIFIED | Tests pass under -race. |
| `internal/poll/poller.go` | cronPoller body, bounded errgroup, SetLimit-before-Go, ctx-aware send (CR-01), local cronInst (CR-02), eligibleContainers skips pinned | VERIFIED | `errgroup.WithContext` line 267 immediately followed by `g.SetLimit(p.concurrency)` line 268 BEFORE any `g.Go` (line 279); `send(ctx, u)` is ctx-aware at lines 504-508; `cron.New` is local to `Run` at lines 220-223 (CR-02). |
| `internal/poll/patterns.go` | Patterns compiled regex cache, RWMutex-protected | VERIFIED | Tests pass under -race. |
| `internal/poll/channel.go` | `StateUpdate` type + `RunUpdater` single consumer + drain-on-ctx-cancel | VERIFIED | `runUpdater` lines 110-135: drains pending messages on `<-ctx.Done()` before returning. `StateUpdate.Apply` doc explicitly forbids I/O inside the closure. |
| `internal/state/schema.go` | Container.AvailableDigest/LastPolledAt/Notes; State.LastPollStart/End/Error | VERIFIED | Lines 92 (AvailableDigest), 107 (LastPolledAt `omitzero`), 117 (Notes). |
| `internal/state/notes.go` | Canonical `NotePinnedOptOut`, `NoteTagMismatch`, `NoteInvalidTagPattern` (WR-10 exported single source of truth) | VERIFIED | All five exported consts present at lines 45-49. |
| `cmd/hmi-update/main.go` | newRedactingHandler with regex + substring + base64 (CR-03); registry.NewResolver/Transport wiring; poll.RunUpdater goroutine BEFORE producers | VERIFIED | newRedactingHandler at line 90 with the three-branch redactor (regex line 99, substring line 102, base64 line 113-116). RunUpdater spawned at line 219 BEFORE poller.NewPoller at 251. registry.authn attestation event at line 200. |
| `cmd/hmi-update/main_test.go` | 5 unit tests for newRedactingHandler | VERIFIED | Tests pass under -race (`go test ./cmd/hmi-update/... -count=1 -race` PASS). |
| `e2e/tests/detect-multiarch.spec.ts` | Both single-arch + multi-arch index flip tests | VERIFIED | Two `test(...)` blocks; cron+5s SLA enforced. |
| `e2e/tests/detect-tag-pattern.spec.ts` | Non-matching no-flip + matching flip + invalid regex Note | VERIFIED | Three `test(...)` blocks. |
| `e2e/tests/detect-pinned.spec.ts` | Pinned appears + pinned never flips | VERIFIED | Two `test(...)` blocks. |
| `e2e/tests/obs-04-redaction.spec.ts` | Zero Bearer/Authorization/Basic Og== + affirmative registry.authn event | VERIFIED | Full poll-sweep capture + 3 negative + 1 affirmative assertions. |
| `e2e/fixtures/push-index.ts` | pushFreshIndex returns AMD64 child digest | VERIFIED | crane append --oci-empty-base + mutate --set-platform + index append + digest --platform. |
| `e2e/compose.test.override.cron-fast.yml` | HMI_UPDATE_CRON=@every 5s | VERIFIED | File present. |
| `Makefile e2e-cron-fast` target | Layers cron-fast override + busybox pre-seed | VERIFIED | Target at Makefile:98. |
| `SMOKE.md` | Phase 03 closure entry with cron-fast outcome | VERIFIED | Entry at lines 22-69. |

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|----|--------|---------|
| `cmd/hmi-update/main.go` | `internal/registry.NewResolver` | direct import + call | WIRED | `resolver := registry.NewResolver(transport)` line 194. |
| `cmd/hmi-update/main.go` | `internal/poll.NewPoller` | direct import + call | WIRED | `poller, err := poll.NewPoller(cronExpr, resolver, patterns, store, updates)` line 251. |
| `cmd/hmi-update/main.go` | `internal/poll.RunUpdater` | goroutine spawn | WIRED | `go poll.RunUpdater(ctx, updates, store)` line 219 — spawned BEFORE both producers (boot-order invariant). |
| `cmd/hmi-update/main.go` | slog default handler | `slog.SetDefault(slog.New(newRedactingHandler(...)))` | WIRED | line 142. |
| `internal/poll/poller.go` (producer A: cron sweep) | `internal/poll/channel.go` (consumer) | `p.updates <- u` via `p.send(ctx, u)` | WIRED | ctx-aware send; cap-64 buffer; lock never held across `resolver.Digest` call (DETECT-10). |
| `internal/docker/discovery.go` (producer B: events) | same channel | `StateUpdate` send | WIRED | Channel-send refactor of the Phase 2 direct-Update site was completed in Plan 03-04. |
| `internal/poll/poller.go` | `internal/registry.Resolver.Digest` | `p.resolver.Digest(callCtx, ref)` | WIRED | Per-call context.WithTimeout at line 280; SIGTERM unblocks sweep. |
| `internal/registry/resolver.go` | `crane.Digest(...)` | `WithAuth(authn.Anonymous)` + `WithPlatform(amd64Platform)` + `WithContext(ctx)` + `WithTransport(redactingTransport)` | WIRED | Lines 208-226 — all four Pitfall-1/Pitfall-2 prevention options applied; production path. |
| `internal/api/types.go` ↔ `ui/src/lib/types.d.ts` | tygo regen | Container fields available_digest/last_polled_at/notes mirrored | WIRED | TS types contain `available_digest?: string`, `last_polled_at?: string`, `notes?: string`, `pinned?: boolean`. |

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|---------------|--------|--------------------|--------|
| `/api/state` Container.AvailableDigest | State.Containers[svc].AvailableDigest | cron sweep → resolver.Digest → handleFetchResult → StateUpdate.Apply → state.Store.Update | YES — crane.Digest returns sha256 from real registry (zot in e2e, ghcr.io in production) | FLOWING |
| `/api/state` Container.Notes | State.Containers[svc].Notes | (a) discovery.go on container events; (b) poller.sendPinnedNote / sendTagMismatch / sendFetchError / clearStaleErrorNotes | YES — every assignment site goes through state.Store.Update with deterministic strings | FLOWING |
| `/api/state` State.LastPollStart/End | KindPollSweepStart/End StateUpdate at the top and bottom of sweep | `time.Now()` captured at sweepStart/sweepEnd | YES | FLOWING |
| `/api/state` Container.UpdateAvailable | handleFetchResult flip rule (two-rule: CurrentDigest vs resolved, OR priorAvailable vs resolved) | resolver.Digest result + prior state | YES — verified by detect-multiarch + detect-tag-pattern positive tests asserting flip semantics | FLOWING |

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| Go build succeeds | `go build ./...` | exit 0 | PASS |
| All Phase 3 packages pass unit tests under -race | `go test ./internal/registry/ ./internal/poll/ ./cmd/hmi-update/ -count=1 -race` | `ok internal/registry 5.826s`, `ok internal/poll 3.034s`, `ok cmd/hmi-update 1.626s` | PASS |
| `authn.DefaultKeychain` absent from production registry code (Pitfall 2 dam) | `grep -rF 'DefaultKeychain' internal/registry/*.go` (excluding `_sdk_shape.txt`) | zero matches in `.go` files; only documented in `_sdk_shape.txt` as FORBIDDEN | PASS |
| `errgroup.SetLimit` BEFORE `g.Go` (Phase-3 ordering pitfall) | `grep -n -E "WithContext\|SetLimit\|g\.Go" internal/poll/poller.go` | line 267 `errgroup.WithContext`, line 268 `g.SetLimit(p.concurrency)`, line 279 first `g.Go(...)` — correct order | PASS |
| ctx-aware send (CR-01) | `grep -A 4 "func.*send\(ctx" internal/poll/poller.go` | `case p.updates <- u:` / `case <-ctx.Done():` at lines 504-508 | PASS |
| Local cronInst (CR-02) | inspect Run() body | `c := cron.New(...)` local at lines 220-223, scoped to Run frame, no struct field | PASS |
| Base64 redaction (CR-03) | inspect newRedactingHandler | base64.StdEncoding.DecodeString probe at lines 113-116; redacts if decoded value contains `:` (RFC 7617 shape) | PASS |
| `store.Update` never wraps registry/docker I/O | `grep -rnE "store\.Update\(.*resolver\|store\.Update\(.*Inspect" internal/` | zero matches | PASS |
| TS types regenerated from Go schema | inspect `ui/src/lib/types.d.ts` | `available_digest`, `last_polled_at`, `notes`, `pinned` all present | PASS |
| Channel single-consumer + drain-on-ctx-cancel | inspect `runUpdater` body | `<-ctx.Done()` branch drains pending `case msg := <-ch` until default; one consumer goroutine | PASS |

### Requirements Coverage

| Requirement | Description | Status | Evidence |
|-------------|-------------|--------|----------|
| DETECT-01 | `internal/registry` uses `crane.Digest()` with linux/amd64 platform filter | SATISFIED | resolver.go:212 `crane.WithPlatform(amd64Platform)`; resolver.go:127 `amd64Platform = &v1.Platform{OS: "linux", Architecture: "amd64"}` |
| DETECT-02 | `Docker-Content-Digest` header is the digest source (never re-hash body) | SATISFIED | Implicit via `crane.Digest` SDK contract (see resolver.go doc comment block reproducing go doc output for crane.Digest); transport_test.go covers the header extraction path |
| DETECT-03 | Bearer-token flow does not send `Authorization: Basic Og==` | SATISFIED | resolver.go:211 `crane.WithAuth(authn.Anonymous)` (NEVER DefaultKeychain); transport_test.go `TestAnonymousFlow_NoBasicHeader` regression guard; SMOKE.md zero matches across full poll sweep |
| DETECT-04 | Both OCI image index AND direct single-arch manifest resolve to the same digest | SATISFIED | detect-multiarch.spec.ts two-test pair; pushFreshIndex returns the AMD64 child digest; resolver's WithPlatform(amd64) resolves index → child |
| DETECT-05 | Cron poller using `robfig/cron/v3` ticks on `HMI_UPDATE_CRON` | SATISFIED | `internal/poll/poller.go` Run() uses `cron.New(cron.WithLocation(time.UTC))` + AddFunc; HMI_UPDATE_CRON env var consumed at main.go:251 area |
| DETECT-06 | Docker event subscription detects new containers within 5s | SATISFIED | Phase 2's Discoverer goroutine now sends StateUpdate via the Phase 3 channel; detect-pinned waits up to 75s for boot-list SLA |
| DETECT-07 | Fresh manifest push flips `update_available` within `cron + 5 s` | SATISFIED | detect-multiarch.spec.ts test 1 + test 2 enforce 10s flip SLA (cron@every 5s + 5s slack) |
| DETECT-08 | Tag-pattern constraint via `hmi-update.tag-pattern=<regex>` label | SATISFIED | detect-tag-pattern.spec.ts three tests cover matching + non-matching + invalid-regex; `internal/poll/patterns.go` + `cronPoller.refForContainer` filter |
| DETECT-09 | Digest-pinned containers excluded from watch list with note | SATISFIED | detect-pinned.spec.ts two tests; `eligibleContainers` skips Pinned; `sendPinnedNote` writes canonical `state.NotePinnedOptOut` |
| DETECT-10 | Cron + event producers feed single-consumer channel; no lock held across I/O | SATISFIED | `internal/poll/channel.go` RunUpdater is the sole consumer; producers compute I/O results first then send pure-map-mutation closures; spot-check grep confirms no I/O inside store.Update |
| OBS-04 | Bearer-token redaction: no tokens/credentials/Authorization headers in slog output | SATISFIED | Two-layer defense: redactingTransport (request-side) + newRedactingHandler ReplaceAttr (output-side); obs-04-redaction.spec.ts asserts zero matches across full poll sweep + affirmative registry.authn boot event |

All 11 Phase 3 requirements satisfied.

### Anti-Patterns Found

None blocker-class. The 03-REVIEW.md cycle already absorbed CR-01..CR-03 (blockers) and WR-01..WR-10 (warnings) with verified fix evidence in the codebase. WR-11 is documented as accepted brittleness (stdlib slog duration encoding test); IN-01..IN-08 are info-class and deferred.

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| (none flagged this verification pass) | — | — | — | — |

### Human Verification Required

| Test | Expected | Why human |
|------|----------|-----------|
| Optional: re-run `make e2e-cron-fast` on this checkout to reproduce the green 8/8 attestation that SMOKE.md records | All four Plan 03-05 specs PASS (detect-multiarch, detect-tag-pattern, detect-pinned, obs-04-redaction); 8/8 tests in ~46s | Verifier ran static code/test checks and the Go unit-test suite under -race (PASS); a live docker-compose e2e takes ~46s of operator wall-clock and is intentionally outside the verifier's autonomous scope under the C4 verify→implement→verify→implement discipline. Phase 8 will fold this into automated CI. Optional because SMOKE.md already documents the green outcome dated 2026-05-14 and the unit-test suite + invariant greps independently corroborate the production code path. |

### Gaps Summary

No gaps. All five ROADMAP success criteria verified via codebase evidence + unit tests + e2e spec inspection + SMOKE.md attestation. One sub-clause of SC#5 (a live `crane.Digest()` call against a real `ghcr.io/centroid-is/*` image) is explicitly deferred to Phase 8 SC#3 per ROADMAP language — this is a planned routing, not a gap.

The Pitfall 2 part of SC#5 (no `Authorization: Basic Og==` on the wire, no DefaultKeychain in production code, anonymous bearer flow works against an OCI v2 registry that issues a bearer challenge) is fully verified in Phase 3 via (a) `internal/registry/transport_test.go::TestAnonymousFlow_NoBasicHeader` regression guard, (b) `e2e/tests/obs-04-redaction.spec.ts` zero-match assertion across a full poll sweep, and (c) the documented SMOKE.md cron-fast attestation against the in-cluster zot (the GHCR analog the Phase 3 development cycle targeted).

The remaining live-network half of SC#5 — "one local `crane.Digest()` call against real ghcr.io succeeds 200" — was placed in Phase 8 by the ROADMAP authors at the original phase-design stage with an explicit cross-reference note ("this smoke targets a Phase 3 concern but lives in the CI surface, hence its placement here"). That routing is preserved here.

---

## VERIFICATION COMPLETE

_Verified: 2026-05-14_
_Verifier: Claude (gsd-verifier)_
