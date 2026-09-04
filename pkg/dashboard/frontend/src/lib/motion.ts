/**
 * The JS side of the motion system. CSS owns the durations (styles/tokens.css); this
 * module exists only for the motion a stylesheet cannot express -- a d3 transition, a
 * Svelte `animate:flip`, a setTimeout-driven class.
 *
 * One clock. Every JS caller asks this question here rather than writing its own
 * matchMedia string, because a second copy is how a reduced-motion hole gets shipped:
 * the string is easy to typo and the miss is invisible to anyone not using the setting.
 *
 * It lives here, not in chartkit.ts, because chartkit imports d3 at module scope and the
 * navbar is on every page -- asking "does this reader want less motion?" must not pull a
 * charting library into the entry chunk. chartkit re-exports it for its own callers.
 */
export function prefersReducedMotion(): boolean {
  return typeof matchMedia !== 'undefined' && matchMedia('(prefers-reduced-motion: reduce)').matches;
}

/**
 * The same three durations as --dur-1/2/3 in styles/tokens.css, in milliseconds.
 *
 * A Svelte transition takes a number, not a custom property, so this is the ONE place
 * the ramp crosses from CSS into JS. Nothing above `reveal` ships; a fourth entry here
 * is the ramp being re-invented, which is what --dur- in architecture.test.ts guards
 * against on the CSS side.
 */
export const MOTION = { feedback: 90, row: 160, reveal: 240 } as const;

/** Every JS duration goes through here, so reduced motion is one branch, not N. */
export function dur(ms: number): number {
  return prefersReducedMotion() ? 0 : ms;
}

/** --stagger-step / --stagger-max, mirrored. See the RATION note in styles/tokens.css. */
export const STAGGER_STEP = 28;
export const STAGGER_MAX = 8;

/**
 * The delay for row `i` of a rationed entrance, clamped BEFORE multiplying: row 30 waits
 * the same 224ms as row 8, so a full page costs 8 x 28ms + --dur-3 = 464ms however long
 * it is. Unclamped, 23 rows is 644ms of theatre and a flaky contrast audit.
 */
export function stagger(i: number): number {
  return dur(Math.min(Math.max(0, i), STAGGER_MAX) * STAGGER_STEP);
}
