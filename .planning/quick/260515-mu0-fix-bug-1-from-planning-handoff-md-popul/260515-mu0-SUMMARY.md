---
phase: quick-260515-mu0
plan: 01
type: execute
wave: 1
status: complete
requirements: [BUG-1, BUG-5, DETECT-FLIP, ACTION-PULL-NOOP]
metrics:
  duration_minutes: 8
  tasks: 3
  files_modified: 9
  net_lines: "+347 / -12"
  completed: 2026-05-15
commits:
  - 0421aff feat(docker): add ImageInspect to Client interface (BUG-1 prep)
  - 068d391 fix(docker): populate Container.CurrentDigest from RepoDigests[0] (BUG-1)
  - 37a9b84 fix(actions): drainPullStream falls back to Status-Digest for no-op pulls (BUG-5)
key-files:
  modified:
    - internal/docker/client.go: 7th method (ImageInspect) + type alias + doc comments
    - internal/docker/moby.go: mobyClient.ImageInspect impl + SDK-shape mirror
    - internal/docker/moby_test.go: TestClient_InterfaceMethodCount 6 -> 7
    - internal/docker/discovery.go: ImageInspect call + CurrentDigest in single Apply
    - internal/docker/discovery_test.go: scripted ImageInspect on fakeClient + BUG-1 regression test
    - internal/actions/orchestrator.go: drainPullStream Status-Digest fallback + strings import
    - internal/actions/orchestrator_test.go: ImageInspect stub on fakeDockerClient + 3 BUG-5 regression tests
    - internal/api/getstate_noio_test.go: ImageInspect stub on panickingDockerClient
    - internal/api/handlers_healthz_test.go: ImageInspect stub on fakeClient
---

# Quick 260515-mu0: Fix BUG-1 (CurrentDigest population) + BUG-5 (drainPullStream no-op-pull fallback) Summary

Two production bugs from the post-deploy diagnostic session (.planning/HANDOFF.md) fixed in one redeploy window. BUG-1 unblocks BUG-4 (update_available flip) automatically; BUG-5 restores Update-button functionality for already-up-to-date images.

## Diff Shape

| File | Lines added | Notes |
|------|-------------|-------|
| internal/docker/client.go | +30 / -5 | 7th interface method + ImageInspect type alias |
| internal/docker/moby.go | +22 / -0 | mobyClient.ImageInspect impl + SDK shape mirror |
| internal/docker/moby_test.go | +4 / -4 | Method-count guard 6 -> 7 |
| internal/docker/discovery.go | +69 / -3 | ImageInspect call + single-StateUpdate CurrentDigest |
| internal/docker/discovery_test.go | +110 / -0 | Scripted ImageInspect on fakeClient + TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests |
| internal/actions/orchestrator.go | +25 / -0 | drainPullStream Status-Digest fallback + "strings" import |
| internal/actions/orchestrator_test.go | +87 / -0 | ImageInspect stub on fakeDockerClient (Task 1) + 3 BUG-5 regression tests (Task 3) |
| internal/api/getstate_noio_test.go | +4 / -0 | ImageInspect panic stub (Rule 3) |
| internal/api/handlers_healthz_test.go | +4 / -0 | ImageInspect zero-value stub (Rule 3) |
| **Total** | **+347 / -12 across 9 files** | |

## Production Stream Sequences (BUG-5 regression triage reference)

The three new BUG-5 tests exercise the exact JSON-line shapes the daemon emits. Future regression triage can match these verbatim against `docker logs hmi-update` output.

### 1. `TestDrainPullStream_NoOpPull_DigestFromStatus`

Verbatim production stream observed at 2026-05-15 16:26:34 on the HMI box for already-up-to-date `:latest` pulls:

```
{"status":"Pulling from centroid-is/seatd","id":"latest"}
{"status":"Digest: sha256:abcd1234deadbeefcafefeed00112233445566778899aabbccddeeff00112233"}
{"status":"Status: Image is up to date for ghcr.io/centroid-is/seatd:latest"}
```

