/**
 * Canonical date formatting for the dashboard.
 * Locale-stable, consistent across all views.
 */

/**
 * Format an ISO date string to human-readable form.
 * "2026-12-31" → "Dec 31, 2026"
 * Returns empty string on invalid/empty input.
 */
export function formatDate(iso: string | null | undefined): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  });
}

/**
 * Format an ISO timestamp as an age: "4 minutes ago", "3 hours ago", "Jul 29, 2026".
 *
 * Snapshot freshness is the one date in the product where the TIME is the whole point --
 * a snapshot taken four minutes ago and one taken twenty hours ago both render as
 * "Jul 29, 2026" through formatDate, which is a freshness claim the reader cannot check.
 *
 * `now` is a parameter, not a call to Date.now() inside, so the tests are wall-clock
 * independent -- a relative formatter tested against the real clock is a test that fails
 * once a year at a timezone boundary.
 */
export function formatRelative(iso: string | null | undefined, now: number = Date.now()): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  // Clock skew is normal: a backend a few seconds ahead must not print "in 3 seconds".
  const secs = Math.max(0, Math.round((now - t) / 1000));
  if (secs < 45) return 'just now';
  const mins = Math.round(secs / 60);
  if (mins < 60) return `${mins} minute${mins === 1 ? '' : 's'} ago`;
  const hours = Math.round(mins / 60);
  if (hours < 22) return `${hours} hour${hours === 1 ? '' : 's'} ago`;
  const days = Math.round(hours / 24);
  // Past a week the calendar date is more use than a running count, and it is the same
  // date every other surface prints.
  if (days < 7) return `${days} day${days === 1 ? '' : 's'} ago`;
  return formatDate(iso);
}
