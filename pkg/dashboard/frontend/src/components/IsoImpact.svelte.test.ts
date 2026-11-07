import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import IsoImpact from './IsoImpact.svelte';
import { layoutImpact } from '../lib/isoImpact.ts';

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- test fixture builder
type Any = any;

const consumer = (key: string, o: Any = {}): Any => ({
  service: { kind: 'service', key, label: key, href: '' },
  path: [], pathTotal: 0, pathTruncated: false, depth: 1, direct: true,
  confidence: 'contractual', compatibilityVerdict: 'unknown', ...o,
});

const impact = (o: Any = {}): Any => {
  const { consumers, ...rest } = o;
  return {
    meta: { completeness: 'complete' },
    service: { kind: 'service', key: 'payments-service', label: 'payments-service', href: '' },
    oldRevision: { kind: 'revision', key: 'p@1', label: 'payments-service 1.0.0', version: '1.0.0', href: '' },
    newRevision: { kind: 'revision', key: 'p@2', label: 'payments-service 2.0.1', version: '2.0.1', href: '' },
    classification: 'BREAKING', gate: 'blocked', gateReason: '',
    consumers: {
      total: 5, direct: 4, count: 0, limit: 100, offset: 0, truncated: false, items: [],
      highlights: [
        consumer('orders-service', { compatibilityVerdict: 'incompatible', compatibility: '^1.0.0', owner: 'commerce', blocking: true }),
        consumer('api-gateway', { compatibilityVerdict: 'incompatible', compatibility: '^1.0.0' }),
        consumer('audit-log'),
        consumer('frontend', { compatibilityVerdict: 'compatible', compatibility: '>=1.0.0' }),
      ],
      ...consumers,
    },
    ...rest,
  };
};

let app: ReturnType<typeof mount> | null = null;
afterEach(() => { if (app) unmount(app); app = null; document.body.innerHTML = ''; });
const render = (props: Record<string, unknown>) => {
  app = mount(IsoImpact, { target: document.body, props });
  flushSync();
  return document.body;
};

describe('IsoImpact', () => {
  it('is one labelled image with a group per drawn consumer', () => {
    const im = impact();
    const b = render({ impact: im });
    const svg = b.querySelector('[data-testid="iso-impact"] svg[role="img"]')!;
    expect(svg.getAttribute('aria-label')).toBe(layoutImpact(im).label);
    expect([...b.querySelectorAll('[data-verdict]')].map((g) => g.getAttribute('data-verdict'))).toEqual(['incompatible', 'incompatible', 'unknown', 'compatible']);
    expect(b.querySelectorAll('.ring')).toHaveLength(1);
    expect(b.querySelector('.stamp')?.textContent).toContain('BREAKING');
  });

  it('moves only what is wrong: err consumers, err wires, the ring and the BREAKING stamp', () => {
    const b = render({ impact: impact() });
    const moving = [...b.querySelectorAll('[data-motion]')];
    expect(moving.length).toBe(2 + 2 + 1 + 1);
    for (const el of moving) {
      const ok = el.matches('.c.tone-err.rise, .wire.tone-err.flow, .ring, .stamp-in.land');
      expect(ok, el.getAttribute('class') ?? '').toBe(true);
    }
    for (const g of b.querySelectorAll('[data-verdict="compatible"], [data-verdict="unknown"]')) {
      expect(g.hasAttribute('data-motion')).toBe(false);
    }
  });

  it('never rings when the gate is not blocked, and calm stamps never land', () => {
    const b = render({ impact: impact({ gate: 'unverified', classification: 'NON_BREAKING' }) });
    expect(b.querySelectorAll('.ring')).toHaveLength(0);
    expect(b.querySelector('.stamp-in.land')).toBeNull();
  });

  it('keeps the full text of every truncated consumer in its title', () => {
    const long = 'x'.repeat(200);
    const b = render({ impact: impact({ consumers: { highlights: [consumer(long, { owner: 'o'.repeat(200) })] } }) });
    const g = b.querySelector('[data-verdict]')!;
    expect(g.querySelector('title')?.textContent).toContain(long);
    expect(g.querySelector('.em')?.textContent?.length).toBeLessThan(40);
  });

  it('draws the neutral placeholder when a partial answer names no consumer', () => {
    const b = render({ impact: impact({ meta: { completeness: 'partial' }, consumers: { total: 0, direct: 0, highlights: [] } }) });
    expect(b.querySelectorAll('[data-verdict]')).toHaveLength(0);
    expect(b.querySelector('[data-testid="iso-impact-placeholder"]')?.textContent).toContain('consumers unknown');
  });

  it('offers the rest in the table and says how many the viewer may not see', () => {
    const onmore = vi.fn();
    const b = render({ impact: impact({ consumers: { hidden: 2 } }), onmore });
    const more = b.querySelector('[data-testid="iso-impact-more"]') as HTMLButtonElement;
    expect(more.textContent?.trim()).toBe('1 more in Affected consumers');
    more.click();
    expect(onmore).toHaveBeenCalledOnce();
    expect(b.querySelector('[data-testid="iso-impact-hidden"]')?.textContent?.trim()).toBe('2 consumers you may not see');
  });

  it('shows no more button without a handler or an overflow', () => {
    const b = render({ impact: impact({ consumers: { total: 4 } }), onmore: () => {} });
    expect(b.querySelector('[data-testid="iso-impact-more"]')).toBeNull();
    expect(b.querySelector('[data-testid="iso-impact-hidden"]')).toBeNull();
  });
});