Asserts: `drainPullStream` returns `("sha256:abcd1234...11223", nil)`.

### 2. `TestDrainPullStream_AuxStillWinsWhenBothPresent`

Defensive: ensures the Status-Digest fallback never overrides a real aux digest.

```
{"status":"Digest: sha256:0000000000000000000000000000000000000000000000000000000000000000"}
{"aux":{"Digest":"sha256:b64c35a5deadbeefcafefeed00112233445566778899aabbccddeeff00112233"}}
```

Asserts: `drainPullStream` returns the aux digest (`sha256:b64c35a5...`), not the Status placeholder.

### 3. `TestDrainPullStream_StatusErrorStillShortCircuits`

Defensive: a Status-Digest line in the SAME JSON object as an Error field must surface the Error.

```
{"status":"Digest: sha256:aaaa","error":"something broke"}
```

Asserts: `drainPullStream` returns an error containing `"docker pull stream error"`.

## BUG-1 Test Fixture

`TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests` exercises the BUG-1 path with this image-id / repo-digest pair:

| Field | Value |
|-------|-------|
| Container ID | `bugonefixabc1234567890` |
| Service name | `svc` |
| Compose image ref | `ghcr.io/centroid-is/svc:v1` |
| Local image ID (`ContainerInspect.Image`) | `sha256:18136d85local` |
| Registry manifest digest (`ImageInspect.RepoDigests[0]`) | `ghcr.io/centroid-is/svc@sha256:b64c35a5deadbeefcafefeed00112233445566778899aabbccddeeff00112233` |
| Expected `state.Containers["svc"].CurrentDigest` after upsert | `sha256:b64c35a5deadbeefcafefeed00112233445566778899aabbccddeeff00112233` |

This is the same image-id / registry-digest split the HMI's flutter container exhibited in production (.planning/HANDOFF.md): local content-addressable hash vs registry manifest digest, conflated by the pre-fix discovery code.

## Verification Gates — All Green

| Gate | Status |
|------|--------|
| `go test ./... -race -count=1` | exit 0 |
| Exactly 1 `c.CurrentDigest =` assignment in discovery.go | pass (filtered comments) |
| The single assignment lives inside `upsertFromInspect`'s Apply closure | pass (awk slice) |
| Exactly 3 `d.updates <- poll.StateUpdate` send sites in discovery.go | pass (unchanged — no second StateUpdate) |
| `TestClient_InterfaceMethodCount` has `const want = 7` | pass |
| `strings.HasPrefix(msg.Status, "Digest: sha256:")` literal in orchestrator.go | pass (exactly 1 occurrence) |
| `"strings"` import in orchestrator.go | pass |
| `"docker pull stream ended without aux digest"` preserved verbatim | pass |
| `TestDiscoverer_UpsertSetsCurrentDigestFromRepoDigests` | PASS |
| `TestDrainPullStream_NoOpPull_DigestFromStatus` | PASS |
| `TestDrainPullStream_AuxStillWinsWhenBothPresent` | PASS |
| `TestDrainPullStream_StatusErrorStillShortCircuits` | PASS |
| `TestDiscoverer_InspectPrecedesUpdate` (anti-deadlock) | PASS |
| `TestDiscoverer_RefactoredUpsertSendsContainerEvent` (single-StateUpdate) | PASS |
| `TestMobyClient_SatisfiesClient` | PASS |

## Deviations from Plan

### Rule 3 (blocking issue): ImageInspect stubs added in Task 1 across all docker.Client fakes

**Found during:** Task 1 verification (`go test ./... -race -count=1`).

**Issue:** Adding `ImageInspect` to `docker.Client` is a Task-1 deliverable, but the plan only enumerated stub updates for `internal/docker/discovery_test.go::fakeClient` (Task 2) and `internal/actions/orchestrator_test.go::fakeDockerClient` (Task 2). Two additional `docker.Client` implementations exist in `internal/api/`:
- `internal/api/getstate_noio_test.go::panickingDockerClient`
- `internal/api/handlers_healthz_test.go::fakeClient`

