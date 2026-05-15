# Releasing hmi-update

> **Purpose:** This runbook documents how a Centroid field maintainer cuts a release of
> `hmi-update`. The brief's C4 constraint requires a manual smoke on an HMI-like stack
> before a release is "done"; this file is the human-side counterpart to the automated
> CI/publish workflows in `.github/workflows/`.

> **Audience:** Maintainers with `centroid-is/docker-update` write access who tag releases.

> **What you'll do:** Confirm pre-release prerequisites → manually smoke a candidate
> `:sha-<short>` image on an HMI-like stack → record the result in `SMOKE.md` → tag the
> release → confirm the publish workflow ran green.

The image is published at `ghcr.io/centroid-is/docker-update` (the GitHub repository
is `centroid-is/docker-update`). The binary, service name, and Go module path remain
`hmi-update` — only the publish target / container image path tracks the GitHub repo.

---

## 1. Pre-release checklist

Before tagging a release, every box below must be checked. Skipping any box is a
deliberate exception that should be recorded in the SMOKE.md entry.

- [ ] All v1 phase plans complete (verify with `cat .planning/STATE.md`).
- [ ] Latest commit on `main` shows a green `ci` workflow run on GitHub Actions:
      `https://github.com/centroid-is/docker-update/actions/workflows/ci.yml`
- [ ] Latest commit on `main` shows a green `publish` workflow run on GitHub Actions
      and the candidate `:sha-<short>` is available at
      `ghcr.io/centroid-is/docker-update:sha-<short>`.
- [ ] Local `make test-sigkill` passes (Phase 4 STATE-04 SIGKILL fault injection —
      deliberately not in CI per Phase 4 design).
- [ ] `README.md`'s install runbook reflects the current install procedure (Pitfall 9
      `id -g docker` step, three bind-mounts, etc.).
- [ ] No uncommitted edits on your `main` checkout (`git status` clean).
- [ ] You are on an HMI or an HMI-like stack (a Debian 12 box with Docker + Compose v2.20+
      installed, matching the production runtime — NOT a developer macOS / WSL2 box).
- [ ] A `SMOKE.md` entry is drafted (committed via a `release: vX.Y.Z prep` PR before
      tagging) — see §3.

## 2. Manual-smoke procedure

> **Why this exists:** Even with green CI and a passing publish workflow, the only way to
> confirm a release runs cleanly on real hardware is to run it on real hardware. This
> procedure exercises the install runbook plus the Update → Rollback round trip — the
> headline differentiator from WUD.
>
> **Display-blackout warning (Pitfall 5):** Do NOT pick `flutter` or `weston` as the
> smoke target — recreating them blanks the operator's screen. Pick a non-display-drawing
> service: a sidecar, a known-safe canary container, or a database service WITHOUT
> `hmi-update.allow-update=false` for the actual Update/Rollback exercise.

Steps:

1. **Identify the candidate `:sha-<short>` tag** — the most recent `sha-` tag at
   `https://github.com/centroid-is/docker-update/pkgs/container/docker-update` matching the
   commit at the tip of `main`. Verify locally:
   ```bash
   crane digest ghcr.io/centroid-is/docker-update:sha-$(git rev-parse --short HEAD)
   ```
   The digest must be non-empty.

2. **Pull on the HMI-like stack:**
   ```bash
   docker pull ghcr.io/centroid-is/docker-update:sha-$(git rev-parse --short HEAD)
   ```

3. **Follow the install runbook** at `README.md` (Install section). The runbook covers:
   - `id -g docker` and setting `user: "65532:<docker-gid>"` in compose.
   - The three bind mounts (`/var/run/docker.sock`, `docker-compose.yml:ro`,
     `hmi_update_state.json`).
   - The env vars (`HMI_UPDATE_CRON`, `HMI_UPDATE_COMPOSE_PATH`).
   - `docker compose up -d hmi-update`.

4. **Verify the UI loads:** open `http://<hmi-like-host>:8080/` in a browser.
   Expected: the watched-containers table renders with at least one row.

5. **Verify `/api/state`:**
   ```bash
   curl -fsS http://<hmi-like-host>:8080/api/state | jq '.version'
   ```
   Expected: `1`.

6. **Exercise Update → Rollback round trip** on a non-display-blackout service:
   a. Click `Update` on the chosen row. EXPECTED: container recreates within 30s, the
      UI's `current_digest` flips, `previous_digest` populates.
   b. Click `Rollback` immediately afterward. EXPECTED: container recreates within 15s
      back to the previous digest; `update_available` flips back on (because the
      registry `:latest` is still the newer digest).

