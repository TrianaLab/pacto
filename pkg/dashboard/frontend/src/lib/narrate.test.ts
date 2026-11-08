import { describe, it, expect, beforeEach } from 'vitest';
import { timeline, sessionKey, claimAutoplay, play, finalMode, resolve, NARRATE, finishNarrations, tokenOffsets } from './narrate.ts';

const step = (dur: number, cap = 's') => ({ cap, dur });

describe('timeline (A1, A1b)', () => {
  it('places steps a hold apart and totals them', () => {
    expect(timeline([step(240), step(160), step(240), step(160)])).toEqual({ at: [0, 360, 640, 1000], total: 1160 });
  });
  it('refuses a step over --dur-3, a story over 1200ms and more than 5 steps', () => {
    expect(() => timeline([step(241)])).toThrow(/over 240/);
    expect(() => timeline([step(240), step(240), step(240), step(240), step(240)])).toThrow(/over 1200/);
    expect(() => timeline(Array.from({ length: 6 }, () => step(10)))).toThrow(/6 steps/);
  });
  it('lets a rail walk take one step per station, up to 8', () => {
    const walk = Array.from({ length: 8 }, () => ({ cap: 's', dur: 120, hold: 30 }));
    expect(timeline(walk, { walk: true }).total).toBe(8 * 120 + 7 * 30);
    expect(() => timeline([...walk, walk[0]], { walk: true })).toThrow(/9 steps/);
    expect(NARRATE.max).toBe(1200);
  });
});

describe('the session key', () => {
  beforeEach(() => sessionStorage.clear());
  it('is the same for the same animated fields and changes with them', () => {
    expect(sessionKey({ a: 1 })).toBe(sessionKey({ a: 1 }));
    expect(sessionKey({ a: 1 })).not.toBe(sessionKey({ a: 2 }));
    expect(sessionKey({ a: 1 })).toMatch(/^narr:/);
  });
  it('lets an answer autoplay once per session', () => {
    expect(claimAutoplay('narr:x')).toBe(true);
    expect(claimAutoplay('narr:x')).toBe(false);
    expect(claimAutoplay('narr:y')).toBe(true);
  });
});

describe('play', () => {
  it('reveals each step\'s marks and skips wires it cannot find', () => {
    const stage = document.createElement('div');
    stage.innerHTML = '<span data-m="a"></span>';
    const anims = play(stage, null, [{ cap: 'a', dur: 100, show: ['[data-m="a"]'], sig: ['w'] }], 'x');
    expect(anims).toHaveLength(1);
    expect(() => finishNarrations()).not.toThrow();
  });
  it('starts nothing where the platform cannot animate, so the final frame stands', () => {
    const real = Element.prototype.animate;
    // @ts-expect-error -- a platform without WAAPI
    delete Element.prototype.animate;
    try {
      expect(play(document.createElement('div'), null, [{ cap: 'a', dur: 100 }], 'x')).toEqual([]);
    } finally {
      Element.prototype.animate = real;
    }
  });
  it('walks a token through its slots in one animation, resting where it is rendered', () => {
    const stage = document.createElement('div');
    stage.innerHTML = '<span data-tok="0"></span><span data-tok="1"><span class="tok"></span></span><span data-m="a"></span>';
    const steps = Array.from({ length: 6 }, (_, i) => ({ cap: `s${i}`, dur: 120, hold: 30, show: i ? [] : ['[data-m="a"]'] }));
    const token = { sel: '.tok', slots: steps.map((_, i) => `[data-tok="${Math.min(i, 1)}"]`) };
    expect(() => play(stage, null, steps, 'x')).toThrow(/over 5/);
    const anims = play(stage, null, steps, 'x', { walk: true, token });
    expect(anims).toHaveLength(2);
    expect(play(stage, null, steps, 'x', { walk: true, token: { sel: '.none', slots: token.slots } })).toHaveLength(1);
  });
  it('places a token in each slot relative to where it rests', () => {
    const rest = { left: 300, top: 10 };
    expect(tokenOffsets([{ left: 100, top: 10 }, { left: 200.4, top: 30 }, rest])).toEqual(['translate(-200px, 0px)', 'translate(-100px, 20px)', 'none']);
  });
  it('resolves wires by id and marks by selector', () => {
    const stage = document.createElement('div');
    stage.innerHTML = '<span data-m="a"></span><svg><g><path data-wid="w1"></path><path class="sig" data-wid="w1"></path></g></svg>';
    const g = stage.querySelector('g');
    expect(resolve(stage, g, 'w:w1')).toHaveLength(1);
    expect(resolve(stage, null, 'w:w1')).toEqual([]);
    expect(resolve(stage, g, '[data-m="a"]')).toHaveLength(1);
  });
  it('is final under html[data-narrate="final"]', () => {
    document.documentElement.dataset.narrate = 'final';
    expect(finalMode()).toBe(true);
    delete document.documentElement.dataset.narrate;
    expect(finalMode()).toBe(false);
  });
});
