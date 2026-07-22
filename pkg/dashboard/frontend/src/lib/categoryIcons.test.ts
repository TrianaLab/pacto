import { describe, it, expect } from 'vitest';
import { categoryIconInner, isKnownCategory } from './categoryIcons';

// The canonical categories from pkg/contract/contract.go.
const CANONICAL = [
  'architecture', 'testing', 'code-quality', 'observability', 'security',
  'documentation', 'infrastructure', 'ci-cd', 'deployment', 'resilience',
  'backup-recovery', 'incident-response', 'compliance', 'other',
];

describe('categoryIcons', () => {
  it('has a non-empty SVG icon for every canonical category', () => {
    for (const c of CANONICAL) {
      expect(isKnownCategory(c)).toBe(true);
      const inner = categoryIconInner(c);
      expect(inner.length).toBeGreaterThan(0);
      expect(inner).toContain('<'); // real SVG child markup
    }
  });

  it('falls back to the "other" icon for unknown/custom categories', () => {
    expect(isKnownCategory('totally-custom')).toBe(false);
    expect(categoryIconInner('totally-custom')).toBe(categoryIconInner('other'));
    expect(categoryIconInner('')).toBe(categoryIconInner('other'));
  });
});
