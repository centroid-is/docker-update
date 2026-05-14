---
phase: quick
plan: 260514-l7h
subsystem: e2e-harness
tags: [d-02-01, e2e, docker-socket, eacces, macos, makefile, compose]
requires:
  - "internal/api/handlers.go healthz EACCES branch (Phase 02-04, unchanged)"
  - "e2e/compose.test.override.eacces.yml literal user pin (Phase 02-05, comment-only addendum here)"
provides:
  - "macOS Docker Desktop unblock for `make e2e` base stack"
  - "HMI_DOCKER_GID env var contract between Makefile recipe and e2e/compose.test.yml"
  - "Linux + macOS dual-platform e2e harness with deterministic eacces override behaviour"
affects:
  - "Phase 03 plan 03-05 Task 0 (resumes after this fix lands)"
tech-stack:
  added: []
  patterns:
    - "Recipe-time docker-socket GID detection via ephemeral alpine container (cross-platform)"
    - "`export` + chained-shell continuation so HMI_DOCKER_GID survives from detection to playwright child process"
key-files:
  created: []
  modified:
    - e2e/compose.test.yml
    - Makefile
    - e2e/compose.test.override.eacces.yml
decisions:
  - "Detect HMI_DOCKER_GID inside an ephemeral alpine container (`docker run --rm --entrypoint stat alpine -c %g /var/run/docker.sock`) rather than host-side `stat`. On macOS Docker Desktop the host-side GID (1/20/etc.) does not match the in-VM socket owner (root:0). The ephemeral-container probe returns the in-container view, which is what the hmi-update container will actually see."
  - "Use shell `export` + recipe line continuation so the var propagates from the Makefile through to playwright's globalSetup `docker compose up`. Without this the playwright child re-runs `up` without HMI_DOCKER_GID, compose detects env drift, recreates the hmi-update container with default 65532, and /healthz returns 503."
metrics:
  duration: "~25min"
  completed: "2026-05-14"
  tasks: 2
  files_modified: 3
---

# Quick 260514-l7h: Fix D-02-01 (macOS docker.sock EACCES in e2e base stack) Summary

D-02-01 closed. The base e2e stack now grants the host docker-socket GID to the hmi-update container at compose-up time via HMI_DOCKER_GID, and the eacces negative spec still produces /healthz=503 because its literal `user: "65532:65532"` pin overrides the env-var interpolation via compose v2 scalar-merge.

## Files Modified (3)

### 1. `e2e/compose.test.yml`

Added `user: "65532:${HMI_DOCKER_GID:-65532}"` to the `hmi-update` service block, plus an explanatory comment block. Diff:

```diff
   hmi-update:
     build:
       context: ..
       dockerfile: Dockerfile
     ports:
       - "8080:8080"
+    # macOS Docker Desktop + Linux dual support. The container's effective
+    # UID stays at 65532 (distroless nonroot reserved). The GID is overridden
+    # at compose-up time from HMI_DOCKER_GID, which the Makefile derives from
+    # `stat`-ing /var/run/docker.sock on the host (see Makefile `e2e` target).
+    # Fallback to 65532 keeps explicit failure visible when the env var is
+    # unset: /healthz will return 503 EACCES with the Pitfall 9 hint, the
+    # same way the compose.test.override.eacces.yml override does
+    # deliberately. Override files that pin a literal user: (e.g.
+    # compose.test.override.eacces.yml's "65532:65532") bypass this base
+    # entry — compose v2 scalar-merge replaces the whole string.
+    user: "65532:${HMI_DOCKER_GID:-65532}"
     volumes:
       - /var/run/docker.sock:/var/run/docker.sock
       - ./compose.test.yml:/host/docker-compose.yml:ro
```

### 2. `Makefile`

`e2e` and `e2e-debug` targets now detect HMI_DOCKER_GID via an ephemeral alpine container and export it for the rest of the recipe. Diff (e2e target shown; `e2e-debug` mirrors the same pattern with the override file + `--build` flag):

```diff
+# HMI_DOCKER_GID is detected at recipe-execution time (not parse time) so a
+# developer who starts Docker Desktop AFTER cloning the repo still gets the
+# right GID. Detection runs INSIDE an ephemeral alpine container (not on
+# the host) because the host-side GID is not the GID a container actually
+# sees on the bind-mounted socket:
+#   - macOS Docker Desktop: host shows GID 1/20/etc. (HFS forwarder UID),
+#     but inside the LinuxKit VM the socket is owned by root:root (GID 0).
+#     A host-side `stat` returns the wrong number.
+#   - Linux: the docker.sock inside any container is owned by the host
+#     docker group GID, which is what we want.
+# Running `stat -c %g /var/run/docker.sock` inside `alpine` produces the
+# correct in-container GID on both platforms. If docker isn't usable at
+# all the var stays unset and the compose default of 65532 surfaces as a
+# deterministic EACCES with the Pitfall 9 remediation hint.
 e2e:
 	cd e2e && npm ci && npx playwright install --with-deps chromium
-	docker compose -f e2e/compose.test.yml up -d --wait
-	cd e2e && npx playwright test ; STATUS=$$? ; \
-	  docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
+	export HMI_DOCKER_GID=$$(docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock 2>/dev/null) ; \
+	  echo "[make e2e] HMI_DOCKER_GID=$${HMI_DOCKER_GID:-<unset; container will hit EACCES>}" ; \
+	  docker compose -f e2e/compose.test.yml up -d --wait ; \
+	  cd e2e && npx playwright test ; STATUS=$$? ; \
+	  cd .. && docker compose -f e2e/compose.test.yml down -v --remove-orphans ; \
 	  exit $$STATUS
```

