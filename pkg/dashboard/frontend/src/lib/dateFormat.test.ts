import { describe, it, expect } from 'vitest';
import { formatDate } from './dateFormat';

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
