import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { initTooltipPlacement } from './tooltips.ts';

// Places an element at a given horizontal position and fires a real mouseover so
// the delegated listener runs, then returns the resulting data-tip-align.
function alignAfterHover(left: number, width: number, viewport = 1000): string | null {
  Object.defineProperty(window, 'innerWidth', { value: viewport, configurable: true });
  const el = document.createElement('span');
  el.setAttribute('data-tip', 'a tip');
  el.getBoundingClientRect = () => ({ left, width, right: left + width, top: 0, bottom: 0, height: 0, x: left, y: 0, toJSON: () => {} });
  document.body.appendChild(el);
  el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
  return el.getAttribute('data-tip-align');
}

describe('initTooltipPlacement — edge-aware alignment', () => {
  let teardown: () => void;

  beforeEach(() => { teardown = initTooltipPlacement(); });
  afterEach(() => { teardown(); document.body.innerHTML = ''; });

  it('anchors right when the host is near the right edge', () => {
    expect(alignAfterHover(970, 20)).toBe('right'); // center 980, +160 > 992
  });

  it('anchors left when the host is near the left edge', () => {
    expect(alignAfterHover(0, 20)).toBe('left'); // center 10, -160 < 8
  });

  it('leaves centered tooltips unaligned in the middle', () => {
    expect(alignAfterHover(490, 20)).toBeNull();
  });

  it('does not clobber a manually-set alignment', () => {
    Object.defineProperty(window, 'innerWidth', { value: 1000, configurable: true });
    const el = document.createElement('span');
    el.setAttribute('data-tip', 'x');
    el.setAttribute('data-tip-align', 'right');
    el.getBoundingClientRect = () => ({ left: 0, width: 20, right: 20, top: 0, bottom: 0, height: 0, x: 0, y: 0, toJSON: () => {} });
    document.body.appendChild(el);
    el.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }));
    expect(el.getAttribute('data-tip-align')).toBe('right'); // manual value kept, not flipped to 'left'
  });
});
