/**
 * The tally-to-segment mappings. Their whole job is to be the single place a state's
 * wording and tone are decided, so the same four compliance states never appear with
 * two vocabularies on two pages, and so a bucket the frontend does not recognize is
 * shown as itself rather than dropped from a chart that claims to cover everything.
 *
 * No function here derives a bucket or infers a state: the counts arrive already
 * computed by the backend over a complete population.
 */
import { describe, it, expect } from 'vitest';
import {
  complianceSegments, linkSegments, severitySegments, evidenceSegments,
  changeSegments, verdictSegments, confidenceSegments, segmentTotal,
} from './distributions.ts';

describe('complianceSegments', () => {
  it('emits all four Compliance 2.0 states plus the catch-all, in a fixed order', () => {
    const s = complianceSegments({ compliant: 3, nonCompliant: 2, unknown: 1, invalid: 1, other: 0 });
    expect(s.map((x) => x.label)).toEqual(['Compliant', 'Non-compliant', 'Unknown', 'Invalid', 'Other']);
    expect(segmentTotal(s)).toBe(7);
  });

  // "We cannot evaluate this" is an open question, not a benign state.
  it('tones Unknown as a warning rather than as neutral', () => {
    const s = complianceSegments({ unknown: 1 });
    expect(s.find((x) => x.label === 'Unknown')?.tone).toBe('warn');
  });

  it('treats a missing tally as all zeros instead of throwing', () => {
    expect(segmentTotal(complianceSegments(undefined))).toBe(0);
  });

  it('attaches a drill-down href per bucket when the caller supplies one', () => {
    const s = complianceSegments({ nonCompliant: 2 }, { nonCompliant: '#/fleet/attention?category=non-compliant' });
    expect(s.find((x) => x.label === 'Non-compliant')?.href).toBe('#/fleet/attention?category=non-compliant');
    expect(s.find((x) => x.label === 'Compliant')?.href).toBeUndefined();
  });
});

describe('linkSegments', () => {
  // Revision-match certainty is kept separate from compliance precisely so the
  // difference between proof and a good guess stays visible.
  it('distinguishes proof from correlation: exact is ok, inferred is only info', () => {
    const s = linkSegments({ exact: 5, inferred: 2, ambiguous: 1, unresolved: 3 });
    expect(s.map((x) => [x.label, x.tone])).toEqual([
      ['Exact', 'ok'], ['Inferred', 'info'], ['Ambiguous', 'warn'], ['Unresolved', 'warn'],
    ]);
    expect(segmentTotal(s)).toBe(11);
  });

  it('treats a missing tally as all zeros', () => {
    expect(segmentTotal(linkSegments(undefined))).toBe(0);
  });
});

describe('severitySegments', () => {
  it('keeps an unrecognized severity in its own bucket rather than folding it into info', () => {
    const s = severitySegments({ errors: 1, warnings: 2, infos: 3, unknown: 4 });
    expect(s.map((x) => x.label)).toEqual(['Errors', 'Warnings', 'Info', 'Unknown severity']);
    expect(s[3].value).toBe(4);
    expect(segmentTotal(s)).toBe(10);
  });

  it('treats a missing tally as all zeros', () => {
    expect(segmentTotal(severitySegments(undefined))).toBe(0);
  });
});

describe('evidenceSegments', () => {
  // "Not observed recently" and "never observed" are different statements, and the
  // fresh bucket is the remainder, never a separately-counted third number.
  it('splits observed targets into fresh and stale, keeping never-observed apart', () => {
    const s = evidenceSegments({ withEvidence: 10, stale: 4, withoutEvidence: 3 });
    expect(s.map((x) => [x.label, x.value])).toEqual([
      ['Fresh evidence', 6], ['Stale evidence', 4], ['No evidence', 3],
    ]);
  });

  it('never reports a negative fresh count when stale exceeds the observed total', () => {
    expect(evidenceSegments({ withEvidence: 2, stale: 5 })[0].value).toBe(0);
  });

  it('treats a missing tally as all zeros', () => {
    expect(segmentTotal(evidenceSegments(undefined))).toBe(0);
  });
});

describe('changeSegments', () => {
  it('renders the diff severity mix worst-first, with breaking as an error', () => {
    const s = changeSegments({ breaking: 2, potential: 3, nonBreaking: 7 });
    expect(s.map((x) => [x.label, x.value, x.tone])).toEqual([
      ['Breaking', 2, 'err'], ['Potentially breaking', 3, 'warn'], ['Non-breaking', 7, 'ok'],
    ]);
    expect(segmentTotal(s)).toBe(12);
  });

  it('treats a missing preview as all zeros', () => {
    expect(segmentTotal(changeSegments(undefined))).toBe(0);
  });
});

describe('impact consumer buckets', () => {
  it('preserves the backend ordering, because the backend ranked the whole blast radius', () => {
    const s = verdictSegments([{ key: 'incompatible', count: 4 }, { key: 'compatible', count: 9 }]);
    expect(s.map((x) => x.label)).toEqual(['Incompatible', 'Compatible']);
    expect(s[0].tone).toBe('err');
    expect(s[1].tone).toBe('ok');
  });

  it('labels evidence confidence in the product vocabulary, not the wire enum', () => {
    const s = confidenceSegments([
      { key: 'corroborated', count: 1 }, { key: 'contractual', count: 2 }, { key: 'declared', count: 3 },
      { key: 'observed', count: 4 }, { key: 'inferred', count: 5 }, { key: 'unknown', count: 6 },
    ]);
    expect(s.map((x) => x.label)).toEqual([
      'Declared and observed', 'Declared with a range', 'Declared only',
      'Observed only', 'Reached through another service', 'Evidence incomplete',
    ]);
    expect(s[5].tone).toBe('warn');
  });

  // A newer engine emitting a value this build has never seen must still appear in the
  // chart: dropping it would understate the blast radius silently.
  it('shows an unrecognized key as itself in neutral rather than dropping it', () => {
    const s = verdictSegments([{ key: 'quarantined', count: 2 }]);
    expect(s).toEqual([{ label: 'quarantined', value: 2, tone: 'neutral' }]);
  });

  it('handles a bucket with neither key nor count, and a missing list', () => {
    expect(verdictSegments([{}])).toEqual([{ label: 'Unknown', value: 0, tone: 'neutral' }]);
    expect(confidenceSegments(undefined)).toEqual([]);
  });
});
