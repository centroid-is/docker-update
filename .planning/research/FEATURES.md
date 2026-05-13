# Feature Research

**Domain:** Per-container Docker image update detection + rollback (compose-native, single-host, LAN-only field-engineer tooling)
**Researched:** 2026-05-13
**Confidence:** HIGH (competitor tooling and patterns well-documented; primary uncertainty is around mid-2025–2026 minor releases of WUD and Komodo)

## Context recap

The 2026 ecosystem split into three lanes after **Watchtower was archived on 2025-12-17** (confirmed: GitHub repo archived, no active maintenance):

1. **Notifier-only** — Diun (notifies, never acts).
2. **Auto-updater w/ varying safety** — Watchtower (archived), Shepherd (Swarm), Dockhand (with health-check rollback), Tugtainer (rollback-on-failure).
3. **Platform / dashboard** — Portainer (full mgmt), Komodo (GitOps stacks, has rollback), WUD (detect + trigger-driven update, **no rollback**).

`hmi-update` deliberately lives between (2) and (3): it has a UI like Portainer/WUD, manual update like Tugtainer, **explicit operator-driven rollback like Komodo** (which neither WUD nor Watchtower has), but ships as **one binary, one JSON file** — no MongoDB (Komodo), no Node app stack (WUD), no shelf of triggers (WUD/Diun).

The PR-based lane (Renovate/Dependabot) is intentionally orthogonal — those tools never touch a running host; they open PRs against compose files in a Git repo. `hmi-update` operates on the running stack, not the source.

## Competitor capability snapshot

