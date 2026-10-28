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

/** sessionApi serves a test's session fixture through the two reads the session
 *  store makes: /api/session (the fixture without its delivery caller) and
 *  /api/delivery/caller (the fixture's caller, or a 404 where it has none). */
export function sessionApi(
  read: () => Promise<Record<string, unknown> | null | undefined>,
  NotFound: new (status: number, message: string) => Error,
) {
  return {
    session: async () => {
      const { caller: _c, requireOwnerTeam: _r, ...s } = (await read()) ?? {};
      return s;
    },
    deliveryCaller: async () => {
      const s = await read();
      if (!s?.caller) throw new NotFound(404, 'delivery is not served');
      return { requireOwnerTeam: !!s.requireOwnerTeam, ...(s.caller as object) };
    },
  };
}
