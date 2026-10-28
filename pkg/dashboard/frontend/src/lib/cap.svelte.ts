/**
 * A list inside a page that shows its first few and offers the rest, for the
 * tables and blocks CappedList cannot wrap (a <tbody>, a stack of cards). One
 * long block (3,000 SBOM packages, 1,200 dependents) must never push the rest of
 * the page screens down. Pair it with ShowAll for the button.
 */
export interface Cap {
  readonly all: boolean;
  readonly max: number;
  toggle(): void;
  slice<T>(items: T[] | null | undefined): T[];
}

export function cap(max = 25): Cap {
  let all = $state(false);
  return {
    get all() { return all; },
    max,
    toggle() { all = !all; },
    slice<T>(items: T[] | null | undefined): T[] {
      const xs = items ?? [];
      return all ? xs : xs.slice(0, max);
    },
  };
}
