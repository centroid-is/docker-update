// Multi-arch OCI image index push helper used by DETECT-04 e2e tests.
//
// Pushes an OCI image index with two children (linux/amd64 + linux/arm64),
// then returns the AMD64 CHILD manifest's digest. crane.Digest(WithPlatform(amd64))
// against this index MUST return the same digest — that is the load-bearing
// invariant of DETECT-04 (the resolver's WithPlatform(amd64) resolves the
// index to the amd64 child).
//
// Why crane CLI and not oras: oras does not have first-class OCI image
// index construction. crane's `index append` is the canonical tool —
// it's the same Go library (go-containerregistry) the resolver wraps.
//
// CLI sequence (each step uses the GLOBAL --insecure flag because zot
// runs plain-HTTP on localhost:15000):
//   1. `crane append --oci-empty-base --new_tag <bare-amd64>:<stamp> -f <tar>`
//      Builds a single-layer empty-base image and pushes it under a
//      throwaway tag. The resulting config.json has empty
//      architecture/os fields — crane.WithPlatform(amd64) on an index
//      built from this would NOT match, so step 2 fixes that.
//   2. `crane mutate --set-platform linux/amd64 -t <amd64Ref> <bareAmd64Ref>`
//      Rewrites the config.json's architecture+os to linux/amd64 and
//      pushes the new manifest under the platform-stamped tag. Same
//      flow for linux/arm64.
//   3. `crane index append -m <amd64Ref> -m <arm64Ref> -t <indexRef>`
//      Constructs the OCI image index (mediaType
//      application/vnd.oci.image.index.v1+json) referencing both
//      children and pushes it at `<repo>:latest`.
//   4. `crane digest --platform linux/amd64 <indexRef>` returns the
//      child digest that a resolver with WithPlatform(amd64) should
//      see. We return THIS value to the test.
//
// crane is expected on PATH. CI's workflow installs it via
// `go install github.com/google/go-containerregistry/cmd/crane@latest`
// (Plan 03-05 Task 2 documents the install step). Local dev: same.
//
// SAFETY: every push uses a per-invocation stamp so a fresh call
// produces a fresh index digest — required by DETECT-07 (the digest
// MUST change so the cron sweep can observe a flip).

import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';

const ZOT_HOST_PORT = process.env.ZOT_HOST_PORT ?? '15000';

/**
 * Pushes a multi-arch OCI image index to localhost:${ZOT_HOST_PORT}/<repo>:latest
 * with linux/amd64 and linux/arm64 children. Returns the AMD64 child
 * manifest digest (NOT the index digest).
 *
 * WR-08: uses execFileSync with argv arrays instead of execSync string
 * interpolation. No shell is invoked — every argument is passed
 * verbatim to the child process, so even an operator-controlled `repo`
 * containing shell metacharacters cannot trigger command injection.
 */
export function pushFreshIndex(repo: string): string {
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2)}`;
  const registry = `localhost:${ZOT_HOST_PORT}`;
  const bareAmd64Ref = `${registry}/${repo}:bare-amd64-${stamp}`;
  const bareArm64Ref = `${registry}/${repo}:bare-arm64-${stamp}`;
  const amd64Ref = `${registry}/${repo}:amd64-${stamp}`;
  const arm64Ref = `${registry}/${repo}:arm64-${stamp}`;
  const indexRef = `${registry}/${repo}:latest`;

  // Build two single-layer placeholder tarballs. Content is per-stamp so
  // the resulting layer + manifest digests are unique per call.
  const amd64Tar = `/tmp/amd64-${stamp}.tar.gz`;
  const arm64Tar = `/tmp/arm64-${stamp}.tar.gz`;
  const amd64TarBase = amd64Tar.replace(/.*\//, '').replace(/\.gz$/, '');
  const arm64TarBase = arm64Tar.replace(/.*\//, '').replace(/\.gz$/, '');
  writeFileSync(amd64Tar.replace(/\.gz$/, ''), `amd64-${stamp}`);
  writeFileSync(arm64Tar.replace(/\.gz$/, ''), `arm64-${stamp}`);
  // Re-pack as a real .tar.gz so crane.append's tarball reader accepts
  // it. We use plain `tar` to avoid a Node tar dep — the e2e harness
  // assumes a Unix-like host (CI: ubuntu-24.04; macOS: bsdtar accepts
  // the same flags here).
  execFileSync('tar', ['-czf', amd64Tar, '-C', '/tmp', amd64TarBase], {
    stdio: 'pipe',
  });
  execFileSync('tar', ['-czf', arm64Tar, '-C', '/tmp', arm64TarBase], {
    stdio: 'pipe',
  });

  // Step 1: empty-base appends. --oci-empty-base produces an OCI-typed
  // config (application/vnd.oci.image.config.v1+json) so the eventual
  // index uses OCI media types throughout.
  execFileSync(
    'crane',
    ['append', '--insecure', '--oci-empty-base', '--new_tag', bareAmd64Ref, '-f', amd64Tar],
    { stdio: 'pipe' },
  );
  execFileSync(
    'crane',
    ['append', '--insecure', '--oci-empty-base', '--new_tag', bareArm64Ref, '-f', arm64Tar],
    { stdio: 'pipe' },
  );

  // Step 2: stamp the platform onto each child via crane mutate. The
  // resulting manifest's referenced config.json has architecture and
  // os fields, which is what go-containerregistry's index-child
  // selection reads when resolving WithPlatform(amd64).
  execFileSync(
    'crane',
    ['mutate', '--insecure', '--set-platform', 'linux/amd64', '-t', amd64Ref, bareAmd64Ref],
    { stdio: 'pipe' },
  );
  execFileSync(
    'crane',
    ['mutate', '--insecure', '--set-platform', 'linux/arm64', '-t', arm64Ref, bareArm64Ref],
    { stdio: 'pipe' },
  );

  // Step 3: construct + push the OCI image index. --flatten=false keeps
  // each child as-is (we provide single-arch manifests, not nested
  // indexes; flatten=true would be a no-op here, but we set it
  // explicitly for clarity).
  execFileSync(
    'crane',
    [
      'index',
      'append',
      '--insecure',
      '--flatten=false',
      '-m',
      amd64Ref,
      '-m',
      arm64Ref,
      '-t',
      indexRef,
    ],
    { stdio: 'pipe' },
  );

  // Step 4: ask crane to resolve the index to its amd64 child digest.
  // This is exactly what internal/registry.Resolver does at runtime
  // (crane.Digest with WithPlatform(linux/amd64)).
  const amd64Digest = execFileSync(
    'crane',
    ['digest', '--insecure', '--platform', 'linux/amd64', indexRef],
    { encoding: 'utf8' },
  ).trim();
  if (!/^sha256:[0-9a-f]{64}$/.test(amd64Digest)) {
    throw new Error(`crane returned an invalid amd64 child digest: ${amd64Digest}`);
  }
  return amd64Digest;
}
