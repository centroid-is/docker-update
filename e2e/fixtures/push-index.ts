// Plan 03-05 Task 0 STUB — RED-FIRST placeholder.
//
// This file is a stub created during the C4 RED-FIRST step so the
// detect-multiarch.spec.ts file LOADS (TypeScript import succeeds) and
// the tests RUN to the call site, where they fail with a meaningful
// runtime error rather than a Playwright module-load error.
//
// Task 2 of Plan 03-05 replaces this stub with the real
// `crane index append` based implementation that pushes a multi-arch
// OCI image index to localhost:${ZOT_HOST_PORT}/<repo>:latest and
// returns the AMD64 child manifest digest.

export function pushFreshIndex(_repo: string): string {
  throw new Error(
    'pushFreshIndex not yet implemented — Plan 03-05 Task 2 lands the real crane-based multi-arch index push.',
  );
}
