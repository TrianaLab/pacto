import { describe, it, expect } from 'vitest';
import { cap } from './cap.svelte.ts';

describe('cap', () => {
  it('shows the first few until asked for all, and back', () => {
    const c = cap(2);
    expect(c.slice([1, 2, 3])).toEqual([1, 2]);
    c.toggle();
    expect(c.all).toBe(true);
    expect(c.slice([1, 2, 3])).toEqual([1, 2, 3]);
    c.toggle();
    expect(c.slice(null)).toEqual([]);
    expect(cap().max).toBe(25);
  });
});
