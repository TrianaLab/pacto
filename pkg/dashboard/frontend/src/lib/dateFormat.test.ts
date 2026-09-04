import { describe, it, expect } from 'vitest';
import { formatDate, formatRelative } from './dateFormat';

describe('formatDate', () => {
  it('formats valid ISO date to human-readable form', () => {
    expect(formatDate('2026-12-31')).toBe('Dec 31, 2026');
    expect(formatDate('2025-01-01')).toBe('Jan 1, 2025');
    expect(formatDate('2026-06-15T10:30:00Z')).toBe('Jun 15, 2026');
  });

  it('returns empty string for null or undefined', () => {
    expect(formatDate(null)).toBe('');
    expect(formatDate(undefined)).toBe('');
  });

  it('returns empty string for invalid date strings', () => {
    expect(formatDate('')).toBe('');
    expect(formatDate('not-a-date')).toBe('');
    expect(formatDate('99999-99-99')).toBe('');
  });
});

describe('formatRelative', () => {
  // `now` is passed in, so these assertions are the same on every machine on every day.
  const NOW = Date.parse('2026-07-29T12:00:00Z');
  const ago = (ms: number) => new Date(NOW - ms).toISOString();
  const S = 1000, M = 60 * S, H = 60 * M, D = 24 * H;

  it('reads an age rather than a calendar date inside the first week', () => {
    expect(formatRelative(ago(10 * S), NOW)).toBe('just now');
    expect(formatRelative(ago(4 * M), NOW)).toBe('4 minutes ago');
    expect(formatRelative(ago(1 * M), NOW)).toBe('1 minute ago');
    expect(formatRelative(ago(3 * H), NOW)).toBe('3 hours ago');
    expect(formatRelative(ago(1 * H), NOW)).toBe('1 hour ago');
    expect(formatRelative(ago(3 * D), NOW)).toBe('3 days ago');
    expect(formatRelative(ago(25 * H), NOW)).toBe('1 day ago');
  });

  it('falls back to the calendar date past a week, matching every other surface', () => {
    expect(formatRelative('2026-06-15T10:30:00Z', NOW)).toBe('Jun 15, 2026');
  });

  it('never reads as the future when the backend clock runs ahead', () => {
    expect(formatRelative(new Date(NOW + 3 * S).toISOString(), NOW)).toBe('just now');
    expect(formatRelative(new Date(NOW + 5 * M).toISOString(), NOW)).toBe('just now');
  });

  it('returns empty string for missing or unparseable input', () => {
    expect(formatRelative(null, NOW)).toBe('');
    expect(formatRelative(undefined, NOW)).toBe('');
    expect(formatRelative('', NOW)).toBe('');
    expect(formatRelative('not-a-date', NOW)).toBe('');
  });
});
