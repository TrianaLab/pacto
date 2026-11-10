import { describe, it, expect } from 'vitest';
import { spills } from './figureLayout.ts';

// jsdom lays nothing out, so each box states the widths a browser would measure. The
// stage joins the document because a disconnected element has no computed style.
function box(html: string, sizes: Record<string, [number, number]>) {
  const stage = document.createElement('div');
  stage.innerHTML = html;
  document.body.replaceChildren(stage);
  for (const [sel, [client, scroll]] of Object.entries(sizes)) {
    const el = stage.querySelector(sel)!;
    Object.defineProperty(el, 'clientWidth', { value: client });
    Object.defineProperty(el, 'scrollWidth', { value: scroll });
  }
  return stage;
}

describe('spills', () => {
  it('finds a box whose text runs past its width', () => {
    expect(spills(box('<div class="lane"><span class="n">45</span></div>', { '.lane': [200, 260] }))).toBe(true);
    expect(spills(box('<div class="lane"></div>', { '.lane': [200, 201] }))).toBe(false);
  });
  it('ignores an elided identity, a scroller, a hidden label and inline text', () => {
    const stage = box(
      '<bdi class="e" style="text-overflow: ellipsis; overflow: hidden; display: block"></bdi><div class="s" style="overflow-x: auto"></div>'
      + '<span class="h" style="position: absolute"></span><span class="i" style="display: inline"></span><div class="z"></div>',
      { '.e': [100, 300], '.s': [100, 300], '.h': [1, 40], '.i': [100, 300], '.z': [0, 300] },
    );
    expect(spills(stage)).toBe(false);
  });
});