The whole sequence (detect → up → test → down → exit) is one continuation-joined shell line so the exported var propagates to playwright's `globalSetup` child process. Without this, playwright's `execSync('docker compose ... up -d --wait')` would see HMI_DOCKER_GID unset, compose would recreate the container with default 65532, and /healthz would 503.

### 3. `e2e/compose.test.override.eacces.yml`

Comment-only edit. Appended a POST-D-02-01 NOTE paragraph documenting how the override's literal pin survives the base's new env-var interpolation:

```diff
   stat (mode 0o660, owner root:docker) but connect() refuses because the
   container user is not in the docker group.

+#   POST-D-02-01 NOTE (Task 260514-l7h)
+#   The base compose.test.yml now declares
+#     user: "65532:${HMI_DOCKER_GID:-65532}"
+#   so on a developer machine with HMI_DOCKER_GID=998 (Linux) or
+#   HMI_DOCKER_GID=20 (macOS Docker Desktop staff group) the base
+#   container WOULD have docker-socket access. This override deliberately
+#   IGNORES HMI_DOCKER_GID by pinning a LITERAL "65532:65532" — compose v2
+#   scalar-merge replaces the entire `user:` string, so the env-var
+#   interpolation in the base is never expanded for this stack
+#   composition. That is the intended invariant: the eacces spec must
+#   produce EACCES on every host, regardless of HMI_DOCKER_GID.
+#
 # CAVEAT
```

The `user: "65532:65532"` line and all other content unchanged.

## Commits

| Task | Hash      | Subject                                                                       |
| ---- | --------- | ----------------------------------------------------------------------------- |
| 1    | `bbe61f5` | fix(quick-260514-l7h): wire HMI_DOCKER_GID into base e2e stack                |
| 2    | `13583ba` | docs(quick-260514-l7h): document eacces override invariant vs new HMI_DOCKER_GID |

## Verification Results

### Positive path (Task 1)

