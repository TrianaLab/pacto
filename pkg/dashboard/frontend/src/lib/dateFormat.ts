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
