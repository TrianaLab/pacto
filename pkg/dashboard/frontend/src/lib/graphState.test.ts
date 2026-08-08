import { describe, it, expect } from 'vitest';
import {
  graphStateFromParams, hasFocus, toggleView, differenceLabel, differenceTone,
  differenceDescription, relationLabel, neighborhoodIsEmpty, DEFAULT_VIEWS, MAX_DEPTH,
} from './graphState.ts';

describe('hasFocus', () => {
  it('is false without a kind or key (the discovery state)', () => {
    expect(hasFocus({})).toBe(false);
    expect(hasFocus({ kind: 'service' })).toBe(false);
    expect(hasFocus({ sel: 'domain-a/x' })).toBe(false);
  });
  it('is true only with both a kind and a key', () => {
    expect(hasFocus({ kind: 'service', sel: 'domain-a/x' })).toBe(true);
  });
});

describe('graphStateFromParams', () => {
  it('applies the focused defaults (depth 1, both, expected+differences)', () => {
    const gs = graphStateFromParams({ kind: 'service', sel: 'x' });
    expect(gs.depth).toBe(1);
    expect(gs.direction).toBe('both');
    expect(gs.views).toEqual(DEFAULT_VIEWS);
    expect(gs.perspective).toBe('service');
  });
  it('parses and clamps valid values, rejecting junk', () => {
    const gs = graphStateFromParams({ kind: 'revision', sel: 'x', perspective: 'revision', views: 'observed,differences', direction: 'dependencies', depth: '9' });
    expect(gs.perspective).toBe('revision');
    expect(gs.views).toEqual(['observed', 'differences']);
    expect(gs.direction).toBe('dependencies');
    expect(gs.depth).toBe(MAX_DEPTH); // clamped from 9
  });
  it('falls back to safe defaults for junk perspective/direction/views/depth', () => {
    const gs = graphStateFromParams({ kind: 'service', sel: 'x', perspective: 'bogus', views: 'nonsense,,', direction: 'sideways', depth: '-3' });
    expect(gs.perspective).toBe('service');
    expect(gs.direction).toBe('both');
    expect(gs.views).toEqual(DEFAULT_VIEWS);
    expect(gs.depth).toBe(1);
  });
});

describe('toggleView', () => {
  it('adds a view in canonical order', () => {
    expect(toggleView(['expected'], 'observed')).toEqual(['expected', 'observed']);
    expect(toggleView(['differences'], 'expected')).toEqual(['expected', 'differences']);
  });
  it('removes a present view', () => {
    expect(toggleView(['expected', 'observed'], 'observed')).toEqual(['expected']);
  });
  it('never produces an empty view set (keeps the last one)', () => {
    expect(toggleView(['expected'], 'expected')).toEqual(['expected']);
  });
});

describe('difference vocabulary (backend-authoritative, never color-only)', () => {
  it('gives every difference a distinct text label', () => {
    const labels = ['matched', 'expected-not-observed', 'observed-not-expected', 'insufficient'].map(differenceLabel);
    expect(new Set(labels).size).toBe(4); // all distinct
    expect(labels.every((l) => l.length > 0)).toBe(true);
  });
  it('distinguishes insufficient from a failure (info/neutral tone, honest wording)', () => {
    expect(differenceTone('insufficient')).toBe('neutral');
    expect(differenceDescription('insufficient')).toMatch(/no observation data/i);
    // expected-not-observed is explicitly NOT proof of being unused.
    expect(differenceDescription('expected-not-observed')).toMatch(/not proof/i);
  });
  it('observed-not-expected is a distinct, warned difference', () => {
    expect(differenceLabel('observed-not-expected')).toBe('Observed, not expected');
    expect(differenceTone('observed-not-expected')).toBe('warn');
  });
});

describe('relationLabel', () => {
  it('names runs vs dependency edges', () => {
    expect(relationLabel('runs')).toBe('Runs');
    expect(relationLabel('dependency')).toBe('Depends on');
  });
});

describe('neighborhoodIsEmpty', () => {
  it('is true for a focus with no edges and no unresolved deps', () => {
    expect(neighborhoodIsEmpty({ edges: [], unresolvedDependencies: { count: 0 } })).toBe(true);
  });
  it('is false when there are edges or unresolved deps', () => {
    expect(neighborhoodIsEmpty({ edges: [{}], unresolvedDependencies: { count: 0 } })).toBe(false);
    expect(neighborhoodIsEmpty({ edges: [], unresolvedDependencies: { count: 2 } })).toBe(false);
  });
});
