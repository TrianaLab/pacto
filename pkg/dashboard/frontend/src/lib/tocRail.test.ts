import { describe, it, expect } from 'vitest';
import { railMaxHeight, railToFold, revealScrollTop } from './tocRail.ts';

describe('railMaxHeight', () => {
  it('lets the list run from its top to the fold, less a gap', () => {
    expect(railMaxHeight(720, 300, 16)).toBe(404);
  });
  it('never shrinks the list below a few rows, even when it starts near the fold', () => {
    expect(railMaxHeight(720, 700, 16)).toBe(120);
    expect(railMaxHeight(720, 900, 16, 90)).toBe(90);
  });
});

describe('revealScrollTop', () => {
  // A list 100px tall, scrolled 50px down, holding 30px rows.
  it('leaves the list alone when the entry is already in view', () => {
    expect(revealScrollTop(50, 100, 60, 30)).toBe(50);
    expect(revealScrollTop(50, 100, 50, 30)).toBe(50);
    expect(revealScrollTop(50, 100, 120, 30)).toBe(50);
  });
  it('scrolls up just far enough to show an entry above the view', () => {
    expect(revealScrollTop(50, 100, 20, 30)).toBe(20);
  });
  it('scrolls down just far enough to show an entry below the view', () => {
    expect(revealScrollTop(50, 100, 150, 30)).toBe(80);
  });
  it('shows the top of an entry taller than the list', () => {
    expect(revealScrollTop(0, 100, 200, 300)).toBe(200);
  });
});

describe('railToFold', () => {
  const frame = () => new Promise((r) => requestAnimationFrame(() => r(null)));
  function rail(top: number) {
    const nav = document.createElement('nav');
    nav.innerHTML = '<button>A</button><button>B</button><button>C</button>';
    nav.getBoundingClientRect = () => ({ top }) as DOMRect;
    Object.defineProperty(nav, 'clientHeight', { value: 60, configurable: true });
    [...nav.children].forEach((b, i) => {
      Object.defineProperty(b, 'offsetTop', { value: i * 40, configurable: true });
      Object.defineProperty(b, 'offsetHeight', { value: 40, configurable: true });
    });
    document.body.appendChild(nav);
    return nav;
  }

  it('caps a sticky rail at the fold and follows it as the page scrolls', async () => {
    const nav = rail(300);
    nav.style.position = 'sticky';
    const a = railToFold(nav);
    expect(nav.style.maxHeight).toBe(`${window.innerHeight - 316}px`);
    nav.getBoundingClientRect = () => ({ top: 70 }) as DOMRect;
    window.dispatchEvent(new Event('scroll'));
    window.dispatchEvent(new Event('resize'));
    await frame();
    expect(nav.style.maxHeight).toBe(`${window.innerHeight - 86}px`);
    a.destroy();
    nav.remove();
  });

  it('leaves a rail in the page flow at its natural height', () => {
    const nav = rail(300);
    const a = railToFold(nav);
    expect(nav.style.maxHeight).toBe('');
    a.destroy();
    nav.remove();
  });

  it('scrolls the rail alone to keep the current entry in view', () => {
    const nav = rail(0);
    const a = railToFold(nav, 'A');
    expect(nav.scrollTop).toBe(0);
    nav.children[2].setAttribute('aria-current', 'true');
    a.update();
    expect(nav.scrollTop).toBe(60);
    a.destroy();
    nav.remove();
  });

  it('cancels a pending frame when the rail goes away', () => {
    const nav = rail(0);
    const a = railToFold(nav);
    window.dispatchEvent(new Event('scroll'));
    expect(() => a.destroy()).not.toThrow();
    nav.remove();
  });
});
