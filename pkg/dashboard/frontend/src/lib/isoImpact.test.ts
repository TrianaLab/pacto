import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { slab, ringPath, fit, verdictOf, TONE, classificationLabel, IMPACT_GATE, layoutImpact } from './isoImpact.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;

const FSI = '\u2068';
const PDI = '\u2069';
const visible = (s: string) => Array.from(s.replace(/[\u2068\u2069]/g, '')).length;

function consumer(key: string, o: Any = {}): Any {
  const { label, domain, ...rest } = o;
  return {
    service: { kind: 'service', key, label: label ?? key, domain, href: '' },
    path: [], pathTotal: 0, pathTruncated: false, depth: 1, direct: true,
    confidence: 'contractual', compatibilityVerdict: 'unknown', ...rest,
  };
}

function impact(o: Any = {}): Any {
  const { consumers, completeness, ...rest } = o;
  return {
    meta: { completeness: completeness ?? 'complete' },
    service: { kind: 'service', key: 'payments-service', label: 'payments-service', href: '' },
    oldRevision: { kind: 'revision', key: 'p@1', label: 'payments-service 1.0.0', version: '1.0.0', href: '' },
    newRevision: { kind: 'revision', key: 'p@2', label: 'payments-service 2.0.1', version: '2.0.1', href: '' },
    classification: 'BREAKING', gate: 'blocked', gateReason: '',
    consumers: { total: 0, count: 0, limit: 100, offset: 0, truncated: false, items: [], highlights: [], direct: 0, refused: 0, byVerdict: [], byConfidence: [], ...consumers },
    ...rest,
  };
}

// The demo's real case (spec 2.3): the backend ranks orders-service (blocking) first.
const demo = () => impact({
  consumers: {
    total: 4, direct: 2,
    highlights: [
      consumer('orders-service', { compatibilityVerdict: 'incompatible', compatibility: '^1.0.0', owner: 'commerce-team', blocking: true }),
      consumer('api-gateway', { compatibilityVerdict: 'incompatible', compatibility: '^1.0.0', owner: 'platform-foundations' }),
    ],
  },
});

const VERDICTS = ['incompatible', 'compatible', 'unknown', 'compatible', 'incompatible', 'unknown', 'compatible', 'compatible', 'unknown', 'incompatible', 'compatible', 'unknown'];
// Twelve direct consumers in the Go ranking order (blocking, err, unknown, compatible, key),
// capped at 8 by the backend.
function twelve(o: Any = {}): Any {
  const all = VERDICTS.map((v, i) => consumer(`svc-${String(i).padStart(2, '0')}`, { compatibilityVerdict: v, compatibility: v === 'unknown' ? undefined : '~1.4.2', blocking: i === 0 }));
  const rank = (c: Any) => (c.blocking ? 0 : ({ err: 1, neutral: 2, ok: 3 } as Any)[TONE[verdictOf(c)]]);
  const ranked = [...all].sort((a, b) => rank(a) - rank(b) || a.service.key.localeCompare(b.service.key));
  return impact({ consumers: { total: 1284, direct: 97, nextOffset: 100, highlights: ranked.slice(0, 8), ...o } });
}

describe('slab and ringPath', () => {
  it('reproduce the hero checkout slab and ring byte for byte', () => {
    expect(slab(114, 388, 74, 14)).toEqual({
      top: 'M114 351 L188 388 L114 425 L40 388 Z',
      left: 'M40 388 L114 425 L114 439 L40 402 Z',
      right: 'M114 425 L188 388 L188 402 L114 439 Z',
      edge: 'M114 351 L188 388 L114 425 L40 388 Z M40 388 V402 L114 439 L188 402 V388 M114 425 V439',
    });
    expect(ringPath(114, 388, 74)).toBe('M114 345 L200 388 L114 431 L28 388 Z');
  });
});