7. **Capture evidence:** a screenshot of the UI showing both digests + the action timestamps
   in `slog` output (`docker logs hmi-update | tail -50`).

## 3. Recording the smoke

Append a new entry to `SMOKE.md` with the following template (most-recent at the top):

````markdown
### vX.Y.Z — YYYY-MM-DD

- **Candidate tag:** `ghcr.io/centroid-is/docker-update:sha-<short>`
- **Image digest:** `sha256:<digest>` (from `crane digest`)
- **Host:** Debian 12 / Docker Engine vXX.X / Compose vX.XX
- **Operator:** <name> <email>
- **Result:** PASS / PASS-WITH-NOTES / FAIL
- **Notes:** <one-paragraph summary — what was tested, anything unexpected>
````

The entry is the C4 "recorded manual smoke note." A release is NOT cut until the entry is
committed to `main` on a PR titled `release: vX.Y.Z prep`. The PR may include README /
CHANGELOG updates that are part of the release; the SMOKE.md entry is the one
required addition.

## 4. Tagging the release

Once SMOKE.md is committed on `main`:

```bash
git checkout main
git pull origin main
# Confirm HEAD matches the :sha-<short> you smoked.
git rev-parse --short HEAD
git tag -s -a vX.Y.Z -m "release vX.Y.Z

See SMOKE.md for the manual-smoke record."
git push origin vX.Y.Z
```

Notes:

- `git tag -s` produces a SIGNED annotated tag. Configure GPG signing per the GitHub
  docs (Settings → SSH and GPG keys) if you haven't already. An unsigned annotated
  tag (`git tag -a vX.Y.Z -m "..."`) is accepted as a v1 fallback but loses the
  trust-on-merge signal.
- The tag name pattern MUST match `v[0-9]+.[0-9]+.[0-9]+` (or `vX.Y.Z-<pre>`). The
  `publish.yml` workflow's `push: tags: ['v*.*.*']` trigger fires on the tag push.
- Within ~6–8 minutes, the publish workflow run completes; the image is tagged at
  `ghcr.io/centroid-is/docker-update:X.Y.Z` (no `v` prefix per `metadata-action`'s
  `pattern={{version}}`) AND at `ghcr.io/centroid-is/docker-update:sha-<short>`
  (identical digest to the candidate).
- Watch the run at:
  `https://github.com/centroid-is/docker-update/actions/workflows/publish.yml`
- Verify the three tags after publish:
  ```bash
  crane digest ghcr.io/centroid-is/docker-update:latest
  crane digest ghcr.io/centroid-is/docker-update:X.Y.Z
  crane digest ghcr.io/centroid-is/docker-update:sha-<short>
  ```
  All three digests must match. (Note: `:latest` is only re-emitted when the tag's
  ref is the default branch's HEAD; if you tag from an older `main` commit it stays
  pointed at whatever main most recently published.)

## 5. What to do if the smoke fails

If step 6 of §2 fails (Update/Rollback misbehaves) or any acceptance check in step 3-5
fails:

1. **Do NOT tag.** Stop the release.
2. Open a bug issue with the failure mode + the `docker logs hmi-update` excerpt.
3. Either:
   a. Roll back the offending commit on `main` and let the next green CI produce a new
      `:sha-<short>` candidate, OR
   b. Land a fix on `main`, wait for green CI, and restart this runbook from §1.
4. Optionally: record the failed attempt in SMOKE.md with a `FAIL` result and notes — keeps
   the trail intact for a future audit. Recommended for non-trivial failures.

## 6. Where to find evidence after release

For a tagged release `vX.Y.Z`:

- **CI run:** the green `ci.yml` workflow run on the merge commit before the tag.
  `https://github.com/centroid-is/docker-update/actions/workflows/ci.yml`
- **Publish run:** the green `publish.yml` workflow run triggered by the tag push.
  `https://github.com/centroid-is/docker-update/actions/workflows/publish.yml`
- **Manual-smoke record:** the `SMOKE.md` entry for vX.Y.Z.
- **Image on GHCR:**
  `https://github.com/centroid-is/docker-update/pkgs/container/docker-update`
  — confirm `:X.Y.Z`, `:latest`, and `:sha-<short>` all point to the same digest.

---

*Last updated: Phase 8 plan 08-03 (2026-05-15).*
