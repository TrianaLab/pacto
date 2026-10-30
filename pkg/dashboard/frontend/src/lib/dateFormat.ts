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
    // A bare YYYY-MM-DD is a calendar date, parsed as UTC midnight: read in local time
    // it printed the day before west of Greenwich.
    ...(/^\d{4}-\d{2}-\d{2}$/.test(iso) ? { timeZone: 'UTC' } : {}),
  });
}

/** todayUTC is the current calendar date in UTC as YYYY-MM-DD, the form contract dates
 *  take, so the two compare as text. `now` is a parameter so tests do not read the clock. */
export function todayUTC(now: number = Date.now()): string {
  return new Date(now).toISOString().slice(0, 10);
}

/**
 * Format an ISO timestamp with its time of day: "Jul 29, 2026, 14:03".
 * For lists ordered by time, where a date alone makes same-day rows look unordered.
 */
export function formatDateTime(iso: string | null | undefined): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  return new Date(t).toLocaleString('en-US', {
    year: 'numeric', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
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
