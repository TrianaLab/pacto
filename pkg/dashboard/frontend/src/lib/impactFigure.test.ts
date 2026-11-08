import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { impactModel, outcomeOf, classificationLabel, answerText, OUTCOMES } from './impactFigure.ts';
import { timeline, sessionKey } from './narrate.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;

const consumer = (key: string, o: Any = {}): Any => ({
  service: { kind: 'service', key, label: key, href: `/fleet/services/${key}` },
  path: [], pathTotal: 0, pathTruncated: false, depth: 1, direct: true, confidence: 'contractual', compatibilityVerdict: 'unknown', ...o,
});

function impact(by: Record<string, number>, o: Any = {}): Any {
  const { consumers, completeness, ...rest } = o;
  const total = Object.values(by).reduce((a, b) => a + b, 0);
  return {
    meta: { completeness: completeness ?? 'complete', asOf: '2026-11-07T14:04:00Z', sourceCounts: { total: 50, available: 48, partial: 0, stale: 0, unavailable: 2 } },
    service: { kind: 'service', key: 'payments-service', label: 'payments-service', href: '/fleet/services/payments-service' },
    oldRevision: { kind: 'revision', key: 'p@1', label: 'payments-service 2.0.1', version: '2.0.1', href: '' },
    newRevision: { kind: 'revision', key: 'p@2', label: 'payments-service 3.0.0', version: '3.0.0', href: '' },
    classification: 'BREAKING', gate: 'blocked', gateReason: 'it breaks',
    changes: { total: 3, count: 3, breaking: 1, potential: 1, nonBreaking: 1, truncated: false, items: [] },
    consumers: {
      total, count: 0, limit: 100, offset: 0, truncated: false, items: [], highlights: [], direct: Math.floor(total / 3), refused: 0,
      byVerdict: [], byConfidence: [], byOutcome: OUTCOMES.map((key) => ({ key, count: by[key] ?? 0 })),
      byOwner: [], beyondRanking: 0, noOwner: total, owners: 0, ...consumers,
    },
    ...rest,
  };
}

// The G1 scale case: 1,284 consumers, 8 ranked highlights, 3 hidden, partial.
const scale = (n = 1284) => {
  const inc = Math.max(1, Math.round(n * 0.03)), unk = Math.round(n * 0.04), uncl = Math.min(6, n - inc - unk);
  const highlights = [
    consumer('a'.repeat(200), { compatibilityVerdict: 'incompatible', blocking: true }),
    ...['b', 'c', 'd', 'e'].map((k) => consumer(k, { compatibilityVerdict: 'incompatible' })),
    ...['f', 'g', 'h'].map((k) => consumer(k)),
  ];
  return impact({ incompatible: inc, unknown: unk, compatible: n - inc - unk - Math.max(0, uncl), unclassified: Math.max(0, uncl) },
    { completeness: 'partial', consumers: { hidden: 3, refused: Math.min(5, inc), highlights } });
};

describe('impactModel lanes (G3)', () => {
  it('partitions the total in canonical order, with hidden outside it', () => {
    for (const n of [1, 8, 40, 400, 4000]) {
      const m = impactModel(scale(n));
      expect(m.lanes.length, `N=${n}`).toBeLessThanOrEqual(5);
      const counted = m.lanes.filter((l) => !l.ghost);
      expect(counted.reduce((a, l) => a + l.n, 0)).toBe(m.total);
      expect(counted.map((l) => l.key)).toEqual(OUTCOMES.filter((k) => k !== 'unclassified' || m.by.unclassified));
      expect(m.lanes.flatMap((l) => l.chips).length).toBeLessThanOrEqual(8);
      for (const l of m.lanes) expect(l.chips.length).toBeLessThanOrEqual(4);
      // Unknown and unclassified are the neutral hatch, never ok.
      for (const l of m.lanes.filter((x) => x.key === 'unknown' || x.key === 'unclassified')) expect([l.tone, l.hatch]).toEqual(['neutral', true]);
      const hidden = m.lanes.find((l) => l.ghost)!;
      expect(hidden.n).toBe(3);
      expect(hidden.chips).toEqual([]);
      expect(hidden.share).toBe(0);
    }
  });
  it('never makes refused a lane, and puts a refused consumer in the incompatible lane', () => {
    const m = impactModel(impact({ incompatible: 1, compatible: 1 }, {
      consumers: { refused: 1, highlights: [consumer('r', { compatibilityVerdict: 'compatible', unmet: [{ guarantee: 'delivery' }] })] },
    }));
    expect(m.lanes.map((l) => l.key)).not.toContain('refused');
    expect(m.lanes[0].chips.map((c) => c.key)).toEqual(['r']);
    expect(m.refused).toBe(1);
  });
  it('rings exactly one chip, the blocking one, and only when the gate blocks', () => {
    const rings = (im: Any) => impactModel(im).lanes.flatMap((l) => l.chips).filter((c) => c.ring).length;
    expect(rings(scale())).toBe(1);
    expect(rings({ ...scale(), gate: 'unverified' })).toBe(0);
  });
  it('tones no aggregate err except the incompatible lane (S10)', () => {
    const m = impactModel(scale(4000));
    expect(m.lanes.filter((l) => l.tone === 'err').map((l) => l.key)).toEqual(['incompatible']);
  });
});

