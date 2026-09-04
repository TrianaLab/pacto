/**
 * jsdom has no Web Animations API and no matchMedia. Svelte's transitions and
 * animate:flip call element.animate()/getAnimations(), and svelte/motion's
 * prefersReducedMotion calls matchMedia, so without these three stubs any
 * component carrying motion throws in the unit run.
 *
 * The stubs are inert on purpose -- they report "already finished", so a test
 * observes the RESTING DOM. Motion behaviour is proven in a real browser
 * (the Playwright specs in e2e/), never here.
 */
class FinishedAnimation {
  currentTime = 0;
  startTime = 0;
  playbackRate = 1;
  playState = 'finished';
  onfinish: (() => void) | null = null;
  oncancel: (() => void) | null = null;
  finished = Promise.resolve(this);
  effect = { getComputedTiming: () => ({ duration: 0 }), getTiming: () => ({ duration: 0 }) };
  cancel() { this.oncancel?.(); }
  finish() { this.onfinish?.(); }
  play() {}
  pause() {}
  reverse() {}
  commitStyles() {}
  persist() {}
  addEventListener() {}
  removeEventListener() {}
}

if (!Element.prototype.animate) {
  Element.prototype.animate = function () { return new FinishedAnimation() as unknown as Animation; };
}
if (!Element.prototype.getAnimations) {
  Element.prototype.getAnimations = function () { return []; };
}
if (!window.matchMedia) {
  window.matchMedia = ((q: string) => ({
    matches: false,
    media: q,
    onchange: null,
    addEventListener() {},
    removeEventListener() {},
    addListener() {},
    removeListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
}
