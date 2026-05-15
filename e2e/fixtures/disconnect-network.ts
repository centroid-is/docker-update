// e2e/fixtures/disconnect-network.ts
//
// ACT-04: Rollback MUST work with the registry network detached. This
// fixture wraps `docker network disconnect` so the rollback-flow spec
// can simulate an offline HMI.
//
// Pattern matches push-image.ts (execFileSync child_process, argv split,
// no shell). The previous revision used execSync with template-string
// interpolation; BLOCKER-04 from the Phase 4 review (carry-forward of
// Phase 3 WR-08) replaced that with the argv-array form here so even a
// pathological compose-network name cannot drive shell injection.

import { execFileSync } from 'node:child_process';

/**
 * Identify the compose network name. The compose project is named after
 * the directory (`e2e`), so the default network is `e2e_default`. We
 * derive it dynamically via `docker network ls` to survive environment
 * differences (e.g. CI may set COMPOSE_PROJECT_NAME).
 *
 * argv form: no shell, no interpolation. The network name returned is
 * still passed downstream as a single argv element (see disconnect /
 * reconnect below) so it cannot be parsed as shell tokens.
 */
function getComposeNetwork(): string {
  const networks = execFileSync(
    'docker',
    ['network', 'ls', '--format', '{{.Name}}'],
    { encoding: 'utf8' },
  );
  const match = networks.split('\n').find((n) => /^e2e.*_default$/.test(n));
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
  execFileSync('docker', ['network', 'disconnect', net, 'zot'], {
    stdio: 'inherit',
  });
}

/**
 * Re-connect the zot service. Used in test cleanup to restore the
 * stack for subsequent tests. Always invoke from a finally{} block so
 * a failing assertion does not leave the stack partitioned.
 */
export function reconnectZot(): void {
  const net = getComposeNetwork();
  execFileSync('docker', ['network', 'connect', net, 'zot'], {
    stdio: 'inherit',
  });
}