describe('fit', () => {
  it('keeps short text and cuts long text to exactly n code points', () => {
    expect(fit('ledger', 8)).toBe('ledger');
    const cut = fit('notification-worker', 10);
    expect(Array.from(cut)).toHaveLength(10);
    expect(cut.endsWith('…')).toBe(true);
  });
  it('never splits an emoji at the cut point', () => {
    const out = fit('abcd😀efgh', 6);
    expect(out).toBe('abcd😀…');
    expect(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])/.test(out)).toBe(false);
    expect(fit('abc😀', 4)).toBe('abc😀');
  });
  it('keeps the version and the owner prefix outside a bidi override in a name', () => {
    const L = layoutImpact(impact({
      service: { kind: 'service', key: 'evil', label: 'evil\u202Ename', href: '' },
      consumers: { total: 1, direct: 1, highlights: [consumer('c', { owner: 'team\u202Ex' })] },
    }));
    expect(L.leaders.next).toBe(`${FSI}evil\u202Ename${PDI} ${FSI}2.0.1${PDI}`);
    expect(L.consumers[0].owner).toBe(`owner ${FSI}team\u202Ex${PDI}`);
  });
});

describe('verdictOf', () => {
  it('lets an unmet guarantee beat a compatible range', () => {
    expect(verdictOf(consumer('a', { compatibilityVerdict: 'compatible', unmet: [{ guarantee: 'delivery' }] }))).toBe('refused');
  });
  it('maps every unrecognised verdict to unknown, which is neutral and never ok', () => {
    for (const v of ['unknown', '', undefined, 'something-new']) {
      expect(verdictOf(consumer('a', { compatibilityVerdict: v }))).toBe('unknown');
    }
    expect(TONE.unknown).toBe('neutral');
  });
  it('ranks the shared Go fixture exactly as highlightRank does', () => {
    // pkg/impact/highlight_rank_test.go runs highlightRank over the same file.
    const cases: Any[] = JSON.parse(readFileSync(join(process.cwd(), '../../impact/testdata/highlight-rank.json'), 'utf8'));
    expect(cases.length).toBeGreaterThan(0);
    const rank = (c: Any) => (c.blocking ? 0 : ({ err: 1, neutral: 2, ok: 3 } as Any)[TONE[verdictOf(c)]]);
    for (const c of cases) expect(rank(c), c.name).toBe(c.rank);
  });
  it('agrees with the Go highlight ranking', () => {
    const L = layoutImpact(twelve());
    const rank = L.consumers.map((c: Any, i: number) => (i === 0 ? 0 : ({ err: 1, neutral: 2, ok: 3 } as Any)[c.tone]));
    expect(rank).toEqual([...rank].sort((a, b) => a - b));
  });
});

describe('classificationLabel and IMPACT_GATE', () => {
  it('words the classification and the gate once', () => {
    expect(classificationLabel('POTENTIAL_BREAKING')).toBe('Potential breaking');
    expect(classificationLabel('')).toBe('Unknown');
    expect(IMPACT_GATE.blocked).toEqual({ cls: 'badge-err', label: 'Blocked', tone: 'err' });
  });
});

