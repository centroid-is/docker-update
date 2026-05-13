import { execSync } from 'node:child_process';

export default async function globalTeardown() {
  execSync('docker compose -f compose.test.yml down -v --remove-orphans', {
    stdio: 'inherit',
  });
}
