---
phase: 02-docker-client-compose-file-reader
plan: 04
subsystem: infra
tags: [go, healthz, http, build-tag, wiring, main, dock-03, obs-02, security]

# Dependency graph
requires:
  - phase: 02-docker-client-compose-file-reader
    plan: 01
    provides: docker.Client interface (Ping/ContainerList/ContainerInspect/Events/ImagePull/ImageTag) + 9 SDK type aliases; state.Container schema expansion
  - phase: 02-docker-client-compose-file-reader
    plan: 02
    provides: compose.Reader + ErrComposeFileMoved sentinel; stat-based drift detection contract
  - phase: 02-docker-client-compose-file-reader
    plan: 03
    provides: docker.NewDiscoverer constructor + Discoverer.Run goroutine

provides:
  - api.NewServer 3-arg signature (store *state.Store, dockerClient docker.Client, composeReader *compose.Reader)
  - 5-branch /healthz handler with VERBATIM remediation-hint response bodies (1 healthy + 4 unhealthy)
  - HMI_UPDATE_DOCKER_HOST env var test seam for socket-path injection
  - Build-tag-gated GET /debug/compose-stat handler ('//go:build debug') — plan 02-05 e2e seam
  - debug_compose_noop.go + debug_compose.go mutually-exclusive build-tag pair (//go:build !debug vs //go:build debug)
  - cmd/hmi-update/main.go full Phase-2 boot wiring: slog -> state.NewStore -> docker.NewClient -> compose.NewReader -> Discoverer goroutine -> api.NewServer.ListenAndServe
  - 7-case table-driven TestHealthzScenarios (healthy, socket-missing, socket-eacces-on-stat, ping-eacces, ping-other, nil-docker-client (W2), ping-timeout)
  - newTestReader helper for test ergonomics across the api package

affects: [02-05-e2e-watched-container-spec, 03-poller (consumer of docker.Client + state.Store), 04-actions (consumer of compose.Reader + the action-endpoint surface)]

# Tech tracking
tech-stack:
  added: []  # no new go.mod entries — all stdlib (context, encoding/json, errors, io/fs, log/slog, net/http, os, strings, syscall, time)
  patterns:
    - "VERBATIM response-body constants: 5 named constants (healthzBodyOK/StateUnwired/SocketMissing/SocketEACCES/DaemonUnreach) declared once at package scope. Any new string requires a threat-model review (T-02-04-01). No interpolation, no dynamic fields."
    - "Build-tag mutually-exclusive pair: debug_compose.go ('//go:build debug') + debug_compose_noop.go ('//go:build !debug') for the registerDebugRoutes method. The // !debug constraint on the noop is REQUIRED for the debug build to compile (without it, both files declare the method, the compiler fails with 'method already declared'). This deviates from the plan's stated done criteria but is a Rule 1 correctness fix."
    - "Defensive nil-guard + W2 test: the nil-docker-client branch in healthz routes to the 'socket missing' message; the W2 test in handlers_healthz_test.go covers the branch explicitly so coverage tooling does not flag it as dead code."
    - "Ping with 500ms ctx hard ceiling: handler responds in <2s even when daemon is wedged. Belt-and-braces EACCES detection via both errors.Is(syscall.EACCES) AND a strings.Contains 'permission denied' fallback (SDK error shapes are not always typed — explicit in CONTEXT.md)."
    - "Two-step TDD execution split: Task 1 (server.go + handlers.go + tests + debug_compose_noop.go stub) lands the contract + RED + GREEN; Task 2 (debug_compose.go + main.go) lands the build-tag-gated route + boot wiring. Task 1 leaves the codebase building at the cost of one main.go placeholder (nil, nil to NewServer) that Task 2 replaces."

key-files:
  created:
    - internal/api/handlers_healthz_test.go (270 lines — fakeClient + 7 table cases + path-leak guard + JSON parse sanity)
    - internal/api/debug_compose_noop.go (19 lines — //go:build !debug; no-op registerDebugRoutes with full doc comment)
    - internal/api/debug_compose.go (66 lines — //go:build debug; registerDebugRoutes wires GET /debug/compose-stat + debugComposeStat handler)
  modified:
    - internal/api/server.go (constructor signature: +2 args; struct: +2 fields; routes() unchanged; +1 call site for registerDebugRoutes)
    - internal/api/handlers.go (healthz rewritten with 4-branch detection flow; 5 verbatim body constants; dockerSocketPath helper)
    - internal/api/server_test.go (helpers updated for 3-arg NewServer; newTestReader helper added; TestHealthz sets HMI_UPDATE_DOCKER_HOST)
    - cmd/hmi-update/main.go (full Task 2 boot order: docker.NewClient + compose.NewReader + Discoverer goroutine; +3 imports)

key-decisions:
  - "//go:build !debug constraint on debug_compose_noop.go — REQUIRED for the debug build to compile. The plan's stated done criterion 'noop has NO build tag' would have produced a 'method registerDebugRoutes already declared' compile error under 'go build -tags=debug ./...'. Verified empirically (deviation Rule 1)."
  - "Verbatim healthz body strings as named constants — 5 named consts at package scope, no interpolation. Any future branch requires a threat-model review per T-02-04-01."
  - "Task 1 introduces a temporary nil/nil placeholder in main.go's api.NewServer call so 'go build ./...' stays green between Task 1 and Task 2 (Rule 3 — blocking issue). Task 2 replaces the placeholder with the full wiring."
  - "HMI_UPDATE_DOCKER_HOST env-var test seam — handlers.go's dockerSocketPath() consults this env first, falls back to /var/run/docker.sock. The test seam matches Phase 1's HMI_UPDATE_STATE_PATH convention and CONTEXT.md's 'overridable for tests' guidance."
  - "ctx = context.Background() in main.go — graceful SIGTERM shutdown is Phase 4 (STATE-04); the named-var seam keeps Phase 4's patch to one line (a context.WithCancel + a SIGTERM handler)."
  - "Defensive nil-docker-client routing to 'socket missing' message — not 'state store unavailable' — because the operator action (check the bind-mount, check the GID) is the same as the explicit socket-missing case. W2 test covers the branch directly so coverage tooling does not flag dead code."
  - "Ping 500ms timeout (not 1s) per CONTEXT.md Claude's Discretion — 'fails fast under wedge'. Phase 1's 10s http.Server ReadTimeout/WriteTimeout caps any per-connection cost above this."

patterns-established:
  - "Build-tag mutually-exclusive method pair: package method ships in two files, '//go:build debug' on one + '//go:build !debug' on the other. Same signature, different bodies. The default (production) build compiles the noop; the debug build compiles the real implementation. Production binaries pass `strings | grep <debug-route>` with zero matches."
  - "Env-var test seam for filesystem paths: production handlers consult an HMI_UPDATE_* env var first and fall back to a documented canonical path. Tests set the env var with t.Setenv (auto-cleanup, parallel-safe) and inject TempDir-rooted paths."
  - "VERBATIM-string response body constants for security-relevant payloads: 5 named const strings at package scope; any new string requires a documented threat-model review. No fmt.Sprintf, no interpolation. Path-leak guard enforced per-test."

requirements-completed: [DOCK-03, OBS-02]

# Metrics
duration: ~10min
completed: 2026-05-13
---

# Phase 02 Plan 04: Healthz Upgrade + Server Signature + main.go Wiring + Build-Tag-Gated Debug Endpoint Summary

**The /healthz handler now distinguishes 4 failure modes (each with a VERBATIM remediation-hint body) from 1 healthy result; the api.NewServer constructor extends to 3 args (store, docker.Client, *compose.Reader); cmd/hmi-update/main.go boots all four subsystems in CONTEXT.md order; and a //go:build debug-gated /debug/compose-stat route ships for plan 02-05's Playwright spec — production binaries contain zero "compose-stat" string matches.**

## Performance

- **Duration:** ~10 min (commit timestamps: 2a39e63 RED -> 8e27e12 Task 1 GREEN -> a203802 Task 2)
- **Started:** 2026-05-13T21:33:15Z (RED commit pre-write)
- **Completed:** 2026-05-13T21:40:39Z (Task 2 commit + SUMMARY composition)
- **Tasks:** 2 sequential tasks per the post-review revision (Task 1 TDD RED+GREEN + stub; Task 2 build-tag-gated debug endpoint + main.go boot wiring)
- **Files modified:** 4 modified + 3 created = 7 files total

## Accomplishments

- **DOCK-03 satisfied:** /healthz routes through 4 distinct failure-mode branches (state-nil -> client-nil-defensive -> socket-stat ENOENT/EACCES -> Ping 500ms ctx -> EACCES typed-or-string-match) and emits 5 VERBATIM response-body constants per CONTEXT.md "Healthz Remediation Hints". Each constant references the canonical operator action (Pitfall 9's `id -g docker` for EACCES; `/var/run/docker.sock` bind-mount path for socket-missing).
- **OBS-02 satisfied:** Every 503 carries a paste-ready remediation hint; the 200 healthy response is `{"status":"ok"}`. Content-Type and Cache-Control headers set on every branch. Response time bounded to <2s in the worst case (ping-timeout) by a 500ms context.WithTimeout on Ping.
- **Build-tag gating proven at the binary level:** `strings /tmp/hmi-update-prod | grep compose-stat` returns 0 matches; `strings /tmp/hmi-update-debug | grep compose-stat` returns 2 matches (the route literal + the slog event name). T-02-04-02 mitigation confirmed empirically.
- **Phase-2 boot wiring complete:** cmd/hmi-update/main.go threads slog -> state.NewStore -> docker.NewClient -> compose.NewReader -> Discoverer goroutine -> api.NewServer.ListenAndServe(":8080") in monotonically-increasing line order. Every constructor's fail-fast log.Fatalf names the failing subsystem.
- **Test coverage of every healthz branch:** 7 table-driven cases in TestHealthzScenarios (healthy, socket-missing, socket-eacces-on-stat, ping-eacces, ping-other, nil-docker-client (W2 defensive-branch coverage), ping-timeout) + 1 carry-over TestHealthzNilStore in server_test.go = 8 branches covered. Path-leak guard runs in every case for the test-host TempDir prefixes.
- **Zero regression:** Phase 1's TestHealthz, TestHealthzNilStore, TestGetStateEmpty, TestGetStateWithContainer, TestAssetsStrictNoFallback, TestIndexHTMLCacheControl, TestAssetsImmutable all pass under the new 3-arg constructor signature.

## Verbatim Response Bodies (per plan output requirement)

```
200 {"status":"ok"}
503 {"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}
503 {"status":"unhealthy","reason":"docker socket missing — add bind-mount '/var/run/docker.sock:/var/run/docker.sock'"}
503 {"status":"unhealthy","reason":"docker daemon unreachable"}
503 {"status":"unhealthy","reason":"state store unavailable; check HMI_UPDATE_STATE_PATH and restart"}
```

These 5 strings are declared as named constants in `internal/api/handlers.go` (`healthzBodyOK`, `healthzBodySocketEACCES`, `healthzBodySocketMissing`, `healthzBodyDaemonUnreach`, `healthzBodyStateUnwired`) and are byte-for-byte from CONTEXT.md "Healthz Remediation Hints". `grep -c` for each substring in handlers.go returns 1.

## Task Commits

1. **Task 1 RED: failing tests for upgraded /healthz + 3-arg NewServer** — `2a39e63` (test) — handlers_healthz_test.go (7-case table-driven) + server_test.go (newTestServer/newTestServerWithContainer updated to 3-arg NewServer + newTestReader helper). Build verified to fail with "too many arguments in call to NewServer" (have `*state.Store, docker.Client, *compose.Reader`; want `*state.Store`).
2. **Task 1 GREEN: upgrade /healthz with socket-stat + Ping flow + 3-arg NewServer** — `8e27e12` (feat) — server.go (3-arg constructor + 2 new fields + registerDebugRoutes call site) + handlers.go (5 constants + 4-branch detection flow + dockerSocketPath helper) + debug_compose_noop.go (one-line stub for Task 1's build) + cmd/hmi-update/main.go (Rule 3 nil/nil placeholder so `go build ./...` stays green). All 7 healthz scenarios pass; 8 total branches covered when TestHealthzNilStore is included.
3. **Task 2: build-tag-gated /debug/compose-stat + main.go boot wiring** — `a203802` (feat) — debug_compose.go (//go:build debug; registers GET /debug/compose-stat + debugComposeStat handler) + debug_compose_noop.go (augmented doc comment + //go:build !debug constraint per Rule 1 deviation) + main.go (full Phase-2 boot order with docker.NewClient + compose.NewReader + Discoverer goroutine).

No REFACTOR commit — the GREEN code is at the documented quality bar (doc comments, threat-model cross-refs, named constants, defensive guards explained inline).

## Files Created/Modified

**Created:**

- `internal/api/handlers_healthz_test.go` (270 lines) — fakeClient implementing docker.Client (Ping configurable; other methods stubbed); 7 table-driven cases; path-leak guard; JSON parse sanity; total-elapsed-time bound.
- `internal/api/debug_compose_noop.go` (19 lines, after Task 2 augmentation) — `//go:build !debug` (Rule 1 deviation); no-op `func (s *Server) registerDebugRoutes() {}`; full doc comment explaining the build-tag rationale and the T-02-04-02 mitigation.
- `internal/api/debug_compose.go` (66 lines) — `//go:build debug` at top; `registerDebugRoutes` calls `s.mux.HandleFunc("GET /debug/compose-stat", s.debugComposeStat)` and emits a slog event; `debugComposeStat` maps compose.ErrComposeFileMoved -> 412, nil -> 200, other errors -> 500.

**Modified:**

- `internal/api/server.go` — Server struct gains `dockerClient docker.Client` + `composeReader *compose.Reader`; NewServer signature is `(store *state.Store, dockerClient docker.Client, composeReader *compose.Reader) *Server`; routes() unchanged; NewServer now also calls `s.registerDebugRoutes()` after `s.routes()`.
- `internal/api/handlers.go` — healthz body rewritten with 4-branch detection flow; 5 named-constant body strings; dockerSocketPath helper reads HMI_UPDATE_DOCKER_HOST or falls back to /var/run/docker.sock; getState unchanged.
- `internal/api/server_test.go` — `newTestServer` + `newTestServerWithContainer` updated to 3-arg NewServer; `newTestReader` helper added (writes a stub compose file + returns *compose.Reader); `TestHealthz` sets `HMI_UPDATE_DOCKER_HOST` to a tmp-file path so the upgraded handler's stat step passes; `TestHealthzNilStore` carries over unchanged (state-nil branch is still the first guard).
- `cmd/hmi-update/main.go` — Full Phase-2 boot per CONTEXT.md "Lifecycle & Wiring": adds `context`, `internal/compose`, `internal/docker` imports; constructs ctx, dockerClient, composeReader, Discoverer; spawns `go discoverer.Run(ctx)`; calls `api.NewServer(store, dockerClient, composeReader)`.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] //go:build !debug constraint required on debug_compose_noop.go**

- **Found during:** Task 2 verification (debug build).
- **Issue:** The plan's done criteria stated "internal/api/debug_compose_noop.go has NO build tag" — implying both `debug_compose.go` (`//go:build debug`) and `debug_compose_noop.go` (no tag) should ship together. Without a complementary build tag on the noop, `go build -tags=debug ./...` fails with `method Server.registerDebugRoutes already declared at internal/api/debug_compose.go:30:18` because BOTH files compile under the debug tag, producing two definitions of the same method.
- **Fix:** Added `//go:build !debug` to debug_compose_noop.go (first line, blank line, then `package api`). This makes the two files mutually exclusive: default build compiles only the noop, debug build compiles only the real handler. Verified empirically by removing the tag and observing the duplicate-method compile error.
- **Files modified:** `internal/api/debug_compose_noop.go`
- **Commit:** `a203802`
- **Plan-gate side effect:** The plan's verify step `grep -L "//go:build" internal/api/debug_compose_noop.go` no longer lists the file (the file now DOES have a build tag). The substantive gate (`go build -tags=debug ./...` exits 0) PASSES, which is the gate that actually matters for correctness. The plan's grep gate would have green-lit a broken debug build.

**2. [Rule 3 - Blocking issue] Temporary nil/nil placeholder in main.go between Task 1 and Task 2**

- **Found during:** Task 1 build verification.
- **Issue:** Task 1's `<verify>` step requires `go build ./... && go vet ./... && go test ./internal/api/... -race` to all exit 0. Once Task 1 changes `api.NewServer`'s signature to 3 args, the existing `cmd/hmi-update/main.go` (Phase 1's 1-arg call) stops compiling — the whole `go build ./...` fails. Task 2 is the documented owner of main.go's full wiring, so Task 1 cannot land the full wiring; but it also cannot leave main.go in a non-building state.
- **Fix:** Task 1 updated main.go's `api.NewServer(store)` call to `api.NewServer(store, nil, nil)` with an inline comment explicitly stating "Task 2 rewrites this block". The upgraded /healthz handler's defensive nil-guards return 503 with documented hints for each nil dependency — fail-soft is the correct posture for a half-wired binary, and Task 2 immediately replaces the placeholder with the real wiring (docker.NewClient + compose.NewReader + Discoverer goroutine).
- **Files modified:** `cmd/hmi-update/main.go`
- **Commit:** `8e27e12` (Task 1 GREEN), superseded by `a203802` (Task 2).
- **Net result:** Final state of main.go (after Task 2) matches the plan's documented boot order exactly; the placeholder lived for one commit (~3 min).

None of these deviations changed the behavioural contract — all 8 healthz branches respond exactly as the plan and CONTEXT.md prescribe, the build-tag mutual exclusion works correctly under both `go build ./...` and `go build -tags=debug ./...`, and main.go's final boot order is monotonic per CONTEXT.md "Lifecycle & Wiring".

## EACCES-on-stat reliability (per plan `<output>` request)

**Worked reliably on this developer machine.** The test creates a sub-directory with mode 0o000 inside t.TempDir() and asks `os.Stat` for a file inside that directory; on macOS APFS (the developer's filesystem), the stat returns `fs.ErrPermission` reliably across all test runs. Cleanup uses `t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })` so the t.TempDir auto-removal still works.

Caveat acknowledged in the plan: "macOS occasionally permits stat through 0o000 dirs". Did NOT observe this on APFS during this plan's execution. If a future CI run on a different filesystem (e.g., a Linux ext4 runner with a specific mount option, or a Linux container running as root) exhibits the flake, the fix is to additionally chmod the test file (not just the parent dir) and assert via a more direct mechanism — e.g., a syscall-level `unix.Faccessat` instead of `os.Stat`. Not needed today.

## t.Setenv("HMI_UPDATE_DOCKER_HOST") and parallel test execution (per plan `<output>` request)

`t.Setenv` is the documented safe surface for test-local env-var injection: the Go testing package's contract is that `t.Setenv` automatically restores the prior value when the test/subtest completes, and a subtest that calls `t.Setenv` is automatically NOT run in parallel (the test framework refuses to call `t.Parallel()` after `t.Setenv` is invoked).

**Observed behaviour:** The 7 healthz table-driven subtests use `t.Setenv` per case; the Go test runner serializes them implicitly. The full TestHealthzScenarios runs in ~0.6s (worst case is the 0.51s ping-timeout subtest). No parallelism was attempted — the env-var injection makes parallel execution unsafe on principle, and the per-subtest cost is low enough that serial execution is not a bottleneck.

If a future plan wants to run healthz subtests in parallel (e.g., to cut a 10-case suite's time), the path forward is to inject the socket path via a constructor argument (e.g., make `dockerSocketPath` a Server field rather than a package-level env-var reader) — that would remove the env-var dependency entirely and unlock `t.Parallel()`. Not needed today.

## go test ./internal/api/... -race -v output (excerpt)

```
=== RUN   TestHealthzScenarios
=== RUN   TestHealthzScenarios/healthy
=== RUN   TestHealthzScenarios/socket-missing
=== RUN   TestHealthzScenarios/socket-eacces-on-stat
=== RUN   TestHealthzScenarios/ping-eacces
=== RUN   TestHealthzScenarios/ping-other
=== RUN   TestHealthzScenarios/nil-docker-client
=== RUN   TestHealthzScenarios/ping-timeout
--- PASS: TestHealthzScenarios (0.60s)
    --- PASS: TestHealthzScenarios/healthy (0.02s)
    --- PASS: TestHealthzScenarios/socket-missing (0.01s)
    --- PASS: TestHealthzScenarios/socket-eacces-on-stat (0.01s)
    --- PASS: TestHealthzScenarios/ping-eacces (0.01s)
    --- PASS: TestHealthzScenarios/ping-other (0.01s)
    --- PASS: TestHealthzScenarios/nil-docker-client (0.01s)
    --- PASS: TestHealthzScenarios/ping-timeout (0.51s)
=== RUN   TestHealthz
--- PASS: TestHealthz (0.02s)
=== RUN   TestHealthzNilStore
--- PASS: TestHealthzNilStore (0.00s)
=== RUN   TestGetStateEmpty
--- PASS: TestGetStateEmpty (0.01s)
=== RUN   TestGetStateWithContainer
--- PASS: TestGetStateWithContainer (0.03s)
=== RUN   TestAssetsStrictNoFallback
--- PASS: TestAssetsStrictNoFallback (0.03s)
=== RUN   TestIndexHTMLCacheControl
--- PASS: TestIndexHTMLCacheControl (0.01s)
=== RUN   TestAssetsImmutable
--- PASS: TestAssetsImmutable (0.01s)
PASS
ok  	github.com/centroid-is/hmi-update/internal/api	2.435s
```

13 PASS, 0 FAIL, 0 SKIP under `-race`. Phase 1 baseline tests carry over green.

## Build gate verification

```
$ go build ./...           # production build (no -tags)
   exit 0
$ go build -tags=debug ./...  # debug build
   exit 0
$ go vet ./...
   exit 0
$ go test ./... -race -count=1
   ok  internal/api      2.330s
   ok  internal/compose  1.884s
   ok  internal/docker   2.044s
   ok  internal/state   11.419s
$ go test -tags=debug ./... -race -count=1
   ok  internal/api      2.157s
   ok  internal/compose  1.240s
   ok  internal/docker   2.117s
   ok  internal/state   11.457s
```

```
$ strings /tmp/hmi-update-prod | grep compose-stat
   (no output — 0 matches)
$ strings /tmp/hmi-update-debug | grep compose-stat
   (2 matches: the route literal "/debug/compose-stat" + the slog event "debug.route.registered")
```

T-02-04-02 mitigation verified at the binary level: production binaries cannot serve /debug/compose-stat because the handler does not exist in the compiled code.

## Boot order in main.go (per Task 2 verify step)

```
$ grep -n -E 'state\.NewStore|docker\.NewClient|compose\.NewReader|NewDiscoverer|api\.NewServer' cmd/hmi-update/main.go
6:  // 2. state.NewStore (path via HMI_UPDATE_STATE_PATH, default ./hmi_update_state.json)
7:  // 3. docker.NewClient(ctx) — DOCK-01 (fail-fast on bad DOCKER_HOST)
8:  // 4. compose.NewReader(env) — DOCK-02 (fail-fast on missing
12: // 6. api.NewServer(store, dockerClient, composeReader).ListenAndServe(":8080")
53: // 2. state.NewStore (unchanged from Phase 1).
58: store, err := state.NewStore(statePath)
60: log.Fatalf("state.NewStore: %v", err)
63: // 3. docker.NewClient (DOCK-01). FromEnv honours DOCKER_HOST for tests
74: dockerClient, err := docker.NewClient(ctx)
76: log.Fatalf("docker.NewClient: %v", err)
79: // 4. compose.NewReader (DOCK-02). Plan 02-05's Task 0 wires
85: composeReader, err := compose.NewReader(composePath)
87: log.Fatalf("compose.NewReader: %v", err)
103: discoverer := docker.NewDiscoverer(dockerClient, store)
110: // 6. api.NewServer with the Phase 2 three-arg signature.
111: srv := api.NewServer(store, dockerClient, composeReader)
```

Actual call-site lines (filtering out doc-comment occurrences):
- state.NewStore       line 58
- docker.NewClient     line 74
- compose.NewReader    line 85
- docker.NewDiscoverer line 103
- api.NewServer        line 111

Monotonically increasing. Matches CONTEXT.md "Lifecycle & Wiring" exactly.

## Threat Model Coverage

| Threat ID | Status | Evidence |
|-----------|--------|----------|
| T-02-04-01 (Info disclosure — /healthz response body) | mitigated | 5 named-constant body strings declared at package scope in handlers.go; no fmt.Sprintf, no interpolation. Path-leak guard runs in every TestHealthzScenarios case for the 3 test-host TempDir prefixes ('/private/', '/var/folders/', '/tmp/'). |
| T-02-04-02 (Info disclosure — /debug/compose-stat in production) | mitigated | Empirical: `strings hmi-update-prod \| grep compose-stat` returns 0 matches; `strings hmi-update-debug \| grep compose-stat` returns 2 matches. The build-tag pair (`//go:build debug` + `//go:build !debug`) is mutually exclusive. Production Dockerfile in Phase 7 will NOT pass `-tags=debug`. |
| T-02-04-03 (Tampering — HMI_UPDATE_DOCKER_HOST) | accepted | Documented in CONTEXT.md and inline in handlers.go: env var is operator-controlled at deploy time; if an attacker can set env vars in the container's namespace, they already have full control. |
| T-02-04-04 (DoS — /healthz Ping timeout) | mitigated | 500ms context.WithTimeout on Ping; handler responds in <2s in the worst case (verified by TestHealthzScenarios/ping-timeout taking 0.51s elapsed). Phase 1's 10s http.Server ReadTimeout/WriteTimeout caps any per-connection cost above this. |
| T-02-04-05 (Repudiation — boot-time fail-fast) | mitigated | Each log.Fatalf in main.go names the failing constructor (state.NewStore, docker.NewClient, compose.NewReader); operator greps journalctl and immediately sees the failing subsystem. |

## Next Phase Readiness

**Ready for plan 02-05 (e2e Playwright specs + compose overrides + Dockerfile/Makefile debug-image):**
- The /debug/compose-stat seam is in place and proven to register only under `-tags=debug`.
- The verbatim /healthz response strings are the contract Playwright asserts against in `e2e/tests/healthz-negative.spec.ts`.
- main.go's boot wiring is complete; the e2e compose stack's `hmi-update` service just needs HMI_UPDATE_COMPOSE_PATH set (plan 02-05's Task 0).

**Ready for Phase 3 (registry poller):**
- docker.Client is consumed via dependency injection; Phase 3's poller takes the same `docker.Client` interface from main.go.
- The single-producer state-mutation pattern (Discoverer is the first producer) is stable; Phase 3's poller joins as the second producer through the same state.Store.Update surface.

**Ready for Phase 4 (mutating actions):**
- compose.Reader is wired into api.Server; Phase 4's update/rollback handlers call `s.composeReader.CheckUnchanged(ctx)` before `docker compose -f $HMI_UPDATE_COMPOSE_PATH up -d --force-recreate <svc>` and branch on `errors.Is(err, compose.ErrComposeFileMoved)` to serve the documented 412 body (which already exists at /debug/compose-stat).
- The /debug/compose-stat endpoint can be deleted in Phase 4 once the action endpoints exercise compose.Reader naturally — search for "debug_compose" when doing that work.

**No blockers or concerns introduced.**

## Self-Check: PASSED

Verified files exist:
- `internal/api/handlers_healthz_test.go` — FOUND (270 lines)
- `internal/api/debug_compose.go` — FOUND (66 lines, //go:build debug)
- `internal/api/debug_compose_noop.go` — FOUND (19 lines, //go:build !debug)
- `internal/api/server.go` — FOUND (modified, 3-arg NewServer)
- `internal/api/handlers.go` — FOUND (modified, 4-branch healthz + 5 verbatim constants)
- `internal/api/server_test.go` — FOUND (modified, newTestReader + 3-arg constructor)
- `cmd/hmi-update/main.go` — FOUND (modified, full Phase-2 boot order)

Verified commits exist (per `git log --oneline -5`):
- `2a39e63` — FOUND (test commit, RED phase)
- `8e27e12` — FOUND (feat commit, Task 1 GREEN)
- `a203802` — FOUND (feat commit, Task 2)

Verified gates pass:
- `go build ./...` — exit 0
- `go build -tags=debug ./...` — exit 0
- `go vet ./...` — exit 0
- `go test ./... -race -count=1` — exit 0 (api/compose/docker/state all pass)
- `go test -tags=debug ./... -race -count=1` — exit 0
- `grep -c "//go:build debug" internal/api/debug_compose.go` — 1
- `grep -c "docker.NewClient" cmd/hmi-update/main.go` — 4 (doc + inline + call site + log line; verified call site at line 74)
- `grep -c "compose.NewReader" cmd/hmi-update/main.go` — 4 (doc + inline + call site + log line; verified call site at line 85)
- `grep -c "NewDiscoverer" cmd/hmi-update/main.go` — 1 (call site at line 103)
- Boot constructor line numbers in main.go: 58 < 74 < 85 < 103 < 111 (monotonically increasing)
- All 4 verbatim healthz body substrings present in handlers.go (`grep -c` returns 1 each for "docker socket permission denied", "docker socket missing", "docker daemon unreachable", "state store unavailable")
- Production binary `strings | grep compose-stat` — 0 matches
- Debug binary `strings | grep compose-stat` — 2 matches

## TDD Gate Compliance

- RED commit: `2a39e63` — `test(02-04): add failing tests for upgraded /healthz + 3-arg NewServer` — build verified to fail with "too many arguments in call to NewServer (have *state.Store, docker.Client, *compose.Reader; want *state.Store)".
- GREEN commit (Task 1): `8e27e12` — `feat(02-04): upgrade /healthz with socket-stat + Ping flow + 3-arg NewServer` — drives all 13 internal/api tests to pass under -race.
- GREEN commit (Task 2): `a203802` — `feat(02-04): build-tag-gated /debug/compose-stat + full main.go boot wiring` — adds the build-tag pair + completes main.go wiring; default + debug builds both pass; cross-package tests stay green.
- REFACTOR commit: not present — GREEN code is at the documented quality bar (named verbatim-string constants, threat-model cross-refs, defensive guards explained inline, build-tag mutual-exclusion proven empirically at binary level). REFACTOR is optional per execute-plan.md and assessed unnecessary.

---
*Phase: 02-docker-client-compose-file-reader*
*Completed: 2026-05-13*
