// Test-only helpers compiled in runes mode (the `.svelte.ts` suffix), so a plain
// `*.test.ts` spec can obtain a reactive props object and toggle a mounted
// component's props (e.g. EntitySearch's `open`) to drive open/close transitions.
// Not shipped: imported only by test specs.

/** reactiveProps wraps an initial props object in `$state` so mutating a property
 *  after mount() propagates into the mounted component. */
export function reactiveProps<T extends object>(initial: T): T {
  const s = $state(initial);
  return s;
}
