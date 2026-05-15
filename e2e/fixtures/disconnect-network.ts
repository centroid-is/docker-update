// e2e/fixtures/disconnect-network.ts
//
// ACT-04: Rollback MUST work with the registry network detached. This
// fixture wraps `docker network disconnect` so the rollback-flow spec
// can simulate an offline HMI.
//
// Pattern matches push-image.ts (execSync child_process).
//
// SECURITY NOTE (WR-08 carry-forward from Phase 3 review): execSync with
// string interpolation is risky when operator input flows into the shell
// command. Here the network name comes from `docker network ls`, filtered
// by a regex against the compose default-suffix pattern (/e2e.*_default$/).
// The container name is the hardcoded string "zot". Neither value is
// operator-supplied; both originate inside the test harness. execSync is
// safe at this surface. If a future revision admits operator-supplied
// names, pivot to execFileSync with an argv split (no shell).

import { execSync } from 'node:child_process';

/**
 * Identify the compose network name. The compose project is named after
 * the directory (`e2e`), so the default network is `e2e_default`. We
 * derive it dynamically via `docker network ls` to survive environment
 * differences (e.g. CI may set COMPOSE_PROJECT_NAME).
 */
function getComposeNetwork(): string {
  const networks = execSync(`docker network ls --format '{{.Name}}'`, {
    encoding: 'utf8',
  });
  const match = networks.split('\n').find((n) => /e2e.*_default$/.test(n));
  if (!match) {
    throw new Error(
      `Could not find e2e compose network in:\n${networks}\nIs the stack up?`,
    );
  }
  return match;
}

/**
 * Disconnect the zot service from the compose network. After this call,
 * hmi-update can still talk to the docker daemon (the bind-mounted
 * socket), but the registry is unreachable. ImagePull will fail; ImageTag
 * (local re-tag) will succeed — that's the ACT-04 contract.
 */
export function disconnectZotFromNetwork(): void {
  const net = getComposeNetwork();
  execSync(`docker network disconnect ${net} zot`, { stdio: 'inherit' });
}

/**
 * Re-connect the zot service. Used in test cleanup to restore the
 * stack for subsequent tests. Always invoke from a finally{} block so
 * a failing assertion does not leave the stack partitioned.
 */
export function reconnectZot(): void {
  const net = getComposeNetwork();
  execSync(`docker network connect ${net} zot`, { stdio: 'inherit' });
}
