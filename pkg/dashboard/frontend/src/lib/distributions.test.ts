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
  changeSegments, verdictSegments, confidenceSegments, outcomeSegments, segmentTotal,
  COMPLIANCE_STATES, statusHrefs, severityHrefs, bucketLabel, signatureSegments, signatureState,
  ownershipSegments, readinessSegments, canonicalRank, type Segment,
} from './distributions.ts';
import { statusLabel } from './format.ts';

describe('complianceSegments', () => {
  it('emits every canonical compliance state plus the catch-all, in a fixed order', () => {
    const s = complianceSegments({
      compliant: 3, nonCompliant: 2, unknown: 1, warning: 1,
      invalid: 1, reference: 1, notEvaluated: 1, other: 0,
    });
    expect(s.map((x) => x.label)).toEqual([
      'Not compliant', 'Invalid', 'Warning', 'Unknown',
      'Compliant', 'Reference', 'Not evaluated', 'Other',
    ]);
    expect(segmentTotal(s)).toBe(10);
  });

  // "We cannot evaluate this" is an open question: never a status tone, never green,
  // and hatched so it never reads as the flat grey of "Not evaluated".
  it('draws Unknown as the neutral hatch and Not evaluated as a flat neutral', () => {
    const s = complianceSegments({ unknown: 1, notEvaluated: 1 });
    const unknown = s.find((x) => x.label === 'Unknown');
    expect(unknown?.tone).toBe('neutral');
    expect(unknown?.hatch).toBe(true);
    expect(s.find((x) => x.label === 'Not evaluated')?.hatch).toBeUndefined();
  });

  // The unknown family is one look on every builder: neutral and hatched, never ok.
  it('never tones the unknown family ok, warn or err', () => {
    const all = [
      ...complianceSegments({}), ...severitySegments({}), ...signatureSegments({}),
      ...evidenceSegments({}),
      ...verdictSegments([{ key: 'unknown', count: 1 }]), ...confidenceSegments([{ key: 'unknown', count: 1 }]),
      ...outcomeSegments([{ key: 'unknown' }, { key: 'unclassified' }]),
    ];
    const hatched = all.filter((x) => x.hatch);
    expect(hatched.length).toBeGreaterThanOrEqual(8);
    for (const x of hatched) expect(x.tone, x.label).toBe('neutral');
  });

  // SPEC 2.2: err, warn, the unknown hatch, ok, whatever the data. Every builder whose
  // buckets are fixed is held to it, so a new bucket cannot land in the wrong place.
  it('orders every fixed-bucket builder err, warn, unknown hatch, ok', () => {
    const builders: [string, Segment[]][] = [
      ['compliance', complianceSegments({})], ['link', linkSegments({})], ['severity', severitySegments({})],
      ['evidence', evidenceSegments({})], ['change', changeSegments({})], ['signature', signatureSegments({})],
      ['ownership', ownershipSegments({})], ['readiness', readinessSegments({})],
      ['verdict', verdictSegments([{ key: 'incompatible' }, { key: 'unknown' }, { key: 'compatible' }])],
    ];
    for (const [name, segs] of builders) {
      const ranks = segs.map(canonicalRank);
      expect(ranks, name).toEqual([...ranks].sort((a, b) => a - b));
    }
  });

  it('treats a missing tally as all zeros instead of throwing', () => {
    expect(segmentTotal(complianceSegments(undefined))).toBe(0);
  });

  it('attaches a drill-down href per bucket when the caller supplies one', () => {
    const s = complianceSegments({ nonCompliant: 2 }, { nonCompliant: '#/fleet/attention?category=non-compliant' });
    expect(s.find((x) => x.label === 'Not compliant')?.href).toBe('#/fleet/attention?category=non-compliant');
    expect(s.find((x) => x.label === 'Compliant')?.href).toBeUndefined();
  });
});