- **macOS Docker Desktop (this executor's host)**: `make e2e` ran. The detection echoed `[make e2e] HMI_DOCKER_GID=0` (the in-VM docker.sock owner is `root:root`, GID 0). globalSetup's `/healthz` poll **returned 200**, confirming the EACCES blocker is closed.
- **Linux**: Not directly executed in this session — falls to CI. The detection pattern is robust on Linux because `stat -c %g /var/run/docker.sock` inside an alpine container returns the host docker group GID, which IS the GID containers see on the bind-mounted socket. No special-casing needed.
- **`docker compose -f e2e/compose.test.yml config`** parses cleanly with the var both set and unset (`${HMI_DOCKER_GID:-65532}` syntax is well-formed compose v2).

### Negative path (Task 2)

Direct verification on this host (macOS Docker Desktop) with `HMI_DOCKER_GID=0` exported AND the eacces override stacked:

```text
HTTP status: 503
Body: {"status":"unhealthy","reason":"docker socket permission denied — set compose user: '65532:$(id -g docker)' (Pitfall 9)"}
PASS: hint contains 'id -g docker'
PASS: 503 status
```

The override's literal `user: "65532:65532"` pin overrides the base's env-var interpolation via compose v2 scalar-merge, so the eacces spec is deterministic on every host.

### Diagnostic visibility

The `[make e2e] HMI_DOCKER_GID=…` echo line surfaces the detected GID in CI logs for future debuggability, as required by the plan's done criteria.

## Deviations from Plan

### Rule 1 (Bug) — Plan's host-side stat returned wrong GID on macOS

- **Found during:** Task 1 verify, after first `make e2e` run
- **Issue:** The plan specified host-side detection via `stat -c '%g' /var/run/docker.sock 2>/dev/null || stat -f '%g' /var/run/docker.sock 2>/dev/null`. On macOS Docker Desktop the host-side BSD-stat returned GID `1` (the HFS forwarder UID), but inside the LinuxKit VM the docker.sock is owned by `root:root` (GID 0). The container started with secondary GID 1 (`bin`), which is NOT in the in-VM docker group, so /healthz still returned 503 EACCES.
- **Fix:** Replaced host-side stat with an ephemeral-container probe: `docker run --rm -v /var/run/docker.sock:/var/run/docker.sock --entrypoint stat alpine -c '%g' /var/run/docker.sock 2>/dev/null`. This returns the in-container view of the socket GID on both platforms:
  - macOS Docker Desktop: GID 0 (root in the LinuxKit VM)
  - Linux: the host docker group GID (e.g. 998, 999, …)
- **Why this is a Rule 1 fix and not Rule 4:** The plan's stated truth — "On macOS Docker Desktop, `make e2e` brings the base stack up and `/healthz` returns 200" — is unachievable with host-side stat. The fix is the same architectural approach (read the docker.sock GID, set it as HMI_DOCKER_GID) with a corrected mechanism. No new layer, no new tool, no new dependency. The plan's grep-regex verify clauses still pass for `stat -c '%g'`/`stat -f '%g'` patterns — they now match the alpine entrypoint args; the BSD `stat -f` clause is the only one that no longer matches and that's because BSD stat is no longer needed.
- **Files modified:** `Makefile` (both `e2e` and `e2e-debug` targets)
- **Commit:** `bbe61f5`

### Rule 3 (Blocker) — HMI_DOCKER_GID needed to propagate to playwright child process

- **Found during:** Task 1 verify, after second `make e2e` run (post-Rule-1 fix)
- **Issue:** Even with the in-container detection returning GID 0, `make e2e` still failed on globalSetup's /healthz=200 wait. Diagnosis: globalSetup itself calls `docker compose up -d --wait` from playwright's `execSync`. The Makefile's recipe assigned `HMI_DOCKER_GID` as a shell-local variable (no `export`), so playwright's child process did not see it. Compose detected the env-var drift, recreated the hmi-update container with the unset-default value 65532, and /healthz reverted to 503.
- **Fix:** Changed the recipe pattern to `export HMI_DOCKER_GID=…` + chained the whole pipeline (`up; playwright test; down; exit`) into one continuation-joined shell line. The exported var now propagates to playwright's child process, so compose's second `up` is a no-op against already-correct containers.
- **Files modified:** `Makefile` (same edits as Rule 1 fix; both deviations landed in one commit)
- **Commit:** `bbe61f5`

### Deferred (out of scope)

Two test-failure classes appeared after globalSetup unblocked, but are NOT part of D-02-01 and are NOT caused by the changes in this quick task:

1. **9 Phase 3 RED specs** (`detect-multiarch`, `detect-pinned`, `detect-tag-pattern`, `discovery`, `obs-04-redaction`) — these are intentionally red as of commit `8d68ec2 test(03-05): RED-FIRST`. They're the target of plan 03-05's GREEN phase, which is the next step the autonomous runner will pick up.
2. **1 smoke spec assertion** (`smoke.spec.ts:71`, "empty state row should use colspan='7'") — this is a UI-shell empty-state assertion that was previously masked by globalSetup failing earlier in the pipeline. The colspan-check is unrelated to docker-socket permissions or this quick task's changes. Phase 1 plan 04 owns the UI shell; revisit there or in a follow-on quick task. Documented here so it isn't silently lost.

## Authentication Gates

None encountered.

## Decisions Made

1. **In-container docker.sock GID probe over host-side stat.** The cross-platform robustness requirement (macOS Docker Desktop's LinuxKit VM forwarding) made host-side `stat` insufficient. Running `stat` inside an ephemeral alpine container costs ~300ms of recipe time but produces the GID the hmi-update container will actually see. This pattern generalises to any future test seam that needs the in-container view of a host-mounted resource.

2. **Single shell continuation + `export` for HMI_DOCKER_GID propagation.** Compose's env-var diff detection silently recreates containers when the env changes between `up` invocations. Exporting the var once at the top of the recipe and chaining the rest of the pipeline into the same shell line eliminates the recreate-and-lose-the-fix race that would otherwise bite anyone who runs `docker compose up` twice in a session.

## STATE.md Decision Line

`D-02-01 closed — base compose grants in-container docker.sock GID via HMI_DOCKER_GID (Makefile-derived via ephemeral alpine stat, exported across recipe), eacces override pins literal 65532:65532 to bypass`

## Known Stubs

None.

## Self-Check: PASSED

- `e2e/compose.test.yml`: FOUND, contains `user: "65532:${HMI_DOCKER_GID:-65532}"`
- `Makefile`: FOUND, contains `HMI_DOCKER_GID` detection on `e2e` and `e2e-debug` targets
- `e2e/compose.test.override.eacces.yml`: FOUND, contains `POST-D-02-01 NOTE` paragraph and unchanged literal `user: "65532:65532"`
- Commit `bbe61f5`: FOUND in `git log --all`
- Commit `13583ba`: FOUND in `git log --all`
- `/healthz` returned 200 with HMI_DOCKER_GID exported during the live `make e2e` run on macOS Docker Desktop (verified)
- `/healthz` returned 503 with Pitfall-9 hint when eacces override stacked + HMI_DOCKER_GID exported (verified)