Without these, Task 1's commit would not compile against the test build target — violating the per-commit "individually compile + pass `go test ./...`" constraint.

**Fix:** Added a minimal `ImageInspect` method to all four docker.Client fakes as part of Task 1's commit:
- `internal/docker/discovery_test.go::fakeClient` — zero `ImageInspect` (later expanded with scripted-response slot in Task 2)
- `internal/actions/orchestrator_test.go::fakeDockerClient` — zero `ImageInspect` (orchestrator tests don't exercise this path)
- `internal/api/getstate_noio_test.go::panickingDockerClient` — `panic(...)` consistent with the OBS-03 violation contract on every other method
- `internal/api/handlers_healthz_test.go::fakeClient` — zero `ImageInspect`

**Commit:** 0421aff (Task 1).

**Files affected by deviation:** 2 additional files (`internal/api/getstate_noio_test.go`, `internal/api/handlers_healthz_test.go`) — both `_test.go` files with single-method stubs; no production-code impact.

No Rule 1/2/4 deviations. No auth gates. No architectural decisions deferred.

## BUG-4 Status: Auto-Unblocked

The flip-rule in `internal/poll/poller.go` (lines 417-421) is unchanged:

```go
case cur.CurrentDigest != "":
    cur.UpdateAvailable = cur.CurrentDigest != resolvedDigest
```

Before BUG-1 fix: `CurrentDigest` was always empty, so this branch never fired and `UpdateAvailable` could never flip true via Rule 1. The Rule 2 fallback (`priorAvailable != "" && priorAvailable != resolvedDigest`) only fires on the SECOND poll tick after a digest change, never on boot.

After BUG-1 fix: `CurrentDigest` is populated on the first start event for every watched container; the Rule 1 branch fires correctly. The next cron sweep on the HMI will populate `UpdateAvailable=true` for any container whose registry digest differs from the running digest. No code change needed in the poller.

## Manual Smoke (post-deploy)

```bash
# Rebuild + push (CI will publish on the next push to main).
git push origin main

# After CI green:
ssh centroid@10.50.10.175 'cd /home/centroid && docker pull ghcr.io/centroid-is/docker-update:latest && docker compose up -d --force-recreate hmi-update'

# Verify BUG-1: every watched container's row now carries current_digest.
ssh centroid@10.50.10.175 'curl -sS http://localhost/api/state' | python3 -m json.tool | head -60

# Verify BUG-4 (auto-green from BUG-1): flutter and centroidx-backend show update_available=true.
# Verify BUG-5: click Update on seatd in the browser at http://10.50.10.175;
#   toast should show success (NOT action.pull_failed); logs show drainPullStream
#   returning the digest via the Status fallback.
ssh centroid@10.50.10.175 'docker logs hmi-update --tail 50 | grep -E "action|drain|discovery"'

# Optional post-validation cleanup: revert HMI cron to hourly.
ssh centroid@10.50.10.175 'sed -i "s/@every 30s/0 * * * */" /home/centroid/docker-compose.yml && cd /home/centroid && docker compose up -d hmi-update'
```

## Self-Check: PASSED

- **Files exist:**
  - `internal/docker/client.go` — FOUND
  - `internal/docker/moby.go` — FOUND
  - `internal/docker/moby_test.go` — FOUND
  - `internal/docker/discovery.go` — FOUND
  - `internal/docker/discovery_test.go` — FOUND
  - `internal/actions/orchestrator.go` — FOUND
  - `internal/actions/orchestrator_test.go` — FOUND
  - `internal/api/getstate_noio_test.go` — FOUND
  - `internal/api/handlers_healthz_test.go` — FOUND
- **Commits exist (in git log):**
  - `0421aff` — FOUND
  - `068d391` — FOUND
  - `37a9b84` — FOUND
- **Race-detector full suite pass:** PASS
- **All structural grep gates:** PASS