describe('linkSegments', () => {
  // Revision-match certainty is kept separate from compliance precisely so the
  // difference between proof and a good guess stays visible.
  it('distinguishes proof from correlation: exact is ok, inferred is only info', () => {
    const s = linkSegments({ exact: 5, inferred: 2, ambiguous: 1, unresolved: 3 });
    expect(s.map((x) => [x.label, x.tone])).toEqual([
      ['Ambiguous', 'warn'], ['Unresolved', 'warn'], ['Exact', 'ok'], ['Inferred', 'info'],
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
    expect(s.map((x) => x.label)).toEqual(['Errors', 'Warnings', 'Unknown severity', 'Info']);
    expect(s[2].value).toBe(4);
    expect(segmentTotal(s)).toBe(10);
  });

  it('treats a missing tally as all zeros', () => {
    expect(segmentTotal(severitySegments(undefined))).toBe(0);
  });

  // The bar was the one summary on the attention page a reader could not act on.
  // A finding with no severity has no wire value, so it gets no dead-end link.
  it('drills down from every filterable severity, and only those', () => {
    const h = severityHrefs((v) => `#/x?severity=${v}`);
    expect(Object.keys(h).sort()).toEqual(['errors', 'infos', 'warnings']);
    expect(severitySegments({ errors: 1, unknown: 2 }, h)[0].href).toBe('#/x?severity=error');
    expect(severitySegments({ errors: 1, unknown: 2 }, h)[2].href).toBeUndefined();
  });
});

describe('evidenceSegments', () => {
  // "Not observed recently" and "never observed" are different statements, and the
  // fresh bucket is the remainder, never a separately-counted third number.
  it('splits observed targets into fresh and stale, keeping never-observed apart', () => {
    const s = evidenceSegments({ withEvidence: 10, stale: 4, withoutEvidence: 3 });
    expect(s.map((x) => [x.label, x.value])).toEqual([
      ['Stale evidence', 4], ['No evidence', 3], ['Fresh evidence', 6],
    ]);
  });

  it('never reports a negative fresh count when stale exceeds the observed total', () => {
    expect(evidenceSegments({ withEvidence: 2, stale: 5 })[2].value).toBe(0);
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
    expect(s[5].tone).toBe('neutral');
    expect(s[5].hatch).toBe(true);
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

/**
 * The bug this pins: the frontend used to spell the compliance buckets itself, matching
 * lowercase 'compliant'/'non-compliant' against the PascalCase values the wire actually
 * carries, so every service fell through to "Other" and the services list drew one flat
 * grey bar above rows badged Compliant, Unknown and Not compliant. The tally is now the
 * backend's, and the residual risk moved with it: a state table row whose `value` is not
 * a wire status, or whose wording is a second spelling of a badge.
 */
describe('COMPLIANCE_STATES', () => {
  // The canonical states pkg/fleet counts, one field each (mirrors
  // TestComplianceTally_EveryCanonicalStatusHasItsOwnBucket).
  const CANONICAL = ['NonCompliant', 'Invalid', 'Warning', 'Unknown', 'Compliant', 'Reference', 'NotEvaluated'];

  it('gives every canonical wire status its own bucket, never the catch-all', () => {
    expect(COMPLIANCE_STATES.filter((s) => s.value).map((s) => s.value)).toEqual(CANONICAL);
    expect(COMPLIANCE_STATES.filter((s) => !s.value).map((s) => s.field)).toEqual(['other']);
  });

  // A legend reading "Non-Compliant" over rows badged "Not compliant" asks the reader to
  // believe those are two states.
  it('words each bucket exactly as the row badges do', () => {
    for (const s of COMPLIANCE_STATES.filter((x) => x.value)) {
      expect(s.label).toBe(statusLabel(s.value));
    }
    expect(bucketLabel(COMPLIANCE_STATES, 'NotEvaluated')).toBe('Not evaluated');
    // An unknown value is shown as itself, not silently relabelled.
    expect(bucketLabel(COMPLIANCE_STATES, 'Quarantined')).toBe('Quarantined');
  });

  // Callers used to hand-list three or four buckets, so a fleet that was mostly
  // "Not evaluated" drew its largest slice as a dead end.
  it('drills down from every filterable bucket, and only those', () => {
    const h = statusHrefs((s) => `#/x?status=${s}`);
    expect(Object.keys(h).sort()).toEqual(CANONICAL.map((v) => v.charAt(0).toLowerCase() + v.slice(1)).sort());
    expect(h.notEvaluated).toBe('#/x?status=NotEvaluated');
    expect(h.other).toBeUndefined();
  });
});

describe('signatureSegments', () => {
  it('orders, words and tones the bundle signature states, unknown never positive', () => {
    const s = signatureSegments({ verified: 1, unverified: 2, unsigned: 3, unknown: 4, notChecked: 5 });
    expect(s.map((x) => [x.label, x.value, x.tone])).toEqual([
      ['Unverified', 2, 'err'], ['Unsigned', 3, 'warn'], ['Unknown', 4, 'neutral'],
      ['Verified', 1, 'ok'], ['Not checked', 5, 'neutral'],
    ]);
  });

  it('reads a missing tally as zeros', () => {
    expect(segmentTotal(signatureSegments(undefined))).toBe(0);
  });
});

describe('signatureState', () => {
  it('never shows verified without a signer', () => {
    expect(signatureState({ status: 'verified', signer: 'ci@acme.dev' }).label).toBe('Verified');
    expect(signatureState({ status: 'verified' }).label).toBe('Unknown');
    expect(signatureState(null).label).toBe('Not checked');
    expect(signatureState({ status: 'brand-new' }).tone).toBe('neutral');
    expect(signatureState({ status: 'unsigned' }).tone).toBe('warn');
  });
});
