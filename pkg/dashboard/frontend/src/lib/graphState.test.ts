import { describe, it, expect } from 'vitest';
import {
  graphStateFromParams, hasFocus, toggleView, differenceLabel, differenceTone,
  differenceDescription, relationLabel, neighborhoodIsEmpty, DEFAULT_VIEWS, MAX_DEPTH,
  defaultPerspectiveForKind, availablePerspectives, revisionLinkAuthoritative,
  perspectiveSupportsDepth, corroborationLabel, corroborationTone, serviceScopedCaveat,
  canonicalFocusForPerspective, projectionFocusMismatch,
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

describe('focus/perspective validity (requirement E)', () => {
  it('defaults the perspective from the entity kind', () => {
    expect(defaultPerspectiveForKind('service')).toBe('service');
    expect(defaultPerspectiveForKind('revision')).toBe('revision');
    expect(defaultPerspectiveForKind('target')).toBe('target');
    expect(defaultPerspectiveForKind('owner')).toBe('service');
  });

  it('offers only perspectives the backend will accept for a focus kind', () => {
    expect(availablePerspectives('service')).toEqual(['service']);
    expect(availablePerspectives('revision')).toEqual(['service', 'revision']);
    // a target is a revision projection only when its link is authoritative
    expect(availablePerspectives('target', { targetRevisionAuthoritative: false })).toEqual(['service', 'target']);
    expect(availablePerspectives('target', { targetRevisionAuthoritative: true })).toEqual(['service', 'revision', 'target']);
  });

  it('classifies an authoritative revision link', () => {
    expect(revisionLinkAuthoritative('exact')).toBe(true);
    expect(revisionLinkAuthoritative('inferred')).toBe(true);
    expect(revisionLinkAuthoritative('ambiguous')).toBe(false);
    expect(revisionLinkAuthoritative('unresolved')).toBe(false);
    expect(revisionLinkAuthoritative(undefined)).toBe(false);
  });

  it('marks the target perspective as one-hop (no real depth model)', () => {
    expect(perspectiveSupportsDepth('service')).toBe(true);
    expect(perspectiveSupportsDepth('revision')).toBe(true);
    expect(perspectiveSupportsDepth('target')).toBe(false);
  });
});

describe('service-scoped corroboration (requirement B)', () => {
  it('labels each corroboration verdict', () => {
    expect(corroborationLabel('matched')).toMatch(/corroborates/i);
    expect(corroborationLabel('expected-not-observed')).toMatch(/did not witness/i);
    expect(corroborationLabel('insufficient')).toMatch(/no service observation/i);
    expect(corroborationLabel(undefined)).toBe('');
  });
  it('tones the verdicts distinctly (never color alone, but a distinct tone helps)', () => {
    expect(corroborationTone('matched')).toBe('ok');
    expect(corroborationTone('expected-not-observed')).toBe('info');
    expect(corroborationTone('insufficient')).toBe('neutral');
  });
  it('caveats a fine-grained edge as service-scoped, not an edge-scope observation', () => {
    expect(serviceScopedCaveat('revision')).toMatch(/service-scoped corroboration/i);
    expect(serviceScopedCaveat('target')).toMatch(/service-scoped corroboration/i);
    expect(serviceScopedCaveat('service')).toBe('');
  });
});

describe('canonicalFocusForPerspective (Part 4: perspective changes canonicalize identity)', () => {
  const nb = {
    focusService: { kind: 'service', key: 'domain-a/web' },
    edges: [
      { relation: 'runs', to: { kind: 'revision', key: 'domain-a/web@sha256:1' } },
      { relation: 'dependency', to: { kind: 'service', key: 'domain-a/api' } },
    ],
  };

  it('target -> service returns the canonical service identity (from focusService)', () => {
    expect(canonicalFocusForPerspective(nb, 'target', 'service')).toEqual({ kind: 'service', key: 'domain-a/web' });
  });
  it('target -> revision returns the linked revision (from the runs edge), never inferred', () => {
    expect(canonicalFocusForPerspective(nb, 'target', 'revision')).toEqual({ kind: 'revision', key: 'domain-a/web@sha256:1' });
  });
  it('revision -> service returns the canonical service identity', () => {
    expect(canonicalFocusForPerspective(nb, 'revision', 'service')).toEqual({ kind: 'service', key: 'domain-a/web' });
  });
  it('keeps the current focus for same-identity transitions (revision->revision, target->target)', () => {
    expect(canonicalFocusForPerspective(nb, 'revision', 'revision')).toBeNull();
    expect(canonicalFocusForPerspective(nb, 'target', 'target')).toBeNull();
  });
  it('returns null when the needed backend ref is absent (no runs edge, no focusService)', () => {
    expect(canonicalFocusForPerspective({ edges: [] }, 'target', 'revision')).toBeNull();
    expect(canonicalFocusForPerspective({ edges: [] }, 'target', 'service')).toBeNull();
    expect(canonicalFocusForPerspective(null, 'target', 'service')).toBeNull();
  });
});

describe('projectionFocusMismatch (Part 4: canonicalize a bookmarked reinterpreted focus)', () => {
  it('returns the projection focus when the backend projected a different entity', () => {
    const nb = { projectionFocus: { kind: 'revision', key: 'domain-a/web@sha256:1' } };
    expect(projectionFocusMismatch(nb, 'target', 'prod/k8s/web')).toEqual({ kind: 'revision', key: 'domain-a/web@sha256:1' });
  });
  it('returns null when the projection focused exactly the requested entity', () => {
    const nb = { projectionFocus: { kind: 'revision', key: 'r1' } };
    expect(projectionFocusMismatch(nb, 'revision', 'r1')).toBeNull();
  });
  it('returns null when there is no projection focus', () => {
    expect(projectionFocusMismatch({}, 'target', 'x')).toBeNull();
    expect(projectionFocusMismatch(null, 'target', 'x')).toBeNull();
  });
});