describe('layoutImpact', () => {
  it('draws the demo case', () => {
    const L = layoutImpact(demo());
    expect(L.consumers.map((c: Any) => c.key)).toEqual(['orders-service', 'api-gateway']);
    expect(L.consumers.filter((c: Any) => c.ring).map((c: Any) => c.key)).toEqual(['orders-service']);
    expect(L.consumers.every((c: Any) => c.flow)).toBe(true);
    expect(L.consumers.map((c: Any) => c.rise)).toEqual([0, 1]);
    expect(L.more).toBe(2);
    expect(L.sheet.consumers).toBe('4 · 2 direct');
    expect(L.sheet.gate).toEqual({ word: 'blocked', tone: 'err' });
    expect(L.sheet.stamp.text).toBe('✕ BREAKING');
    expect(L.sheet.stamp.land).toBe(true);
    expect(L.leaders.next).toBe(`${FSI}payments-ser…${PDI} ${FSI}2.0.1${PDI}`);
    expect(L.leaders.prev).toBe(`${FSI}payments-ser…${PDI} ${FSI}1.0.0${PDI}`);
    expect([L.W, L.H]).toEqual([600, 589]);
    expect(L.label).toBe('Impact of payments-service 1.0.0 to 2.0.1: breaking. Release gate blocked. 4 consumers, 2 direct. '
      + 'orders-service incompatible, accepts ^1.0.0; api-gateway incompatible, accepts ^1.0.0; 2 more in Affected consumers.');
  });

  it('prints ? for a revision with no version and never repeats the service', () => {
    const L = layoutImpact(impact({
      oldRevision: { kind: 'revision', key: 'p@1', label: 'payments-service', href: '' },
      newRevision: { kind: 'revision', key: 'p@2', label: 'payments-service', href: '' },
    }));
    expect(L.leaders.next).toBe(`${FSI}payments-ser…${PDI} ${FSI}?${PDI}`);
    expect(L.leaders.face).toBe('?');
    expect(L.sheet.change).toBe('? → ?');
  });

  it('rings only under a blocked gate, while err wires still flow', () => {
    for (const gate of ['unverified', 'clear']) {
      const L = layoutImpact({ ...demo(), gate });
      expect(L.consumers.some((c: Any) => c.ring)).toBe(false);
      expect(L.consumers.every((c: Any) => c.flow)).toBe(true);
    }
  });

  it('draws 8 of 12 direct consumers, cutting compatible before unknown', () => {
    const L = layoutImpact(twelve());
    expect(L.consumers).toHaveLength(8);
    const tones = L.consumers.map((c: Any) => c.tone);
    expect(tones.lastIndexOf('err')).toBeLessThan(tones.indexOf('neutral'));
    expect(tones.lastIndexOf('neutral')).toBeLessThan(tones.indexOf('ok'));
    expect(tones.filter((t: string) => t === 'neutral')).toHaveLength(4);
    expect(L.more).toBe(1284 - 8);
    expect(L.consumers[0].hw).toBe(52);
    expect(L.W).toBeLessThanOrEqual(980);
    expect([L.W, L.H]).toEqual([976, 574]);
  });

  it('never reads the page: the drawing and counts ignore items and nextOffset', () => {
    const a = layoutImpact(twelve());
    const b = layoutImpact(twelve({ nextOffset: 200, offset: 100, items: [consumer('zzz', { compatibilityVerdict: 'incompatible' })] }));
    expect(b).toEqual(a);
    expect(a.sheet.consumers).toBe('1,284');
    expect(a.label).toContain('1,284 consumers, 97 direct');
  });

  it('draws nothing under a complete answer with no consumers', () => {
    const L = layoutImpact(impact());
    expect(L.consumers).toEqual([]);
    expect(L.placeholder).toBeNull();
    expect(L.more).toBe(0);
    expect(L.H).toBeLessThan(layoutImpact(demo()).H);
  });

  it('draws one consumers unknown placeholder when the answer is not complete', () => {
    for (const completeness of ['partial', 'empty']) {
      const L = layoutImpact(impact({ completeness }));
      expect(L.consumers).toEqual([]);
      expect(L.placeholder?.label).toBe('consumers unknown');
      expect(L.label).toContain('consumers unknown');
      expect(L.label).toContain(`, evidence ${completeness}`);
      expect(L.sheet.evidence).toEqual({ word: completeness, tone: 'warn' });
    }
  });

  it('counts hidden consumers without drawing them', () => {
    const L = layoutImpact(impact({ consumers: { ...demo().consumers, hidden: 3 } }));
    expect(L.consumers).toHaveLength(2);
    expect(L.more).toBe(2);
    expect(L.hidden).toBe(3);
    expect(L.label).toContain(', 3 you may not see');
  });

  it('draws 4 when the figure is narrow', () => {
    const L = layoutImpact(twelve(), { maxDrawn: 4 });
    expect(L.consumers).toHaveLength(4);
    expect(L.more).toBe(1284 - 4);
    expect(L.W).toBe(776);
  });

  it('says a refused consumer refuses a guarantee, in the table order', () => {
    const L = layoutImpact(impact({ consumers: { total: 1, direct: 1, highlights: [consumer('ledger', { compatibilityVerdict: 'compatible', compatibility: '^1.0.0', unmet: [{ guarantee: 'delivery' }] })] } }));
    const c = L.consumers[0];
    expect(c.verdict).toBe('refused');
    expect(c.tone).toBe('err');
    expect(L.label).toContain('ledger refuses a guarantee it requires, range compatible');
    expect(c.title).toContain('ledger refuses a guarantee it requires, range compatible');
  });

  it('carries a configuration-reference note in the title and the label', () => {
    const note = 'the new revision drops a configuration value it reads';
    const L = layoutImpact(impact({ consumers: { total: 1, direct: 1, highlights: [consumer('cfg', { compatibilityVerdict: 'incompatible', compatibility: '^1.0.0', note })] } }));
    expect(L.consumers[0].title).toContain(note);
    expect(L.label).toContain(`cfg incompatible, ${note}, accepts ^1.0.0`);
  });

  it('adds the domain when two drawn consumers share a name', () => {
    const L = layoutImpact(impact({ consumers: { total: 2, direct: 2, highlights: [
      consumer('a/payments', { label: 'payments', domain: 'a' }),
      consumer('b/payments', { label: 'payments', domain: 'b' }),
    ] } }));
    expect(L.consumers.map((c: Any) => c.name)).toEqual(['payments · a', 'payments · b']);
    expect(L.label).toContain('payments · a unknown');
  });

  it('marks an absent range as no range', () => {
    const L = layoutImpact(impact({ consumers: { total: 1, direct: 1, highlights: [consumer('obs')] } }));
    expect(L.consumers[0].range).toBe('no range');
    expect(L.consumers[0].rangeAbsent).toBe(true);
    expect(L.consumers[0].owner).toBe('no owner');
  });

  it('keeps every label within its budget for 200-character names, owners and ranges', () => {
    const long = (p: string) => p + 'x'.repeat(200 - p.length);
    const budgets: Record<number, [number, number, number]> = { 2: [22, 26, 24], 4: [20, 24, 21], 8: [12, 14, 12] };
    for (const n of [2, 4, 8]) {
      const hl = Array.from({ length: n }, (_, i) => consumer(`k${i}`, { label: long(`n${i}`), owner: long('o'), compatibility: long('^'), compatibilityVerdict: 'compatible' }));
      const L = layoutImpact(impact({
        service: { kind: 'service', key: 's', label: long('s'), href: '' },
        newRevision: { kind: 'revision', key: 'r', label: 'r', version: long('9'), href: '' },
        consumers: { total: n, direct: n, highlights: hl },
      }));
      const [name, owner, chip] = budgets[n];
      for (const c of L.consumers) {
        expect(visible(c.name)).toBeLessThanOrEqual(name);
        expect(visible(c.owner)).toBeLessThanOrEqual(owner);
        expect(visible(c.range)).toBeLessThanOrEqual(chip);
      }
      expect(visible(L.leaders.next)).toBeLessThanOrEqual(13 + 1 + 8);
      expect(visible(L.leaders.face)).toBeLessThanOrEqual(8);
      expect(visible(L.sheet.title)).toBeLessThanOrEqual(14);
      expect(visible(L.sheet.change)).toBeLessThanOrEqual(14);
    }
  });

  it('names every drawn consumer and its verdict in the aria-label', () => {
    const L = layoutImpact(twelve());
    for (const c of L.consumers) expect(L.label).toContain(`${c.fullName} ${c.verdict}`);
    expect(L.label).toContain(`${(1284 - 8).toLocaleString('en-US')} more`);
  });

  it('sizes the stamp for each classification', () => {
    const stamp = (classification: string) => layoutImpact({ ...demo(), classification }).sheet.stamp;
    expect(stamp('POTENTIAL_BREAKING')).toMatchObject({ text: '! POTENTIAL BREAKING', tone: 'warn', land: false, fs: 12.19 });
    expect(stamp('NON_BREAKING')).toMatchObject({ text: '✓ NON BREAKING', tone: 'ok', land: false });
    expect(stamp('')).toMatchObject({ text: '? UNKNOWN', tone: 'neutral' });
    const s = stamp('BREAKING');
    expect(s.fs).toBe(13.13);
    expect(s.w).toBe(Math.round(10 * 13.13 * 0.82) + 20);
    expect(s.x).toBe(Math.max(8, 236 - s.w - 4));
  });
});