| Tool | Detect `:latest` digest flip | Manual update button | Rollback | Persistence | Footprint | Why it doesn't fit Centroid |
|------|------------------------------|----------------------|----------|-------------|-----------|------------------------------|
| WUD 8.x | Yes (multi-arch + digest) | Trigger-driven (docker/docker-compose triggers) | **No** | LowDB (file-based, JS) | ~150–300 MB image, Node runtime | No rollback; requires `sed` patches for arm-handling and anon-creds bugs in 8.2.2; trigger model is awkward for per-row manual action |
| Komodo 2.x | Yes (poll-driven on Stacks) | Yes ("Update Available" button per stack) | **Yes** (since 1.18 stack-level; per-service rollback proposed in #1276) | **MongoDB** | 3-container deploy | MongoDB violates "no DB" budget; per-HMI Stack setup is manual UI work; state doesn't travel with the compose file |
| Diun | Yes (digest + tag) | **No** (notify only) | No | BoltDB | ~30 MB | Pure notifier; no actuation, no UI for action |
| Watchtower (archived 2025-12-17) | Yes (digest-based) | Indirect (HTTP API to trigger run) | **No** (explicit non-goal) | None (in-memory) | ~30 MB | Auto-update model = wrong shape; archived |
| nicholas-fedor/watchtower (fork) | Yes | Yes (HTTP API) | No | None | ~30 MB | Carries forward Watchtower's auto-update bias; no UI |
| Portainer CE | Yes (image update indicators, "Pull latest image" toggle on recreate) | Yes (per stack/container) | **No** dedicated rollback — relies on git revert + redeploy | SQLite/BoltDB | ~250 MB image, agent model | Heavy; designed for ops dashboards, not single-purpose field tooling |
| Shepherd | Yes (Swarm services) | No (auto only) | **Yes — `ROLLBACK_ON_FAILURE`** but only for failed updates (uses `docker service update --rollback`) | None | ~20 MB | Swarm-only; not applicable to plain compose |
| Dockhand | Yes | Yes (manual + scheduled) | **Yes — pull→recreate→healthcheck→rollback-on-failure** (auto, not operator-driven) | File | ~80 MB | Rollback is reactive (failed healthcheck), not "user pressed Rollback after they saw the new build was bad" |
| Tugtainer | Yes | Yes (per-container manual) | **Yes — rollback-on-recreate-failure** | SQLite + volume | ~100 MB | Closest in spirit; rollback is failure-driven, not operator-driven; auth required; broader Docker management (start/stop/inspect) inflates scope |
| Ouroboros (deprecated 2020) | Yes | No | No | None | ~30 MB | Abandoned ("devs have succumbed to real life") |
| Renovate / Dependabot | Yes (against Git repo's compose file) | N/A — opens PRs | Via `git revert` | Git | N/A | Doesn't touch running hosts; wrong layer entirely for field engineers without Git workflow on the HMI |

**Key insight from the matrix:** *No existing tool has operator-driven, per-container, single-slot, immediate rollback to the previous digest.* Every "rollback" found in the wild is one of:
1. Reactive (Dockhand, Tugtainer, Shepherd — fires only when the new image's healthcheck fails).
2. GitOps (Komodo, Renovate — rollback = revert the source-of-truth).
3. Manual via raw Docker (Watchtower's official advice in discussion #2099 — "recreate with the previous image yourself").

That gap is the differentiator for `hmi-update`.

## Feature Landscape

### Table Stakes (Users Expect These)

Features Centroid field engineers will expect by analogy with WUD / Komodo / Tugtainer. Missing any of these = the tool feels broken and engineers go back to WUD-with-patches.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Per-container update detection against `:latest` (digest-based, not tag-based) | WUD/Diun/Watchtower/Komodo all do this; the whole reason WUD exists | MEDIUM | The hard part is multi-arch manifests: HEAD `/v2/<repo>/manifests/<tag>` returns an OCI index for multi-arch — must GET the index, filter `linux/amd64`, then HEAD the platform manifest. This is the bug WUD 8.2.2 has that the Centroid `sed` patches fix. F1. |
| Manual "Update" action per container | Tugtainer, WUD (via docker-compose trigger), Komodo, Portainer all expose this | MEDIUM | Pull → record prior digest → `compose up -d --force-recreate <service>`. F2. |
| View current digest + available digest in a UI | Standard across WUD/Portainer/Komodo/Tugtainer; engineers need to confirm what's about to change | LOW | Short-form display (12 chars) in the row, full digest on copy-icon click. F6. |
| Status badge (`up-to-date` / `update-available` / `rollback-available` / `disabled`) | Portainer's "image up to date" column, WUD's badge, Komodo's "Update Available" pill | LOW | Pure derivation from state; no extra storage. F6. |
| Health/liveness endpoint | Every K8s/compose deployment expects `/healthz`; the absence of one is a footgun | LOW | `GET /healthz` = state file readable + docker socket reachable. N8. |
| Full state inspection endpoint | Komodo has a state inspector; Tugtainer shows JSON; engineers need a fallback when the UI is broken | LOW | `GET /api/state` returns the JSON state verbatim. N8. |
| Persisted state across restarts | Komodo (Mongo), Tugtainer (SQLite), WUD (LowDB) all do this; without persistence, "previous digest" evaporates on container restart and rollback is impossible | LOW–MEDIUM | Single JSON file with atomic temp+rename writes. N2, F4. The "single-file" choice is itself the differentiator vs Komodo's Mongo. |
| Idempotent update/rollback (no-op when already at target) | Required to make the operation re-tryable; Komodo, Watchtower all do this | LOW | Compare RepoDigest to target digest before pull. 200 + no-op response. N3. |
| Auto-discovery of watched containers via labels | WUD uses `wud.watch`, Watchtower uses `com.centurylinklabs.watchtower.enable`, Dockhand uses `dockhand.enable` — label-based opt-in is the universal pattern | LOW | `hmi-update.watch=true` opt-in; matches existing mental model. F1. |
| Live UI refresh while operator is watching | Portainer/Tugtainer poll every few seconds; engineers expect the row to flip after they click | LOW | Svelte 5 `onMount` interval, 5s GET `/api/state`. F6. |
| Manual "force a poll now" button | WUD has "Watch all containers now"; Komodo has "Check for updates"; impatient operators need it | LOW | `POST /api/poll` triggers the cron-job function out of band. F6 (header). |
| Docker socket via bind mount, no daemon-over-TCP | Universal pattern across the entire competitor set | LOW | `/var/run/docker.sock:/var/run/docker.sock`. F7. |
| Structured logging for every action | Komodo logs every action; Dockhand emits Prometheus metrics; engineers need an audit trail when something goes sideways on a customer site | LOW | `slog` JSON: container, before/after digests, exit code, duration. N7. |
| Disabled-state buttons (no rollback target → button is greyed out) | Standard UI hygiene; otherwise engineers click and get cryptic errors | LOW | Server returns enough state to derive button enablement client-side; server also enforces. F6, N4. |

### Differentiators (Competitive Advantage)

Where `hmi-update` deliberately goes beyond, or against, the competitor pack. Each one is a direct response to a documented gap in WUD/Komodo/Watchtower.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **Operator-driven per-container rollback to the previous digest** | No tool in the field offers this. Watchtower's docs literally say "we don't do rollback; recreate the container yourself" (discussion #2099). Komodo has stack-level rollback (since 1.18) but per-service is still a feature request (#1276). Dockhand/Tugtainer/Shepherd only roll back automatically on a failed healthcheck — they cannot roll back a *successful* update that the operator now regrets. | MEDIUM | `docker tag <image>@<previous_digest> <image>:<tag>` locally + `compose up -d --force-recreate`. The previous-digest tracking and atomic state-file commit are the whole game. F3. |
| **Single-slot rollback that toggles** | Tugtainer/Dockhand have "rollback once after failure"; nothing has "press Rollback again to flip back to the new image you just left." For the elevator HMI workflow this matches the operator's actual mental model: "I updated, it looks broken, take me back; oh wait it was fine, take me forward." | LOW (incremental on F3) | After rollback, the previously-current digest becomes the new `previous_digest`. The state file ends up with exactly two digests bouncing back and forth until a real new push arrives. F3. |
| **Single binary, single JSON file, single OCI image** | Komodo's biggest differentiator vs WUD is also its biggest cost: MongoDB + core + periphery containers. Centroid's "copy compose, `up -d`, done" deployment model is incompatible with that. Every competing tool that has both UI and rollback (Komodo, Portainer) requires multiple containers and a database. | MEDIUM (it's a project constraint, not really a "feature" — but it's what wins over Komodo) | Stage 1 Node builds Svelte, stage 2 Go compiles with `//go:embed`, final stage distroless/static. <30MB. C1, C2, N6. |
| **Compose-native, mutates the running stack via `docker compose -f /host/docker-compose.yml`** | WUD's docker-compose trigger has a regression (issue #546) where it sometimes updates the container but doesn't update the file; Komodo's stack model requires re-deploying through Komodo's abstraction. `hmi-update` reaches *into* the operator's existing `docker-compose.yml` — the file is the source of truth, the binary is just a remote control. | MEDIUM | Mount the compose file read-only. Use `docker compose` subprocess (the Go SDK lacks compose primitives). The compose file is never modified — only the image's local digest tag is. This is subtly different from WUD's "rewrite the image tag in the YAML" approach, and avoids the WUD trigger bug entirely. F2, F3, F7. |
| **Force-pull endpoint** | None of the competitors have this exact thing — they all assume "if digests match, don't pull." But operators on isolated HMIs sometimes need to recover from "I accidentally `docker image prune -a`'d." Komodo's `auto_pull` is closest but is global, not per-container. Diun won't pull at all. | LOW | One endpoint: pulls `:latest` and recreates regardless of digest match. UI button is a third action per row. F8 / Q6. |
| **Server-enforced safety labels (`allow-update=false`, `allow-rollback=false`)** | Komodo has stack-level locking; WUD has `wud.watch=false` (exclusion, not action-locking); none have per-action locking with both UI hide *and* server-side 409. Centroid needs this for timescaledb specifically — the database must never be force-recreated by accident. | LOW | Read labels at poll time, cache on container state, hide button in UI **and** return 409 from POST. F2/F3, N4. |
| **Tag-pattern constraint label (`hmi-update.tag-pattern=<regex>`)** | WUD has `wud.tag.include` / `wud.tag.exclude` but they're string-list filters, not regex constraints on what counts as "comparable to current." Required for timescaledb's `latest-pg17` → must not be confused with `latest-pg18`. Komodo/Watchtower/Dockhand have no equivalent. | LOW | Compile regex once, filter manifest tag list before comparison. F5. |
| **Compose service name as the stable API identifier** | Watchtower, WUD, Tugtainer mostly use container names — which change when `docker compose up --force-recreate` re-creates them. The service name is the stable identifier in compose semantics; using it kills a class of "container disappeared from the UI after I clicked Update" bugs. | LOW | Read `com.docker.compose.service` label from running containers. Q3. |
| **Embedded Svelte 5 UI served from the same Go binary on port 8080** | Most tools with UIs (Portainer, Komodo, Tugtainer) ship the frontend in a separate container or as static files on disk. WUD ships the UI as part of the Node app, but the whole runtime is Node. `//go:embed` on a distroless image is the absolute minimum surface area for "tool with a UI." | LOW (it's a stack choice) | Vite build → `dist/` → embedded by `//go:embed` → served on the same router as the API. F6, C1. |

### Anti-Features (Commonly Requested, Often Problematic)

Features that look reasonable on first glance, are present in many competitors, and would be wrong for `hmi-update` v1. These are documented in PROJECT.md "Out of Scope" — repeated here with the **specific competitor that has them** and the **specific reason they don't fit here**.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **Auto-update on detection** | Watchtower's entire premise; Komodo's `auto_update` toggle; Shepherd; Dockhand can do scheduled auto-update. "It's right there in WUD, just turn it on." | An elevator HMI is a customer-facing piece of safety-adjacent kit. A silent overnight update that breaks `centroidx-backend` is a field-engineer truck roll. Field engineers also need to *be present* when the screen reboots so they can verify the elevator UI comes back. The whole product premise is "explicit operator action." Auto-update would defeat the rollback feature too — by the time anyone notices, the previous-digest slot has been overwritten by another auto-update. | Notification-only UI badge ("update available"); operator presses the button when they're on-site. Future webhook to surface the badge into a chat channel (deferred). |
| **Multi-host fleet management** | Komodo's headline feature; Portainer Edge agent; FleetLock for k3s. Centroid's HMI count is growing — "wouldn't it be nice to update all 12 at once?" | Each HMI is a separate physical site with its own service window and customer. The unit of operation is one HMI, one engineer, one elevator. A multi-host orchestrator adds: secret distribution, identity, RBAC, central state, network exposure beyond LAN. Massive scope-creep for zero current value. Komodo's three-container architecture is precisely the cost of supporting this. | Engineer logs into each HMI's `:8080` over the customer LAN. If we later want centralized visibility, a stateless aggregator (read-only) is the pattern. |
| **Authentication / RBAC** | Portainer, Komodo, Tugtainer all have it. Security questionnaires will ask about it. | Matches the existing WUD posture exactly — the HMI is on a customer LAN, behind their firewall; the threat model is "what if a malicious customer-network device hits port 8080" and the mitigation is "the network is the perimeter." Adding auth = TLS terminator, password storage, password reset flow, lost-password recovery — none of which makes sense for an engineer who has physical access to the box anyway. | LAN-only deployment. Document the threat model. Future phase if the deployment model changes (e.g. exposed to the public internet via tunnel). |
| **Logs viewer / shell exec / stats** | Portainer's bread-and-butter; Tugtainer has a "container inspection" panel. "If I'm already looking at the container, just let me see the logs." | This is exactly the scope-creep that turned every Docker GUI into a Portainer clone. Engineers already have `docker logs`, `docker exec`, `docker stats` over SSH. Duplicating that means: TTY emulation, log streaming, websockets, security review of arbitrary command execution. Massive surface for zero new capability. | Direct SSH to the HMI. The whole tool is one screen. |
| **Notifications (Slack/email/MQTT/webhook)** | Diun's entire reason for existing; WUD's headline feature ("triggers"); Dockhand supports nine notification providers. | The user flow is "engineer arrives at HMI for scheduled maintenance, opens browser, sees what's pending, decides." There's no async actor needing to be notified — the engineer is the only person who can act, and they're already at the box when they open the page. Adding notifications = config surface for providers, secret storage for API tokens, delivery-retry logic. | Banner in the UI ("3 updates available"). Future phase: optional webhook out, no provider plugins. |
| **Private registry credentials** | Every serious tool has them; Watchtower, WUD, Dockhand all support `~/.docker/config.json` mounting. | All current images are public (GHCR public registry for `centroid-is/*`, Docker Hub public for `timescale/timescaledb`). Premature complexity. The day a private registry shows up, mount `~/.docker/config.json` into the container — standard pattern, easy retrofit. | Deferred. Document the future retrofit pattern. |
| **N-deep rollback history** | Komodo's GitOps model implicitly gives unlimited history; theoretically valuable ("which of the last 5 builds was the good one?"). | The toggle workflow is "I just updated, was that wrong, take me back." Pre-toggle state plus current state = 2 slots = enough. N-deep means: a UI list, a digest picker, a way to prune old digests, more state file complexity, more edge cases. Concrete cost, abstract benefit. | Single-slot. If someone genuinely wants `previous_previous`, they can keep their own `image:tag-old` locally. Future phase if a real use case emerges. |
| **arm64 image builds** | Standard for any modern container tool. | Current `elevator-hmi` hardware is amd64. New ARM HMIs are "future hardware," not landed yet. Buildx multi-arch costs CI minutes, doubles the image-tag matrix, requires arm64 emulation in tests. | Flip a CI matrix entry when the first ARM HMI lands. ~1 hour of work. |
| **Drift detection on `image: …@sha256:…` pinned services** | "Surely the tool should tell me a pinned image is now outdated?" — natural intuition. | Pinned digests are *the explicit opt-out*. If timescaledb upstream re-publishes `:latest-pg17` (digest A), and our compose file is pinned to digest B, that's not drift — that's "we deliberately chose B." Reporting drift here means re-interpreting the user's pin as a mistake. Ambiguous semantics. | `image: <name>@sha256:<digest>` is treated as "this service has opted out of detection entirely." It just doesn't appear in the UI's watched list. Q4. |
| **Auto-pruning of old images** | Tugtainer has it; Dockhand has it; Watchtower has `--cleanup`. "After 20 updates, the disk fills with dangling images." | Pruning the previous-digest image while a rollback target still points to it is catastrophic — the rollback would have to re-pull from the registry, which might be offline or have advanced past that digest. Manual `docker image prune` on a service schedule is the right discipline. | Document it in the README's operations section. Maybe add an `/api/prune` endpoint in a future phase with explicit "this will delete the rollback target" warning. |
| **Container start/stop/restart controls** | Tugtainer has it; Portainer has it; "while we're here, why not?" | Out of scope (PROJECT.md). The tool does exactly two things to a container: pull a new image and recreate at a new or old digest. Adding start/stop = duplicating `docker compose`. | `docker compose start/stop/restart <service>` over SSH. |
| **Container creation / deletion** | Portainer/Komodo headline features. | Out of scope. The tool only operates on services that already exist in the user's compose file. | Edit the compose file, `docker compose up -d`, then the new service appears in the watched list. |
| **UI kit (skeleton.dev, daisyui, etc.)** | Faster to build forms/toasts. | Project's "no extra deps" ethos. Tailwind + hand-rolled components for a 6-row table with three buttons is ~150 lines of Svelte. A UI kit would inflate the bundle and the surface area. | Tailwind-only, hand-rolled toast and badge. Q7. |
| **Server-side WebSockets / SSE for real-time UI updates** | Portainer, Komodo do this. | The action latency that matters here (pull + recreate) is 5–30 seconds; polling every 5s is fine. WebSockets add: stateful connections, reconnect logic, server-side fan-out. | Client polls `GET /api/state` every 5 seconds while open. |

## Feature Dependencies

```
F1 Update detection (poll + docker events)
    └──requires──> docker client wrapper (internal/docker)
    └──requires──> registry HEAD/GET with multi-arch handling (internal/registry)
    └──requires──> state persistence (F4) [reads "tag-pattern", caches "current_digest"]

F2 Manual update
    └──requires──> F1 (must know what to pull from)
    └──requires──> F4 (must record previous_digest atomically before recreate)
    └──requires──> docker compose subprocess wrapper

F3 Manual rollback
    └──requires──> F4 (reads previous_digest)
    └──requires──> F2's prior execution (otherwise previous_digest is empty)
    └──requires──> docker compose subprocess wrapper
    └──enhanced-by──> single-slot toggle semantic (rollback writes the
                       just-displaced digest back into previous_digest)

F4 State persistence (./hmi_update_state.json, atomic writes)
    └──underpins──> F1, F2, F3, F8, N2, N4
    └──enhanced-by──> compose service name as identifier (Q3 decision)

F5 Tag-pattern label
    └──refines──> F1 (filters candidate tags from the registry response)

F6 Web UI
    └──requires──> F1, F2, F3 (these are what the buttons trigger)
    └──requires──> F4 (renders current/available/previous digest columns)
    └──requires──> //go:embed of dist/
    └──enhanced-by──> N4 (allow-update/allow-rollback hides buttons)
    └──enhanced-by──> F8 (third button per row)

F7 Compose deployment
    └──requires──> single OCI image (constraint C1)
    └──requires──> bind-mount paths from docker-compose.yml and state file
    └──conflicts──> multi-host orchestration (anti-feature)

F8 Force-pull endpoint
    └──requires──> docker client wrapper
    └──independent of──> F1 (force-pull doesn't care about detection)
    └──enhances──> recovery from "local image accidentally pruned"

N1 Portability ──derives from──> F7 (compose deployment)
N2 Stateless restart ──requires──> F4
N3 Idempotency ──refines──> F2, F3, F8
N4 Server-enforced safety ──refines──> F2, F3
N7 Structured logging ──cross-cuts──> F1, F2, F3, F8
N8 /healthz, /api/state ──cross-cuts──> all
```

### Dependency Notes

- **F1 requires registry primitives:** the multi-arch manifest handling is the exact piece WUD 8.2.2 gets wrong and that the Centroid `sed` patches fix. Worth writing as a small standalone package (`internal/registry`) with table-driven unit tests covering: single-arch manifest, multi-arch OCI index with amd64/arm64, manifest without `Docker-Content-Digest` header (Docker Hub edge case), unauthenticated GHCR vs Docker Hub anonymous token flow.
- **F3 requires F4 to be solid:** rollback's correctness lives in the atomic write of `previous_digest` *before* the recreate command runs. If the state file write fails after the pull but before the recreate, the user loses the rollback target. Order: pull → atomic-write state with new `current_digest` and old digest in `previous_digest` → recreate. If recreate fails, the state file already reflects the new digest but the running container is on the old one — that's a recoverable inconsistency (the next poll fixes it; the rollback button still does the right thing).
- **F6 enhanced by F8 with caveats:** the force-pull button is a third per-row action. The UI's row layout has to accommodate three buttons cleanly on a 1024px-wide HMI screen. Worth a small UX pass before finalising the row template — disabled-state visuals already need a pattern (greyed background + lock icon for label-disabled, greyed background + "no rollback" tooltip for missing-previous-digest).
- **F5 isolated from F1's core logic:** the tag-pattern filter applies at the *tag enumeration* step, before manifest fetch. If the pattern is missing or doesn't match the current tag, the existing single-tag behaviour (HEAD on the configured tag) applies. Defaulting to "no filter" means it's purely additive.
- **N4 is a cross-cutting check, not a feature in itself:** both the UI's button-render and the API handler's authorization check have to read the same labels. Risk: UI and server disagree about whether an action is allowed. Mitigation: derive UI button-enablement from the same `state.containers[name].allow_update` field the server uses; never have the UI guess.

## MVP Definition

### Launch With (v1)

Mapped 1:1 to the `Active` F-requirements in PROJECT.md. Each is justified below.

- [ ] **F1 update detection** — without this, nothing else matters. The whole tool exists because WUD's detection is fragile and Komodo's is too heavy.
- [ ] **F2 manual update** — the "act" half of the value proposition. Without this we've built another Diun (notifier-only) and gained nothing over the WUD-with-`sed`-patches setup.
- [ ] **F3 manual rollback** — *the* differentiator. The reason for not adopting WUD. If we ship without this, we've built a worse WUD.
- [ ] **F4 state persistence** — required for F3 (rollback needs the previous digest) and N2 (stateless restart). Atomic writes are non-negotiable; the failure mode of a partial write is "container is in an unknown state forever."
- [ ] **F5 tag-pattern label** — required by the production stack. Without it, timescaledb will get a false-positive "update available" the day `latest-pg18-oss` ships. Cheap to add, immediately demonstrable on the real stack.
- [ ] **F6 Svelte UI** — without it, this is a CLI and engineers won't reach for it. The "one button per container" promise is the brand.
- [ ] **F7 compose deployment** — the on-HMI install model. Without it we're not yet a product.
- [ ] **F8 force-pull endpoint** — small surface, high-value recovery affordance. Decided up-front (PROJECT.md Key Decisions) rather than deferred.
- [ ] **N1–N8 nonfunctionals** — these are quality bars, not separable features. Skipping any one of them creates a footgun.

### Add After Validation (v1.x)

Triggered by "we shipped, used it on N HMIs, and now want to add..."

- [ ] **arm64 image build** — trigger: first ARM HMI hardware lands. Cost: one CI matrix entry, distroless/static is already multi-arch.
- [ ] **Optional outbound webhook on update-available** — trigger: engineers want a "morning email" of what's pending across their fleet. Cost: one endpoint + retry policy, no provider zoo.
- [ ] **Private registry credentials** — trigger: first non-public image lands in the stack. Cost: mount `~/.docker/config.json`, ~1 day of code + tests.
- [ ] **Image prune endpoint** — trigger: engineers report dangling-image disk pressure. Cost: one endpoint that excludes the rollback-target digest from the prune candidate set.
- [ ] **Force a poll for a single container** (vs the global `Watch now`) — trigger: operator wants to test one row without waiting for the whole sweep. Cost: trivial.

### Future Consideration (v2+)

Defer until product-market fit is established (i.e. multiple HMIs in production, real operator feedback).

- [ ] **N-deep rollback history** — defer until someone actually loses work due to single-slot. Likely never; the toggle pattern is sufficient.
- [ ] **Multi-host aggregation (read-only)** — defer until fleet > 10. Even then, a separate aggregator service is cleaner than building it in.
- [ ] **Authentication** — defer until a non-LAN deployment is on the table.
- [ ] **Drift detection for digest-pinned services** — defer indefinitely; semantics are ambiguous (see Anti-Features table).
- [ ] **Compose-file rewriting (à la WUD's docker-compose trigger)** — defer indefinitely; explicitly avoided because of WUD's #546 regression. The compose file is the user's source of truth, not ours to edit.

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| F1 Update detection (multi-arch correct) | HIGH | MEDIUM | **P1** |
| F2 Manual update | HIGH | MEDIUM | **P1** |
| F3 Manual rollback (single-slot toggle) | HIGH | MEDIUM | **P1** |
| F4 State persistence (atomic JSON) | HIGH | LOW | **P1** |
| F5 Tag-pattern label | HIGH (required for timescaledb) | LOW | **P1** |
| F6 Svelte UI | HIGH | MEDIUM | **P1** |
| F7 Compose deployment | HIGH (deployment requirement) | LOW | **P1** |
| F8 Force-pull endpoint | MEDIUM | LOW | **P1** |
| N4 Server-enforced safety labels | HIGH (safety-critical for timescaledb) | LOW | **P1** |
| N7 Structured logging | MEDIUM | LOW | **P1** |
| N8 /healthz + /api/state | MEDIUM | LOW | **P1** |
| arm64 build | LOW (today) | LOW | **P2** |
| Webhook out | LOW | LOW | **P2** |
| Private registry creds | LOW (today) | LOW | **P2** |
| Image prune | LOW | LOW | **P2** |
| N-deep rollback | LOW | MEDIUM | **P3** |
| Multi-host aggregation | LOW | HIGH | **P3** |
| Authentication | LOW (LAN-only) | MEDIUM | **P3** |

**Priority key:**
- P1: Must have for launch — every active F- and N-requirement is here.
- P2: Should have, add when a concrete trigger arrives (ARM hardware, private images, etc.).
- P3: Nice to have, future consideration; defer until validated by real usage.

## Competitor Feature Analysis

Side-by-side comparison on the dimensions that matter for the Centroid use case. The "Our Approach" column is the call we're making and why.

| Feature | WUD | Komodo | Watchtower (archived) | Diun | Tugtainer | Dockhand | **hmi-update** |
|---------|-----|--------|----------------------|------|-----------|----------|----------------|
| Multi-arch digest detection | Yes (buggy in 8.2.2) | Yes | Yes | Yes | Yes | Yes | **Yes** — directly addresses WUD's bug |
| Per-container manual update | Via triggers (awkward) | Per-stack, not per-service | HTTP API (no UI) | No | Yes | Yes | **Yes — primary affordance** |
| Operator-driven rollback | **No** | Stack-level only (#1276 open for per-service) | **No** (explicit non-goal) | N/A | Failure-driven only | Failure-driven only | **Yes — per-service, single-slot, operator-initiated** |
| Persistence | LowDB | MongoDB | None | BoltDB | SQLite | File | **Single JSON file, atomic writes** — eliminates the DB |
| Auth/RBAC | Yes (since 8.x) | Yes | N/A | N/A | Yes | No | **No — LAN-only** |
| Auto-update | Optional | Optional | Default | N/A (notify-only) | Optional | Optional | **Never. Operator-driven only** |
| UI | Yes (Vue, Node-served) | Yes (React, separate container) | None | None | Yes (separate) | Yes | **Yes — Svelte 5, `//go:embed`, same binary** |
| Notifications | Many providers | Yes | Email + webhook | Many providers | Apprise | Nine providers | **None in v1; webhook in v1.x** |
| Compose-native | Rewrites compose file (#546 broken) | Wraps compose in Stack abstraction | Operates on running containers only | N/A | Operates on running containers | Operates on running containers | **Reads compose, never writes; uses `docker compose` subprocess for recreate** |
| Per-action safety labels | Exclusion only | Stack-level lock | Per-container monitor-only | N/A | Per-container disable | Per-container `dockhand.disable` | **Per-action: `allow-update` and `allow-rollback` independently, server-enforced** |
| Tag-pattern filter | String include/exclude | No | No | Yes (tag regex) | No | SemVer policy | **Regex on tag list — `^latest-pg17$` style** |
| Force-pull when digest matches | No | `auto_pull` global | No | N/A | No | No | **Yes — per-container endpoint and button** |
| Image footprint | ~150–300 MB | 3 containers + DB | ~30 MB | ~30 MB | ~100 MB | ~80 MB | **<30 MB target (distroless/static)** |
| RAM at idle | ~80 MB (Node) | Hundreds (Mongo) | ~10 MB | ~10 MB | ~50 MB | ~30 MB | **<30 MB target** |
| Single-host scope | Yes | Multi-host | Yes | Yes | Multi-host | Yes | **Yes — explicit single-HMI scope** |

## Sources

Primary competitor research:
- [getwud/wud — GitHub](https://github.com/getwud/wud) — WUD 8.x feature set, label conventions, trigger model
- [moghtech/komodo — GitHub](https://github.com/moghtech/komodo) — Komodo 2.x features, stack-level rollback, auto-update model
- [Komodo per-service rollback feature request — Issue #1276](https://github.com/moghtech/komodo/issues/1276) — confirms per-service rollback is NOT in Komodo today
- [Komodo auto-update discussion — #238](https://github.com/moghtech/komodo/discussions/238) — auto_pull and update flow details
- [crazy-max/diun — GitHub](https://github.com/crazy-max/diun) — notifier-only model, scope boundary
- [containrrr/watchtower — GitHub](https://github.com/containrrr/watchtower) (archived 2025-12-17) — confirms archived status
- [Watchtower rollback discussion #2099](https://github.com/containrrr/watchtower/discussions/2099) — "Watchtower does not perform rollback" — quoted directly
- [containrrr/shepherd — GitHub](https://github.com/containrrr/shepherd) — Swarm-only rollback, `ROLLBACK_ON_FAILURE`
- [izm1chael/Dockhand — GitHub](https://github.com/izm1chael/Dockhand) — pull→recreate→healthcheck→rollback-on-failure model
- [Quenary/tugtainer — GitHub](https://github.com/Quenary/tugtainer) — closest spirit competitor; per-container manual + rollback-on-failure
- [pyouroboros/ouroboros — GitHub](https://github.com/pyouroboros/ouroboros) — confirms abandoned, "devs have succumbed to real life"

Watchtower discontinuation context:
- [LinuxHandbook — Watchtower discontinued alternatives](https://linuxhandbook.com/blog/watchtower-like-docker-tools/) — 2025-12 ecosystem realignment
- [XDA — what I use instead of Watchtower](https://www.xda-developers.com/with-watchtower-discontinued-heres-how-i-update-containers/) — confirms WUD as the most-cited replacement

Portainer's update model:
- [Portainer — pull latest image feature](https://www.portainer.io/blog/pull-latest-image-feature-in-ce) — toggle on recreate
- [Portainer — image update indicators FAQ](https://docs.portainer.io/faqs/troubleshooting/stacks-deployments-and-updates/how-does-the-image-update-notification-icon-work)

Docker Compose rollback patterns:
- [Docker Compose force-recreate docs](https://docs.docker.com/reference/cli/docker/compose/up/)
- [Kristof Kovacs — docker-compose update and rollback](https://kkovacs.eu/docker-compose-rollback/) — manual digest-tracking pattern that `hmi-update` automates
- [LinuxServer.io — updating and backing up containers with version control](https://www.linuxserver.io/blog/2019-10-01-updating-and-backing-up-docker-containers-with-version-control)

Renovate / Dependabot (PR-based, complementary lane):
- [Renovate docker-compose manager docs](https://docs.renovatebot.com/modules/manager/docker-compose/) — confirms the Git-PR-based lane; orthogonal to `hmi-update`

WUD-specific bugs and limitations:
- [WUD #546 — docker-compose trigger doesn't update file](https://github.com/getwud/wud/issues/546) — confirms WUD's compose-trigger regression that `hmi-update` avoids by *not* rewriting the compose file
- [WUD #691 — selective auto-update is exclusion-only](https://github.com/getwud/wud/issues/691) — confirms the per-action safety-label gap

---
*Feature research for: per-container Docker update + rollback (compose-native, single-host)*
*Researched: 2026-05-13*
