// The one seam the graph acceptance uses to change a backend answer. The demo runs the
// real engine in wasm and boot.js shims window.fetch to call it, so Playwright's network
// interception never sees an API call; every request goes through `window.__pactoServe`.
// An init script installs an accessor for it BEFORE the wasm runtime assigns it, so a test
// can post-process the real engine's real answer. Set `window.__pactoMutate` to pick what
// changes in the neighborhood response: 'semantic' (same topology, different truth),
// 'addNode' (one new provider beside the focus) or 'many' (sixty entities two hops out).

/** Amends the ONE neighborhood response under test, in the page, before the app parses it. */
export function installServeInterceptor(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  const w = window as unknown as { __pactoMutate?: string };
  let real: Serve | null = null;
  const NEW = 'synthetic-probe-service';
  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    const mode = w.__pactoMutate;
    if (!mode || !path.startsWith('/api/fleet/neighborhood') || res.status !== 200) return res;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let doc: any;
    try { doc = JSON.parse(res.body); } catch { return res; }
    if (!doc || !Array.isArray(doc.nodes) || !doc.nodes.length) return res;
    if (mode === 'semantic') {
      // Same topology, different truth: statuses, labels and reconciliation verdicts move.
      for (const n of doc.nodes) {
        if (n.focus) continue;
        n.status = 'NonCompliant';
        // A PREFIX, not a suffix: a canvas label over 18 characters keeps its first nine
        // and last eight around an ellipsis, so the nine letters of "rechecked" always show.
        n.ref.label = `rechecked-${n.ref.label || n.ref.key}`;
      }
      for (const e of doc.edges || []) {
        if (e.relation === 'runs') continue;
        if (e.serviceCorroboration) e.serviceCorroboration = 'expected-not-observed';
        e.difference = 'observed-not-expected';
      }
    } else if (mode === 'many' && !doc.nodes.some((n: { ref: { key: string } }) => n.ref.key === 'far-0')) {
      // A large neighborhood: sixty entities two hops out, four teams of fifteen, all
      // calling a service that uses the focus. Three of those calls were never declared.
      const focus = doc.nodes.find((n: { focus?: boolean }) => n.focus) || doc.nodes[0];
      const user = (doc.edges || []).find((e: { to: { key: string } }) => e.to.key === focus.ref.key)?.from;
      if (!user) return res;
      doc.edges = doc.edges || [];
      for (let i = 0; i < 60; i++) {
        const ref = { kind: 'service', key: `far-${i}`, label: `far-${i}`, href: `/fleet/services/far-${i}` };
        doc.nodes.push({ ref, depth: 2, status: 'Compliant', owner: `team-${i % 4}` });
        doc.edges.push({
          id: `far-${i}->${user.key}`, from: ref, to: user, relation: 'dependency',
          expected: i >= 3, observed: true, provenance: 'observed', difference: i < 3 ? 'observed-not-expected' : 'matched',
          declaredClaims: { total: 0, count: 0, truncated: false },
          observationSources: { total: 0, count: 0, truncated: false },
        });
      }
    } else if (mode === 'addNode' && !doc.nodes.some((n: { ref: { key: string } }) => n.ref.key === NEW)) {
      // Topology change: one genuinely new provider appears next to the focus.
      const focus = doc.nodes.find((n: { focus?: boolean }) => n.focus) || doc.nodes[0];
      const ref = { kind: 'service', key: NEW, label: NEW, href: `/fleet/services/${NEW}` };
      doc.nodes.push({ ref, depth: 1, status: 'Unknown' });
      doc.edges = doc.edges || [];
      doc.edges.push({
        id: `${focus.ref.key}->${NEW}`, from: focus.ref, to: ref, relation: 'dependency',
        expected: true, observed: false, provenance: 'declared', difference: 'expected-not-observed',
        declaredClaims: { total: 0, count: 0, truncated: false },
        observationSources: { total: 0, count: 0, truncated: false },
      });
    }
    return { status: res.status, body: JSON.stringify(doc), contentType: res.contentType };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}
