/**
 * relativeTime renders a human-readable "X ago" string for an ISO-8601
 * timestamp relative to a "now" epoch ms. Resolution is 1 second.
 *
 * Returns "never" when iso is undefined or unparseable; otherwise:
 *   - <60 s    → "Xs ago"
 *   - <60 m    → "Xm Ys ago"
 *   - <24 h    → "Xh Ym ago"
 *   - ≥24 h    → "Xd Yh ago"
 *
 * The two-argument shape (iso, now) lets the caller drive the clock via a
 * Svelte 5 $state tick (Header.svelte ticks `now` every 1 s) without
 * coupling this pure formatter to Date.now() / globalThis. Pure function;
 * easy to unit-test without a clock fake.
 *
 * See 05-RESEARCH.md §I for the design rationale (no dayjs/date-fns dep).
 */
export function relativeTime(iso: string | undefined, now: number): string {
  if (!iso) return 'never';
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return 'never';
  const s = Math.max(0, Math.floor((now - then) / 1000));
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m ago`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h ago`;
}