describe('the gate sits under the lane that decides it', () => {
  it('blocked: the incompatible lane, and the caption says so', () => {
    const m = impactModel(scale());
    expect(m.lanes[m.gateLane].key).toBe('incompatible');
    expect(m.lanes.filter((l) => l.decides).map((l) => l.key)).toEqual(['incompatible']);
    expect(m.figRef).toBe('The gate is drawn under the incompatible lane, which decides it; the other lanes do not reach it.');
    expect(m.wires.filter((w) => w.to === 'gate').map((w) => w.from)).toEqual([`ln-${m.gateLane}`]);
  });
  it('unverified: the lanes it waits on', () => {
    const m = impactModel(impact({ unknown: 3, compatible: 2, unclassified: 1 }, { gate: 'unverified' }));
    expect(m.lanes.filter((l) => l.decides).map((l) => l.key)).toEqual(['unknown', 'unclassified']);
    expect(m.gateWhy).toBe('Release gate, decided by the unknown and unclassified lanes');
    expect(m.gate?.stamp).toBe('Needs a person');
  });
  it('unverified on range-only matches: the compatible lane', () => {
    const m = impactModel(impact({ compatible: 2 }, { gate: 'unverified' }));
    expect(m.lanes[m.gateLane].key).toBe('compatible');
  });
  it('clear on a change that is not breaking: no lane, the classification decides', () => {
    const m = impactModel(impact({ incompatible: 1, compatible: 4 }, { gate: 'clear', classification: 'NON_BREAKING' }));
    expect(m.gateLane).toBe(-1);
    expect(m.wires.some((w) => w.to === 'gate')).toBe(false);
    expect(m.gateWhy).toMatch(/classification/);
    expect(m.figRef).toBe('No lane reaches the gate.');
  });
  it('words a missing gate and a breaking change with no consumers', () => {
    expect(impactModel(impact({}, { gate: 'clear' })).gateWhy).toBe('Release gate: no consumer depends on this service');
    expect(impactModel(impact({}, { gate: 'unverified' })).gateWhy).toBe('Release gate: no consumer depends on this service');
    expect(impactModel(impact({ compatible: 1 }, { gate: 'clear' })).gateWhy).toBe('Release gate, decided by incomplete evidence');
    const none = impactModel(impact({ unknown: 1 }, { gate: '' }));
    expect(none.gate).toBeNull();
    expect(none.steps).toHaveLength(3);
    expect(none.figRef).toBe('');
  });
});

describe('the narration (G2)', () => {
  it('fits the budget, ends on the gate, and reveals every lane', () => {
    for (const n of [1, 400, 4000]) {
      const m = impactModel(scale(n));
      expect(timeline(m.steps).total).toBeLessThanOrEqual(1200);
      expect(m.steps.length).toBeLessThanOrEqual(5);
      expect(m.steps.at(-1)!.show).toContain('[data-m="gate"]');
      for (let k = 0; k < m.lanes.length; k++) expect(m.steps[2].show).toContain(`[data-m="ln-${k}"]`);
    }
  });
  it('keys the story on what it animates, never on asOf', () => {
    const a = scale();
    const b = { ...scale(), meta: { ...scale().meta, asOf: '2027-01-01T00:00:00Z' } };
    expect(sessionKey(impactModel(a).key)).toBe(sessionKey(impactModel(b).key));
    const c = impact({ incompatible: 9 });
    expect(sessionKey(impactModel(a).key)).not.toBe(sessionKey(impactModel(c).key));
  });
});

describe('partial coverage (S5a)', () => {
  it('says "at least" and names what is missing', () => {
    const m = impactModel(scale());
    expect(m.partial).toBe(true);
    expect(m.caption).toContain('of at least 1,284 consumers');
    expect(m.caption).toContain('consumers known only to the 2 unavailable sources are missing, so every share ends open');
    expect(m.alt).toContain('evidence partial');
    expect(impactModel(impact({ compatible: 2 }, { completeness: 'partial', meta: { completeness: 'partial' } })).missing).toBe('sources Pacto cannot read');
  });
  it('states a complete answer without the caveat', () => {
    const m = impactModel(impact({ incompatible: 2, compatible: 3 }));
    expect(m.caption).toBe('it breaks 2 of 5 consumers. The release gate blocks it.');
  });
});

describe('outcomeOf and the shared ranking fixture', () => {
  it('ranks the Go fixture exactly as highlightRank does', () => {
    const cases: Any[] = JSON.parse(readFileSync(join(process.cwd(), '../../impact/testdata/highlight-rank.json'), 'utf8'));
    const rank = (c: Any) => (c.blocking ? 0 : ({ incompatible: 1, unknown: 2, unclassified: 2, compatible: 3 } as Any)[outcomeOf(c)]);
    for (const c of cases) expect(rank(c), c.name).toBe(c.rank);
  });
  it('words the classification once', () => {
    expect(classificationLabel('POTENTIAL_BREAKING')).toBe('Potential breaking');
    expect(classificationLabel('')).toBe('Unknown');
  });
});

describe('answerText', () => {
  // Who the change reaches is the lanes' fact (one carrier, from byOutcome); the line
  // states only the diff, so a second tally can never contradict the lanes.
  it('states the diff, and that nobody is reached only when nobody is', () => {
    const im = impact({ incompatible: 1, unknown: 1 }, { consumers: { byVerdict: [{ key: 'incompatible', count: 1 }, { key: 'unknown', count: 1 }], refused: 1 } });
    expect(answerText(im)).toBe('3 changes, 1 breaking.');
    expect(answerText(impact({}, { changes: { total: 0 } }))).toBe('No contract differences. No consumer is affected.');
  });
});
