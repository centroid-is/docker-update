// Manifest-push helper used by globalSetup and (Phase 3+) by mid-test
// fixtures that need to flip a tag in the local zot registry.
//
// Each call writes a per-invocation timestamped payload so the resulting
// digest is unique — that's how downstream tests verify the registry
// resolver sees a new manifest. We rely on the `oras` CLI being on PATH;
// the GitHub Actions workflow installs it from releases, and developers
// install via `brew install oras` (see README onboarding).
//
// Phase 3 plan 03-05: pushFreshManifest gains an optional second
// parameter so DETECT-08 (tag-pattern) tests can push to a specific tag
// (e.g. :latest-pg17 vs :latest-pg18-oss) while preserving the
// no-args-default-:latest call shape used by global-setup.ts.
//
// If oras flakes in CI, swap the execSync call here for the crane-based
// Go helper in research/PHASE-01/manifest-push (RESEARCH.md §"Fallback Go helper").

import { execSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

// Host port that compose maps to zot's container-internal :5000.
// Container-to-container traffic (docker-update -> zot) uses `zot:5000` on
// the compose-internal network; only host-side oras pushes use this port.
// Overridable so CI runners without the macOS Control Center conflict can
// pin to 5000 if they prefer.
const ZOT_HOST_PORT = process.env.ZOT_HOST_PORT ?? '15000';

export interface PushOpts {
  /** Target tag in the registry. Defaults to "latest". */
  tag?: string;
}

export function pushFreshManifest(repo: string, opts: PushOpts = {}): string {
  const tag = opts.tag ?? 'latest';
  const file = `/tmp/payload-${Date.now()}-${Math.random().toString(36).slice(2)}.txt`;
  writeFileSync(file, `payload-${Date.now()}`);
  // --disable-path-validation: oras >= 1.3 refuses absolute file paths
  // unless this flag is set (security guard against accidental leaking of
  // host paths when oras-pulling). Test fixtures intentionally use /tmp
  // payloads, so we opt in explicitly.
  const out = execSync(
    `oras push --plain-http --disable-path-validation localhost:${ZOT_HOST_PORT}/${repo}:${tag} ${file}:text/plain`,
    { encoding: 'utf8' },
  );
  // oras prints "Pushed [registry] localhost:5000/...  Digest: sha256:..."
  const match = out.match(/Digest:\s+(sha256:[0-9a-f]+)/);
  if (!match) throw new Error(`oras output did not contain a Digest: ${out}`);
  return match[1];
}
