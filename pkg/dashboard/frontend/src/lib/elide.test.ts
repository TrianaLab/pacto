import { describe, it, expect, vi } from 'vitest';
import { fitName, fitSiblings, fitHead, elidable, fitIdents } from './elide.ts';

const within = (n: number) => (s: string) => Array.from(s).length <= n;

describe('fitName (C6)', () => {
  it('keeps a name that fits whole', () => {
    expect(fitName('ledger', within(10))).toBe('ledger');
  });
  it('replaces whole middle segments, keeping the separator on both sides', () => {
    const out = fitName('risk-us-eu-west-1-partner-feed-compliance', within(26))!;
    expect(out).toMatch(/^risk-us-.*…-.*compliance$/);
    expect(Array.from(out).length).toBeLessThanOrEqual(26);
  });
  it('keeps at least 4 characters of head and 5 of tail', () => {
    const out = fitName('ab-cdefgh-ijklmnop-qrstuvwx-yz', within(24))!;
    const [head, tail] = out.split('…');
    expect(head.length).toBeGreaterThanOrEqual(4);
    expect(tail.length).toBeGreaterThanOrEqual(5);
    expect(out.startsWith('ab-')).toBe(true);
  });
  it('returns null rather than a fragment when no whole-segment cut fits', () => {
    // A character cut would read `consumer-0-b…wes`: a head ending mid-word, a 3-character tail.
    expect(fitName('consumer-0-backfill-feed-canary-consumer-wes', within(16))).toBeNull();
    expect(fitName('consumer-0-backfill-feed-canary-consumer-wes', within(23))).toBe('consumer-…-consumer-wes');
  });
  it('keeps whole head segments and the last 8 characters of a long last segment', () => {
    expect(fitName('ledger-feed-averyveryverylongsinglewordname', within(21))).toBe('ledger-feed-…wordname');
  });
  it('falls back to head characters and the last segment when no segment cut fits', () => {
    expect(fitName('averyveryverylongsinglewordname', within(14))).toBe('avery…wordname');
    expect(fitName('averyveryverylongsinglewordname', within(3))).toBeNull();
  });
  it('never elides a short name or one with a bidi control', () => {
    expect(elidable('payments-api')).toBe(false);
    expect(elidable('orders-‮reverse‬-sync-bridge')).toBe(false);
    expect(elidable('payments-service-eu')).toBe(true);
  });
});

describe('fitHead (C6 last resort)', () => {
  it('keeps the longest whole-segment head that fits beside a tail of 8 to 5 characters', () => {
    expect(fitHead('fraud-scoring-realtime-event', within(18))).toBe('fraud-…me-event');
    expect(fitHead('fraud-scoring-realtime-event', within(12))).toBe('fraud-…event');
    expect(fitHead('fraud-scoring-realtime-event', within(4))).toBeNull();
    expect(fitHead('averyveryverylongsinglewordname', within(40))).toBeNull();
  });
});

describe('fitSiblings (C6 siblings)', () => {
  it('keeps the segment that tells two colliding names apart, or elides neither', () => {
    const names = ['catalog-service-bridge-eu-west-compliance', 'catalog-service-stream-eu-west-compliance'];
    const out = fitSiblings(names, (_n, s) => Array.from(s).length <= 30);
    const head = (s: string | null) => Array.from(s ?? '').slice(0, 16).join('');
    if (out[0] && out[1] && out[0] !== names[0] && out[1] !== names[1]) expect(head(out[0])).not.toBe(head(out[1]));
    else expect(out.every((o, i) => o === null || o === names[i])).toBe(true);
    expect(out[0]).not.toBe(out[1]);
  });
  it('leaves names that do not collide alone', () => {
    expect(fitSiblings(['a-b', 'c-d'], () => true)).toEqual(['a-b', 'c-d']);
  });
});

describe('fitIdents', () => {
  it('keeps an elided name whole for assistive technology, and undoes it once the name fits', () => {
    vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ font: '', measureText: (s: string) => ({ width: [...s].length * 10 }) } as never);
    const full = 'risk-us-eu-west-1-partner-feed-compliance';
    const root = document.createElement('div');
    root.innerHTML = `<a href="#/x" title="${full}"><bdi class="fit">${full}</bdi></a>`;
    document.body.appendChild(root);
    const bdi = root.querySelector('bdi') as HTMLElement;
    let width = 300;
    Object.defineProperty(bdi, 'getClientRects', { value: () => [{}] });
    Object.defineProperty(bdi, 'clientWidth', { get: () => width });
    Object.defineProperty(bdi, 'scrollWidth', { get: () => (bdi.textContent ?? '').length * 10 });
    fitIdents(root);
    expect(bdi.textContent).toContain('…');
    expect(bdi.getAttribute('aria-hidden')).toBe('true');
    expect(root.querySelector('a .fit-full.visually-hidden')?.textContent).toBe(full);
    width = 1000;
    fitIdents(root);
    expect(bdi.textContent).toBe(full);
    expect(bdi.hasAttribute('aria-hidden')).toBe(false);
    expect(root.querySelector('.fit-full')).toBeNull();
    root.remove();
    vi.restoreAllMocks();
  });
});
