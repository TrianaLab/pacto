import { expect, type Page } from '@playwright/test';
import { settle } from './ready';

// G1, the transversal layout gate, and the scale fixture it runs on.
//
// `layoutGate.ts`, not `*.spec.ts`, so Playwright does not collect it. layout.spec.ts
// drives every route through it; any spec may call `layoutProblems` on a page it
// already opened.
//
// The fixture amends the real engine's answers in the page, through
// `window.__pactoServe` (the one function boot.js routes every API call through;
// Playwright's interception never sees a wasm-served fetch). It turns the 38-service
// demo into the population the spec's figures must survive: 5,000 services, 300 owners,
// 300 domains, 50 sources, 1,284 consumers (412 direct, 8 highlights, 3 hidden,
// partial), 25 providers, 60 delivery environments (waiting in the inbox and counted
// by state), a history timeline, autonomy levels and published versions in the
// thousands, and
// identities of 60 to 200 characters including an emoji and a right-to-left override.
// Keys and hrefs never change, so every link still lands.

/** Installs the scale fixture. Pass to `page.addInitScript` before the first load. */
export function installScaleFixture(): void {
  interface ServeResult { status: number; body: string; contentType: string }
  type Serve = (method: string, path: string, body: string | null) => Promise<ServeResult>;
  let real: Serve | null = null;

  const WORDS = ['settlement', 'reconciliation', 'ledger', 'export', 'worker', 'eu', 'west', 'partner', 'feed',
    'compliance', 'archive', 'backfill', 'canary', 'bridge', 'stream', 'projection', 'read', 'model', 'consumer'];
  const hash = (s: string) => { let h = 2166136261; for (const c of s) h = Math.imul(h ^ c.codePointAt(0)!, 16777619) >>> 0; return h; };
  const memo = new Map<string, string>();
  // Deterministic per name, so the same identity is the same long name on every page.
  const longName = (s: string): string => {
    if (!s || memo.has(s)) return memo.get(s) ?? s;
    const h = hash(s);
    const target = 60 + (h % 141); // 60..200 code points
    let out = s;
    if (h % 7 === 0) out += '-\u{1F6D2}-edge';
    if (h % 11 === 0) out += '-‮reverse‬-sync';
    let k = h;
    while ([...out].length < target) { out += '-' + WORDS[k % WORDS.length]; k = (Math.imul(k, 31) + 7) >>> 0; }
    out = [...out].slice(0, target).join('').replace(/[-_.:/]+$/, '');
    memo.set(s, out);
    return out;
  };
  const ENTITY_KINDS = new Set(['service', 'revision', 'target', 'owner', 'domain', 'source', 'offering']);
  // Every entity reference carries its display label beside its key: lengthen the label,
  // keep the key. Owner names printed beside a service are lengthened the same way, and
  // so are the context bits a reference shows beside its name (its domain, its parent
  // service), except on a page's own entity, whose domain the page links by value.
  const walk = (o: unknown, field = ''): void => {
    if (Array.isArray(o)) { o.forEach((v) => walk(v)); return; }
    if (!o || typeof o !== 'object') return;
    const r = o as Record<string, unknown>;
    if (typeof r.label === 'string' && typeof r.key === 'string' && ENTITY_KINDS.has(String(r.kind))) {
      r.label = r.kind === 'revision' ? r.label : longName(r.label);
      if (field !== 'entity') {
        for (const k of ['businessDomain', 'parentService']) if (typeof r[k] === 'string') r[k] = longName(r[k] as string);
      }
    }
    if (typeof r.owner === 'string' && r.kind === 'service') r.owner = longName(r.owner);
    for (const [k, v] of Object.entries(r)) walk(v, k);
  };
  const split = (total: number, weights: number[]) => {
    const sum = weights.reduce((a, b) => a + b, 0) || 1;
    const out = weights.map((w) => Math.floor((total * w) / sum));
    out[weights.indexOf(Math.max(...weights))] += total - out.reduce((a, b) => a + b, 0);
    return out;
  };
  const scaleTally = (t: Record<string, number> | undefined, total: number) => {
    if (!t) return t;
    const keys = Object.keys(t).filter((k) => typeof t[k] === 'number');
    const parts = split(total, keys.map((k) => t[k]));
    keys.forEach((k, i) => { t[k] = parts[i]; });
    return t;
  };
  const SOURCE_COUNTS = { total: 50, available: 44, partial: 2, stale: 2, unavailable: 2 };
  const page = (doc: Record<string, unknown>, total: number) => {
    const count = Number(doc.count ?? 0), offset = Number(doc.offset ?? 0);
    doc.total = total;
    doc.truncated = offset + count < total;
    if (doc.truncated) doc.nextOffset = offset + count;
  };
  const LIST_TOTAL: Record<string, number> = { service: 5000, owner: 300, domain: 300, source: 50 };
  const NC_OWNERS: [string, number, number][] = [['team:load-owner-a', 40, 297], ['team:load-owner-b', 31, 60], ['team:load-owner-c', 22, 30], ['team:load-owner-d', 12, 400]];
  const NC_DOMAINS: [string, number, number][] = [[longName('load-domain-a'), 45, 310], [longName('load-domain-b'), 30, 90], [longName('load-domain-c'), 25, 40], [longName('load-domain-d'), 15, 700]];
  const NC_LISTS = new Map<string, number>([...NC_OWNERS, ...NC_DOMAINS].map(([k, n]) => [k, n] as [string, number])
    .concat([['-unidentified', 84], ['(none)', 40], ['(unknown)', 14]]));
  // The origin tally at fleet scale (B5): 5,000 services across 14 golden paths with
  // names up to 200 characters, 80 whose origin resolves to none and 3 hidden.
  const ORIGIN_PATHS: [string, number, number][] = [['load/path-a', 1800, 120], ['load/path-b', 900, 40], ['load/path-c', 320, 50]];
  const ORIGIN_LISTS = new Map<string, number>([...ORIGIN_PATHS.map(([k, n]) => [k, n] as [string, number]), ['(unresolved)', 80], ['(none)', 1300]]);

  const amend = (method: string, path: string, doc: Record<string, unknown>, body: string | null) => {
    const [route, query = ''] = path.split('?');
    const q = new URLSearchParams(query);
    const meta = doc.meta as Record<string, unknown> | undefined;
    if (meta && meta.sourceCounts) meta.sourceCounts = { ...SOURCE_COUNTS };
    // A narrowed list (a domain, an owner, a status, a service, an offering's consumers)
    // keeps the engine's total: the fixture leaves the tallies that link to it (an
    // offering's phases, the non-compliant rankings) alone.
    const narrowed = ['businessDomain', 'service', 'consumes', 'ownerKey', 'owner', 'status', 'ownership', 'sourceHealth', 'text', 'createdFrom'].some((k) => q.get(k));
    // The golden path list keeps the engine's paths: a fleet has a handful.
    if (route === '/api/fleet/entities' && Number(q.get('limit') || 25) > 1 && !narrowed && q.get('template') !== 'only') {
      const total = LIST_TOTAL[q.get('kinds') || ''];
      if (total) {
        page(doc, total);
        const ag = doc.aggregate as Record<string, unknown> | undefined;
        if (ag && q.get('kinds') === 'service') {
          ag.matched = total; ag.services = total;
          scaleTally(ag.serviceCompliance as Record<string, number>, total);
          scaleTally(ag.ownership as Record<string, number>, total);
          ag.distinctOwners = 300; ag.rankedOwners = 300;
        } else if (ag) {
          ag.matched = total;
        }
      }
    }
    // The non-compliant rankings at fleet scale (389 of 5,000 across 300 owners and
    // domains, names up to 200 characters), and every list a lane links to holds exactly
    // what the lane counts.
    if (route === '/api/fleet/entities' && q.get('kinds') === 'service') {
      const ag = doc.aggregate as Record<string, unknown> | undefined;
      if (ag && q.get('limit') === '1' && !narrowed) {
        scaleTally(ag.ownership as Record<string, number>, 5000);
        Object.assign(ag, {
          services: 5000, matched: 5000, serviceCompliance: { ...(ag.serviceCompliance as object), nonCompliant: 389 },
          nonCompliantByOwner: NC_OWNERS.map(([key, n, size]) => ({ key, label: longName(key.slice(5)), kind: 'team', services: size, nonCompliant: n })),
          nonCompliantBeyondRanking: 389 - 105 - 84, nonCompliantUnidentified: 84, nonCompliantOwners: 120,
          nonCompliantByDomain: NC_DOMAINS.map(([domain, n, size]) => ({ domain, services: size, nonCompliant: n })),
          nonCompliantDomainsBeyond: 389 - 115 - 40 - 14, nonCompliantDomains: 140, nonCompliantNoDomain: 40, nonCompliantDomainUnknown: 14,
          origins: {
            byPath: ORIGIN_PATHS.map(([key, n, nc]) => ({ key, label: longName(key.slice(5)), services: n, nonCompliant: nc, hidden: key.endsWith('a') ? 3 : 0 })),
            paths: 14, beyond: { services: 600, nonCompliant: 30 }, unresolved: { services: 80, nonCompliant: 9 }, none: { services: 1300, nonCompliant: 140 }, hidden: 3,
          },
        });
      }
      const from = q.get('createdFrom') || '';
      const listed = !q.get('status') && ORIGIN_LISTS.get(from);
      if (listed) {
        // The made-up paths created nothing the engine lists: stand-ins fill the page.
        const ents = doc.entities as Record<string, unknown>[];
        if (!ents.length && q.get('limit') !== '1') {
          const lim = Math.min(listed, Number(q.get('limit') || 25));
          doc.entities = Array.from({ length: lim }, (_, i) => ({ kind: 'service', key: `load/from-${i}`, label: `from-${i}`, status: 'Compliant', href: `/fleet/services/load%2Ffrom-${i}` }));
          doc.count = lim;
        }
        if (q.get('limit') !== '1') page(doc, listed);
      }
      if (q.get('status') === 'NonCompliant') {
        const n = NC_LISTS.get(q.get('ownerKey') || q.get('businessDomain') || (q.get('ownership') === 'unidentified' ? '-unidentified' : ''));
        if (n != null) {
          // The ranking's groups are made up, so the engine lists none of their services:
          // the page is filled with stand-ins and counts what the lane counted.
          const ents = doc.entities as Record<string, unknown>[];
          if (!ents.length) {
            const lim = Math.min(n, Number(q.get('limit') || 25));
            doc.entities = Array.from({ length: lim }, (_, i) => ({ kind: 'service', key: `load/nc-${i}`, label: `nc-${i}`, status: 'NonCompliant', href: `/fleet/services/load%2Fnc-${i}` }));
            doc.count = lim;
          }
          page(doc, n);
        }
      }
    }
    if (route === '/api/fleet/overview') {
      const s = doc.summary as Record<string, unknown>;
      s.services = 5000; s.servicesNeedingAttention = 389; s.domains = 300;
      scaleTally(s.ownership as Record<string, number>, 5000);
      s.degradedSources = 2; s.staleSources = 2; s.unavailableSources = 2;
      const at = doc.attention as Record<string, unknown>;
      if (at) at.total = 2400;
    }
    if (route === '/api/fleet/attention') {
      page(doc, 2400);
      scaleTally(doc.severities as Record<string, number>, 2400);
      const cats = doc.categories as { count: number }[] | undefined;
      if (cats) { const parts = split(2400, cats.map((c) => c.count)); cats.forEach((c, i) => { c.count = parts[i]; }); }
    }
    if (route === '/api/fleet/entities/offering') {
      const off = doc.offering as Record<string, Record<string, unknown>>;
      const prov = off?.providers as { items: Record<string, unknown>[]; total: number; count: number; truncated: boolean } | undefined;
      if (prov && prov.items.length) {
        const base = prov.items[0];
        const svc = base.service as Record<string, unknown>;
        prov.items = Array.from({ length: Math.min(10, 25) }, (_, i) => i === 0 ? base : ({
          ...base, service: { ...svc, key: `${svc.key}-p${i}`, label: `${svc.label}-provider-${i}`, href: `${svc.href}-p${i}` },
        }));
        prov.total = 25; prov.count = prov.items.length; prov.truncated = true;
      }
    }
    // The inbox figure at fleet scale: 60 environments with long names, a declared chain
    // through some of them and counts in the thousands. Only the figure's tallies change;
    // the lists stay the engine's, and the figure caps itself at seven stations.
    if (route === '/api/delivery/inbox') {
      doc.byEnvironment = Array.from({ length: 60 }, (_, i) => {
        const environment = longName(`prod-${i}`);
        return { environment, errored: (i * 37) % 1300, held: (i * 53) % 900, frozen: i % 4, upstream: i ? [longName(`prod-${i - 1}`)] : [] };
      });
    }
    // The environments figure at fleet scale: 60 environments with long names and a
    // declared chain through them, Stages in the thousands; the board stays the engine's.
    if (route === '/api/delivery/environments') {
      const counts = doc.counts as Record<string, unknown> | undefined;
      if (counts) {
        counts.byEnvironment = Array.from({ length: 60 }, (_, i) => ({
          environment: longName(`prod-${i}`), tier: 'prod', stages: 900, failed: (i * 37) % 120, frozen: i % 4, autoHold: i % 3, waiting: (i * 53) % 90,
          unverified: 40, empty: 2, verified: 900 - 40 - 2 - ((i * 37) % 120) - (i % 4) - (i % 3) - ((i * 53) % 90),
          upstream: i ? [longName(`prod-${i - 1}`)] : [],
        }));
      }
    }
    if (route === '/api/delivery/history') {
      const tl = doc.timeline as { buckets: Record<string, number>[]; totals: Record<string, number> } | undefined;
      if (tl) {
        tl.buckets.forEach((b, i) => { b.released = 1200 + i * 37; b.failed = (i * 13) % 40; b.held = (i * 7) % 25; b.other = 300; });
        for (const k of ['released', 'failed', 'held', 'other']) tl.totals[k] = tl.buckets.reduce((n, b) => n + b[k], 0);
      }
    }
    // The publish figure at fleet scale: thousands of services and versions, and the
    // unsigned lane's four chips named 60 to 200 characters long. Only the chip names
    // are lengthened (no route lists them), and the list totals follow the lanes, so
    // "N more" still lands on N plus the chips.
    if (route === '/api/delivery/published') {
      const c = doc.counts as Record<string, unknown> | undefined;
      if (c) {
        const names = (c.firstUnsigned as string[] | undefined) ?? [];
        Object.assign(c, { services: 5000, unsignedServices: 1800, versions: 41000, signed: 38000, unsigned: 3000,
          firstUnsigned: Array.from({ length: 4 }, (_, i) => longName(names[i] ?? `unsigned-${i}`)) });
        const pg = doc.page as { total: number; offset: number; limit: number; nextOffset?: number };
        pg.total = q.get('signed') === 'no' ? 1800 : q.get('signed') === 'yes' ? 3200 : 5000;
        if (pg.offset + pg.limit < pg.total) pg.nextOffset = pg.offset + pg.limit;
      }
    }
    if (route === '/api/delivery/autonomy') {
      doc.byLevel = [{ level: 'observe', stages: 1200 }, { level: 'advise', stages: 2300 }, { level: 'auto-nonbreaking', stages: 1100 }, { level: 'autonomous', stages: 380 }, { level: '', stages: 20 }];
    }
    if (method === 'POST' && route === '/api/fleet/impact') {
      const cs = doc.consumers as Record<string, unknown>;
      const names = [
        'fraud-scoring-realtime-event-stream-projection-read-model-consumer-for-partner-feed-compliance-archive-backfill-eu-west-1-canary-blue-legacy-bridge-settlement-reconciliation-batch-processor-us-east-2',
        'checkout-\u{1F6D2}-edge-adapter-europe-west',
        'orders-‮reverse‬-sync-bridge',
        ...Array.from({ length: 9 }, (_, i) => longName(`consumer-${i}`)),
      ];
      const V = ['incompatible', 'unknown', 'compatible'];
      const consumer = (name: string, i: number) => {
        const key = `domain-load/consumer-${i}`;
        return {
          service: { kind: 'service', key, label: name, domain: 'domain-load', href: `/fleet/services/${encodeURIComponent(key)}` },
          direct: true, depth: 1, confidence: 'declared', compatibilityVerdict: V[i % 3], compatibility: '^1.0.0',
          blocking: i === 0, owner: longName(`team-${i}`), path: [], pathTotal: 0, pathTruncated: false,
        };
      };
      const all = names.map(consumer);
      // One consistent population: byVerdict judges the range alone (6 carry a verdict
      // this engine does not know); the 5 refused accept the range, so the outcome
      // tally counts them with the incompatible: 14 + 5 = 19.
      const byOutcome = [{ key: 'incompatible', count: 19 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1207 }, { key: 'unclassified', count: 6 }];
      let outcome = '';
      try { outcome = String(JSON.parse(body ?? '{}').outcome ?? ''); } catch { /* no body */ }
      const listed = outcome ? all.filter((c) => c.compatibilityVerdict === outcome) : all;
      Object.assign(cs, {
        items: listed.slice(0, 10), highlights: all.slice(0, 8), total: 1284, direct: 412, count: Math.min(10, listed.length), offset: 0, limit: 10,
        nextOffset: 10, truncated: true, hidden: 3, refused: 5,
        outcome: outcome || undefined, matching: outcome ? byOutcome.find((b) => b.key === outcome)!.count : 1284,
        byVerdict: [{ key: 'incompatible', count: 14 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1212 }, { key: 'novel', count: 6 }],
        byOutcome,
        byOwner: Array.from({ length: 5 }, (_, i) => ({ owner: longName(`team-${i}`), consumers: 200 - i * 20, incompatible: 9 - i })),
        beyondRanking: 400, noOwner: 84, owners: 300,
      });
      if (meta) meta.completeness = 'partial';
    }
    walk(doc);
  };

  const wrapped: Serve = async (method, path, body) => {
    const res = await (real as Serve)(method, path, body);
    if (res.status !== 200 || !(res.contentType || '').includes('json')) return res;
    let doc: Record<string, unknown>;
    try { doc = JSON.parse(res.body); } catch { return res; }
    try { amend(method, path, doc, body); } catch (e) { console.error('scale fixture', path, e); return res; }
    return { ...res, body: JSON.stringify(doc) };
  };
  Object.defineProperty(window, '__pactoServe', {
    configurable: true,
    get() { return real ? wrapped : undefined; },
    set(v: Serve) { real = v; },
  });
}

/** Forces the wide font CI renders (DejaVu stands behind Verdana) and the final frame. */
export async function gateMode(page: Page): Promise<void> {
  await page.addStyleTag({ content: '*{font-family: Verdana, "DejaVu Sans", sans-serif !important}' });
  await page.evaluate(() => { document.documentElement.dataset.narrate = 'final'; });
}

/**
 * layoutProblems runs every G1 check on the page as it is and returns one line per
 * problem. Nothing is excluded.
 */
export async function layoutProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = [];
    const root = document.querySelector('#app') ?? document.body;
    const say = (el: Element | null) => {
      if (!el) return '?';
      const id = el.id ? `#${el.id}` : '';
      const cls = typeof el.className === 'string' && el.className ? '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.') : '';
      const t = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 32);
      return `<${el.tagName.toLowerCase()}${id}${cls}> "${t}"`;
    };
    const hiddenByA11y = (el: Element) => {
      for (let p: Element | null = el; p && p !== document.body; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.display === 'none' || cs.visibility === 'hidden' || Number(cs.opacity) === 0) return true;
        if (cs.position === 'absolute' && (cs.clip === 'rect(0px, 0px, 0px, 0px)' || cs.clipPath === 'inset(50%)')) return true;
        if (p.tagName === 'DETAILS' && !(p as HTMLDetailsElement).open && p !== el.closest('summary')?.parentElement) {
          if (!el.closest('summary')) return true;
        }
      }
      return false;
    };

    // Horizontal page scroll.
    const doc = document.documentElement;
    if (doc.scrollWidth > doc.clientWidth + 1) out.push(`the page scrolls sideways by ${doc.scrollWidth - doc.clientWidth}px`);

    // Text runs: one box per line of each visible text node, clipped by every ancestor
    // that clips, so text scrolled or cut out of view does not count as on screen.
    type Box = { node: Text; el: Element; l: number; t: number; r: number; b: number };
    const boxes: Box[] = [];
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const fonts = new Set<Element>();
    let lone = '';
    for (let n = walker.nextNode() as Text | null; n; n = walker.nextNode() as Text | null) {
      const el = n.parentElement;
      if (!el || !(n.textContent ?? '').trim()) continue;
      if (el.closest('script, style, noscript, template, option, canvas, [popover]:not(:popover-open), dialog:not([open])')) continue;
      if (hiddenByA11y(el)) continue;
      const range = document.createRange();
      range.selectNodeContents(n);
      const rects = [...range.getClientRects()].filter((r) => r.width > 0.5 && r.height > 0.5);
      if (!rects.length) continue;
      if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(n.textContent ?? '')) lone = say(el);
      // SPEC 22 r2, squeezed: a word broken down to one or two characters per line
      // (a column squeezed to a sliver renders its header one letter per line).
      if (rects.length > 1) {
        const txt = n.textContent ?? '';
        let best = { at: 0, len: 0 };
        for (const m of txt.matchAll(/\S+/g)) if (m[0].length > best.len) best = { at: m.index, len: m[0].length };
        if (best.len >= 4) {
          const wr = document.createRange();
          wr.setStart(n, best.at);
          wr.setEnd(n, best.at + best.len);
          const lines = new Set([...wr.getClientRects()].filter((r) => r.width > 0.5).map((r) => Math.round(r.top))).size;
          if (lines > 1 && best.len / lines < 3) out.push(`squeezed text, ${lines} lines for a ${best.len}-character word: ${say(el)}`);
        }
      }
      fonts.add(el);
      let clip = { l: -1e9, t: -1e9, r: 1e9, b: 1e9 };
      // The tightest horizontal cut by a box that does not scroll: text past it is lost.
      let cut = { l: -1e9, r: 1e9 };
      let ellipsis = false;
      // Text inside a box that scrolls sideways is reachable, whatever clips above it.
      let scrolls = false;
      for (let p: Element | null = el; p && p !== document.documentElement; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.overflowX !== 'visible' || cs.overflowY !== 'visible') {
          const q = p.getBoundingClientRect();
          clip = { l: Math.max(clip.l, q.left), t: Math.max(clip.t, q.top), r: Math.min(clip.r, q.right), b: Math.min(clip.b, q.bottom) };
          if (cs.overflowX === 'auto' || cs.overflowX === 'scroll') scrolls = true;
          else if (!scrolls && (cs.overflowX === 'hidden' || cs.overflowX === 'clip')) {
            const pad = (v: string) => parseFloat(v) || 0;
            cut = { l: Math.max(cut.l, q.left + pad(cs.borderLeftWidth)), r: Math.min(cut.r, q.right - pad(cs.borderRightWidth)) };
            if (cs.textOverflow === 'ellipsis') ellipsis = true;
          }
        }
      }
      if (rects.some((r) => (r.right > cut.r + 1 || r.left < cut.l - 1) && Math.min(r.right, cut.r) - Math.max(r.left, cut.l) > 0.5)) {
        if (!ellipsis) out.push(`text cut with no ellipsis: ${say(el)}`);
        else if (!el.closest('[title]')) out.push(`ellipsized text with no title: ${say(el)}`);
      }
      for (const r of rects) {
        const b = { node: n, el, l: Math.max(r.left, clip.l), t: Math.max(r.top, clip.t), r: Math.min(r.right, clip.r), b: Math.min(r.bottom, clip.b) };
        if (b.r - b.l > 0.5 && b.b - b.t > 0.5) boxes.push(b);
      }
    }
    if (lone) out.push(`a lone surrogate (a cut emoji) in ${lone}`);

    // SPEC 22 r2: no table column holding words is narrower than 6ch.
    for (const cell of root.querySelectorAll('th, td')) {
      if (hiddenByA11y(cell) || !cell.getClientRects().length) continue;
      if ((cell.textContent ?? '').trim().length < 3) continue;
      const cs = getComputedStyle(cell);
      const ctx = document.createElement('canvas').getContext('2d');
      if (!ctx) continue;
      ctx.font = `${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      const ch = ctx.measureText('0').width;
      const w = cell.getBoundingClientRect().width - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
      if (w < 6 * ch - 0.5) out.push(`a table column ${w.toFixed(0)}px wide, under 6ch (${(6 * ch).toFixed(0)}px): ${say(cell)}`);
    }

    // Nothing renders below --text-xs (12.19px).
    for (const el of fonts) {
      const fs = parseFloat(getComputedStyle(el).fontSize);
      if (fs < 12.1) out.push(`text under 12.19px (${fs}px): ${say(el)}`);
    }

    // No two text runs intersect. Rows are bucketed so a 500-row page stays fast.
    const rows = new Map<number, Box[]>();
    for (const x of boxes) {
      for (let k = Math.floor(x.t / 40); k <= Math.floor(x.b / 40); k++) {
        if (!rows.has(k)) rows.set(k, []);
        rows.get(k)!.push(x);
      }
    }
    const seen = new Set<string>();
    const idx = new Map(boxes.map((b, i) => [b, i]));
    // A text box is the nearest box that is not inline: runs inside one inline
    // formatting context are one sentence, not two boxes.
    const boxOf = new Map<Element, Element>();
    // An atomic inline box (an inline-flex link, an inline-block badge) placed in running
    // text is a word of that sentence too, spaced by the sentence's own word space; a
    // flex or grid item is blockified and stays a box of its own.
    const flows = (p: Element) => {
      const d = getComputedStyle(p).display;
      if (/^(inline|contents)$/.test(d)) return true;
      if (!/^inline-/.test(d) || !p.parentElement) return false;
      return !/(flex|grid)$/.test(getComputedStyle(p.parentElement).display);
    };
    const textBox = (el: Element): Element => {
      if (boxOf.has(el)) return boxOf.get(el)!;
      let p: Element = el;
      while (p.parentElement && flows(p)) p = p.parentElement;
      boxOf.set(el, p);
      return p;
    };
    for (const list of rows.values()) {
      for (let i = 0; i < list.length; i++) {
        for (let j = i + 1; j < list.length; j++) {
          const a = list[i], b = list[j];
          if (a.node === b.node) continue;
          const key = `${idx.get(a)}:${idx.get(b)}`;
          if (seen.has(key)) continue;
          seen.add(key);
          const ix = Math.min(a.r, b.r) - Math.max(a.l, b.l);
          const iy = Math.min(a.b, b.b) - Math.max(a.t, b.t);
          if (ix > 1 && iy > 1) out.push(`text overlaps text: ${say(a.el)} x ${say(b.el)}`);
          else if (iy > 4 && textBox(a.el) !== textBox(b.el)) {
            // C4: two text boxes side by side on one line keep a clear 8px gap.
            const gap = Math.max(a.l, b.l) - Math.min(a.r, b.r);
            if (gap < 7.5 && gap > -1) out.push(`a ${gap.toFixed(1)}px gap, under 8px, between text boxes: ${say(a.el)} / ${say(b.el)}`);
          }
        }
      }
    }

    // Innermost first, so a spill is reported once, on the box it leaves.
    const spilt = new Set<Text>();
    for (const el of [...root.querySelectorAll('*')].reverse()) {
      if (el instanceof SVGElement) continue;
      const cs = getComputedStyle(el);
      if (cs.display === 'inline' || cs.display === 'none' || el.clientWidth === 0) continue;
      const over = el.scrollWidth - el.clientWidth;
      if (over <= 1) continue;
      if (hiddenByA11y(el)) continue;
      const ownText = [...el.childNodes].some((c) => c.nodeType === 3 && (c.textContent ?? '').trim()) || el.querySelector(':scope > bdi');
      if (cs.overflowX === 'visible') {
        // Text spilling out of a box that does not clip it. A touch target widened by
        // a negative margin overflows too, but carries no text past the edge.
        const q = el.getBoundingClientRect();
        const spill = boxes.find((x) => el.contains(x.el) && (x.r > q.right + 1 || x.l < q.left - 1));
        if (spill && !spilt.has(spill.node)) {
          spilt.add(spill.node);
          out.push(`${say(el)} lets text spill out of its box: ${say(spill.el)}`);
        }
      } else if (cs.overflowX === 'hidden' || cs.overflowX === 'clip') {
        if (!ownText) continue;
        // Ellipsized text says where its full value is.
        if (cs.textOverflow === 'ellipsis' && !el.closest('[title]')) out.push(`ellipsized text with no title: ${say(el)}`);
      }
    }

    // A shown identity is its title, or an elision of it (head and tail are its ends).
    // A bdi marked data-format is a deliberate rendering (a short digest, an account
    // as namespace/name), not a cut name.
    for (const bdi of root.querySelectorAll('bdi:not([data-format])')) {
      if (hiddenByA11y(bdi)) continue;
      const t = bdi.textContent ?? '';
      const parts = t.split('…');
      const elided = parts.length > 1;
      // The title that holds an identity's full value sits on an element around it. A
      // title on a box that also holds other words speaks for the identity only when the
      // identity is elided; otherwise it may describe the whole box.
      const owner = bdi.closest('[title]') as HTMLElement | null;
      const own = !!owner && (owner.textContent ?? '').trim() === t.trim();
      if (!owner || (!own && !elided)) {
        if (elided) out.push(`elided identity "${t.slice(0, 40)}" has no title with its full value`);
        continue;
      }
      const title = owner.title;
      // C6: whole, or head + … + tail (with the siblings' distinguishing middle between two
      // ellipses): the head is the title's start (4 or more characters), the tail its end
      // (5 or more), each middle piece sits in order between them, and a name of 12 code
      // points or fewer is never elided.
      let ok = t === title;
      if (elided) {
        const cp = (x: string) => [...x].length;
        const head = parts[0], tail = parts[parts.length - 1];
        let at = head.length;
        ok = cp(title) > 12 && cp(head) >= 4 && cp(tail) >= 5 && title.startsWith(head) && title.endsWith(tail)
          && parts.slice(1, -1).every((m) => { const i = title.indexOf(m, at); if (i < 0 || !m) return false; at = i + m.length; return true; })
          && at < title.length - tail.length;
      }
      if (!ok) out.push(`identity "${t.slice(0, 40)}" is neither its title nor a head, middle and tail elision of it: "${title.slice(0, 40)}"`);
    }

    // L1 (r2), at 1280: a data figure's drawing and aside together fill at least 60% of
    // its body; a fixed-width drawing that left the right half of the card empty failed.
    // A row of steps turned vertical (S1 r3, data-w="col") is narrow by that rule.
    if (window.innerWidth >= 1200 && window.innerWidth <= 1440) {
      for (const f of root.querySelectorAll('[data-fig].fig--data:not([data-compact]):not([data-w="col"])')) {
        const body = f.querySelector('.fig-body');
        if (!body || !body.getClientRects().length) continue;
        const q = body.getBoundingClientRect();
        let l = Infinity, t = Infinity, r = -Infinity, b = -Infinity;
        const take = (x: DOMRect) => { if (x.width < 0.5 || x.height < 0.5) return; l = Math.min(l, x.left); t = Math.min(t, x.top); r = Math.max(r, x.right); b = Math.max(b, x.bottom); };
        for (const m of body.querySelectorAll('.mk')) take(m.getBoundingClientRect());
        const tw = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
        for (let n = tw.nextNode(); n; n = tw.nextNode()) {
          if (!(n.textContent ?? '').trim() || !n.parentElement || hiddenByA11y(n.parentElement)) continue;
          const rg = document.createRange(); rg.selectNodeContents(n);
          for (const x of rg.getClientRects()) take(x);
        }
        if (r < l || q.width * q.height === 0) continue;
        const filled = ((Math.min(r, q.right) - Math.max(l, q.left)) * (Math.min(b, q.bottom) - Math.max(t, q.top))) / (q.width * q.height);
        if (filled < 0.6) out.push(`${say(f)} leaves ${Math.round((1 - filled) * 100)}% of its card empty, more than 40% (L1)`);
      }
    }

    // Figures (the [data-fig] drawings): at most 20 marks, and every number in a lane,
    // station or aside is a filter link or says it has none.
    for (const f of root.querySelectorAll('[data-fig]')) {
      const marks = [...f.querySelectorAll('.mk')].filter((m) => m.getClientRects().length);
      if (marks.length > 20) out.push(`${say(f)} draws ${marks.length} marks, more than 20`);
      // A mark never covers text (a "+N" count chip is itself the mark).
      const words = boxes.filter((x) => f.contains(x.el) && !x.el.closest('.mk, .on-mark'));
      for (const m of marks) {
        // A mark is what it draws: its own box and every child that overflows it (a
        // stacked segment taller than its column spills out of a fixed-height box).
        const r = [m, ...m.querySelectorAll('*')].map((e) => e.getBoundingClientRect()).filter((q) => q.width > 0.5 && q.height > 0.5)
          .reduce((a, q) => ({ left: Math.min(a.left, q.left), top: Math.min(a.top, q.top), right: Math.max(a.right, q.right), bottom: Math.max(a.bottom, q.bottom) }),
            { left: 1e9, top: 1e9, right: -1e9, bottom: -1e9 });
        if (r.right < r.left) continue;
        const hit = words.find((x) => Math.min(x.r, r.right) - Math.max(x.l, r.left) > 1 && Math.min(x.b, r.bottom) - Math.max(x.t, r.top) > 1);
        if (hit) { out.push(`a mark covers text in a figure: ${say(hit.el)}`); continue; }
        // C4: a mark and the text straight above or below it keep a clear 8px gap.
        const near = words.find((x) => Math.min(x.r, r.right) - Math.max(x.l, r.left) > 1 && Math.max(x.t - r.bottom, r.top - x.b) < 7.5);
        if (near) out.push(`a ${Math.max(near.t - r.bottom, r.top - near.b).toFixed(1)}px gap, under 8px, between a mark and text: ${say(near.el)}`);
      }
      for (const n of f.querySelectorAll('.lane, .station, .fig-aside')) {
        const tw = document.createTreeWalker(n, NodeFilter.SHOW_TEXT);
        for (let t = tw.nextNode(); t; t = tw.nextNode()) {
          if (/^\s*\+?\d[\d,]*\s*$/.test(t.textContent ?? '') && !t.parentElement?.closest('a, [data-nofilter]')) {
            out.push(`a figure number is neither a link nor data-nofilter: ${say(t.parentElement)}`);
          }
        }
      }
    }

    // One err ring per page at most (the alarm, or a figure's single ring). A ring is
    // any element (or its ::before / ::after) whose outer box-shadow is drawn in the
    // err colour, whatever its class, plus the ring classes themselves.
    // Colours come back as rgb()/rgba(), or as color(srgb …) when a color-mix() is
    // involved; both reduce to "r, g, b" in 0..255.
    const rgbOf = (str: string): string => {
      const m = str.match(/rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/);
      if (m) return [m[1], m[2], m[3]].map((v) => Math.round(Number(v))).join(', ');
      const c = str.match(/color\(srgb\s+([\d.]+)\s+([\d.]+)\s+([\d.]+)/);
      return c ? [c[1], c[2], c[3]].map((v) => Math.round(Number(v) * 255)).join(', ') : '';
    };
    const probe = document.createElement('span');
    probe.style.color = 'var(--c-err)';
    document.body.appendChild(probe);
    const errRgb = rgbOf(getComputedStyle(probe).color);
    probe.remove();
    const errRing = (shadow: string) => !!errRgb && shadow !== 'none' && shadow.split(/,(?![^(]*\))/)
      .some((one) => !/\binset\b/.test(one) && rgbOf(one) === errRgb);
    const rings = [...root.querySelectorAll('*')].filter((el) =>
      (el.matches('.is-alarm, .ring, [data-ring]') || [null, '::before', '::after'].some((pe) => errRing(getComputedStyle(el, pe).boxShadow)))
      && el.getClientRects().length > 0 && !hiddenByA11y(el));
    if (rings.length > 1) out.push(`${rings.length} err rings on one page, more than 1: ${rings.slice(0, 3).map(say).join(', ')}`);

    return [...new Set(out)];
  });
}

/**
 * densityProblems is L3 (r5), for list views at 1280x900: a data figure above the list
 * either leaves the list's first row (a table header or a list row) within 600px of the
 * page top or offers a Compact toggle (aria-pressed) that folds the drawing away.
 */
export async function densityProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const fig = document.querySelector('main [data-fig].fig--data');
    if (!fig) return [];
    const after = (el: Element) => !!(fig.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING) && !fig.contains(el);
    const first = [...document.querySelectorAll('main thead, main .pl-row')].find((el) => after(el) && el.getClientRects().length);
    if (!first) return [];
    const top = Math.round(first.getBoundingClientRect().top + window.scrollY);
    const toggle = [...fig.querySelectorAll('.fig-head button[aria-pressed]')].some((b) => (b.textContent ?? '').trim() === 'Compact');
    return top > 600 && !toggle ? [`the list starts ${top}px from the page top, under a figure with no Compact toggle (L3)`] : [];
  });
}

/**
 * figureLinkTotals follows every "List all N" and "N more" link of a figure (T3, G1 r5)
 * and reads the total of the list it lands on, from the `data-view-total` its pager
 * carries: "List all N" must land on N, "N more" on N plus the chips its lane names,
 * and a link that says how many it lists in `data-lists` (a phase station's "N
 * consumers", where the station's own count is of requests) on that N. A link that lands on no paged list (prose) fails too. It navigates, so call it last.
 */
export async function figureLinkTotals(page: Page): Promise<string[]> {
  const links = await page.evaluate(() => [...document.querySelectorAll<HTMLAnchorElement>('[data-fig] a')]
    .filter((a) => a.getClientRects().length && (a.hasAttribute('data-lists') || /^(List all [\d,]+|[\d,]+ more)$/.test((a.textContent ?? '').trim())))
    .map((a) => {
      const t = (a.textContent ?? '').trim(), n = Number(t.replace(/[^\d]/g, ''));
      const lists = a.getAttribute('data-lists');
      if (lists != null) return { href: a.getAttribute('href') ?? '', text: t, want: Number(lists) };
      const lane = a.closest('.lane, .station');
      const shown = lane ? [...lane.querySelectorAll('.chip')].filter((c) => c.getClientRects().length).length : 0;
      return { href: a.getAttribute('href') ?? '', text: t, want: t.startsWith('List all') ? n : n + shown };
    }));
  const out: string[] = [];
  for (const l of links) {
    await page.goto(`/${l.href}`);
    const total = async () => page.evaluate(() => {
      const v = document.querySelector('[data-view-total]')?.getAttribute('data-view-total');
      return v == null ? null : Number(v);
    });
    try {
      await expect.poll(total, { timeout: 20_000 }).toBe(l.want);
    } catch {
      const got = await total();
      out.push(got == null ? `"${l.text}" lands on no paged list: ${l.href}` : `"${l.text}" lands on a list of ${got}, want ${l.want}: ${l.href}`);
    }
  }
  return out;
}

/**
 * sweepOverflow checks horizontal overflow at every 20px from 760 to 1440 (SPEC 19),
 * then every 40px to 1920 (SPEC 22):
 * a layout clean at 1024 and 1280 spilled at 840 to 900, 1040 to 1100 and 1140 to
 * 1160. Only overflow is checked here; the full gate runs at the fixed widths.
 */
export async function sweepOverflow(page: Page): Promise<string[]> {
  const out: string[] = [];
  for (let w = 760; w <= 1920; w += w < 1440 ? 20 : 40) {
    await page.setViewportSize({ width: w, height: 900 });
    await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
    const found = await page.evaluate(() => {
      const doc = document.documentElement;
      const msgs: string[] = [];
      if (doc.scrollWidth > doc.clientWidth + 1) msgs.push(`the page scrolls sideways by ${doc.scrollWidth - doc.clientWidth}px`);
      // html and body clip sideways (overflow-x: clip), so a spill never scrolls the
      // page: it is cut silently. Look for any box that runs past the main column and
      // is not inside a box of its own that scrolls or clips.
      const main = document.querySelector('main');
      if (main) {
        const edge = Math.min(main.getBoundingClientRect().right, doc.clientWidth);
        const contained = (el: Element) => {
          for (let p = el.parentElement; p && p !== main; p = p.parentElement) {
            if (getComputedStyle(p).overflowX !== 'visible') return true;
          }
          return false;
        };
        for (const el of main.querySelectorAll('*')) {
          const r = el.getBoundingClientRect();
          if (!r.width || r.right <= edge + 1) continue;
          const cs = getComputedStyle(el);
          if (cs.position === 'fixed' || cs.visibility === 'hidden' || cs.display === 'none' || contained(el)) continue;
          const t = (el.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 32);
          msgs.push(`<${el.tagName.toLowerCase()}> "${t}" runs ${Math.round(r.right - edge)}px past the main column`);
          break;
        }
      }
      return msgs;
    });
    for (const m of found) out.push(`${w}px: ${m}`);
  }
  return out;
}

// The routes the layout specs walk (G1 and the SPEC 24.6 matrix), and the waits they share.

export async function chooseRevision(page: Page, sel: string, version: string) {
  const read = () => page.locator(`${sel} option`).evaluateAll(
    (os, v) => (os as HTMLOptionElement[]).find((o) => (o.textContent ?? '').startsWith(`${v} `))?.value ?? '', version);
  await expect.poll(read, { message: `no ${version} revision in ${sel}`, timeout: 20_000 }).not.toBe('');
  await page.locator(sel).selectOption(await read());
}

/** Opens the first entity link of a kind on a list route: keys come from the engine. */
const firstLink = (list: string, href: string) => async (page: Page) => {
  await page.goto(`/${list}`);
  const a = page.locator(`main a[href*="${href}"]`).first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

/** Opens a service created from a golden path, through the path's own created count. */
const createdService = async (page: Page) => {
  await page.goto('/#/fleet/golden-paths');
  const created = page.locator('[data-testid="path-created"]').first();
  await expect(created).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await created.getAttribute('href')}`);
  const a = page.locator('[data-testid="service-list"] a[href*="/fleet/services/"]').first();
  await expect(a).toBeVisible({ timeout: 20_000 });
  await page.goto(`/${await a.getAttribute('href')}`);
};

// `list` marks list views, where L3 holds the list within 600px or the figure folds.
export type Route = { name: string; hash?: string; list?: boolean; open?: (page: Page) => Promise<void> };
export const ROUTES: Route[] = [
  { name: 'overview', hash: '#/fleet' },
  { name: 'services', list: true, hash: '#/fleet/services' },
  { name: 'owners', list: true, hash: '#/fleet/owners' },
  { name: 'domains', list: true, hash: '#/fleet/domains' },
  { name: 'offerings', list: true, hash: '#/fleet/offerings' },
  { name: 'sources', list: true, hash: '#/fleet/sources' },
  { name: 'attention', list: true, hash: '#/fleet/attention' },
  { name: 'graph discovery', hash: '#/fleet/graph' },
  { name: 'graph focused', hash: '#/fleet/graph/service/payments-service' },
  { name: 'change analysis idle', hash: '#/fleet/changes/payments-service' },
  {
    name: 'change analysis answered',
    open: async (page) => {
      await page.goto('/#/fleet/changes/payments-service');
      await chooseRevision(page, '#impact-old-rev', '1.0.0');
      await chooseRevision(page, '#impact-new-rev', '2.0.1');
      await page.getByRole('button', { name: /Compare revisions/ }).click();
      await expect(page.locator('#sec-affected-consumers')).toBeVisible({ timeout: 30_000 });
    },
  },
  { name: 'service entity', hash: '#/fleet/services/payments-service' },
  { name: 'revision list', list: true, hash: '#/fleet/revisions?service=payments-service' },
  { name: 'revision entity', open: firstLink('#/fleet/revisions?service=payments-service', '/fleet/revisions/') },
  { name: 'target list', list: true, hash: '#/fleet/targets' },
  { name: 'target entity', open: firstLink('#/fleet/targets', '/fleet/targets/') },
  { name: 'owner entity', hash: '#/fleet/owners/team:catalog-team' },
  { name: 'domain entity', hash: '#/fleet/domains/commerce' },
  { name: 'offering entity', hash: '#/fleet/offerings/messaging' },
  { name: 'source entity', hash: '#/fleet/sources/edge-cluster' },
  { name: 'golden paths', list: true, hash: '#/fleet/golden-paths' },
  { name: 'golden path entity', open: firstLink('#/fleet/golden-paths?sort=name', '/fleet/services/') },
  { name: 'service created from a golden path', open: createdService },
  { name: 'delivery inbox', list: true, hash: '#/delivery/inbox' },
  { name: 'delivery environments', list: true, hash: '#/delivery/environments' },
  { name: 'delivery environments of one service', hash: '#/delivery/environments?service=payments' },
  { name: 'delivery history', list: true, hash: '#/delivery/history' },
  { name: 'delivery stage history', hash: '#/delivery/history?namespace=prod&stage=payments' },
  { name: 'delivery autonomy', list: true, hash: '#/delivery/autonomy' },
  { name: 'delivery publish', list: true, hash: '#/delivery/publish' },
  { name: 'delivery change', hash: '#/delivery/changes/prod/payments-promote-2' },
  { name: 'delivery change held', hash: '#/delivery/changes/test/pay-agent-promote-17' },
];

/** Flips the sidebar with its "[" shortcut and returns the state it lands in. */
export async function toggleSidebar(page: Page): Promise<string> {
  const side = page.locator('[data-testid="sidebar"]');
  const before = await side.getAttribute('data-rail');
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur?.()); // "[" is ignored in a field
  await page.keyboard.press('[');
  await expect(side).not.toHaveAttribute('data-rail', before ?? '');
  await settle(page);
  return (await side.getAttribute('data-rail')) ?? 'none';
}

/** The route has drawn its answer: a heading, and nothing still loading. */
export async function loaded(page: Page) {
  await expect(page.locator('h1').first()).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => page.evaluate(() =>
    !document.querySelector('[aria-busy="true"]') &&
    ![...document.querySelectorAll('main *')].some((el) => /^Loading\b/.test((el.textContent ?? '').trim()) && (el as HTMLElement).offsetParent !== null && el.children.length === 0)),
  { timeout: 30_000 }).toBe(true);
}

/**
 * shellProblems checks the shell at the current viewport (SPEC 22.3, 22.4, 23, 24.4):
 * the content stops at its capped width; the sidebar and its background run to the
 * window bottom at scroll top and at the foot of the page; under 768px the Menu drawer
 * does. It scrolls, then returns to the top.
 */
export async function shellProblems(page: Page): Promise<string[]> {
  const out: string[] = [];
  // SPEC 22.3: the content stops at its capped width, however wide the window.
  const cap = await page.evaluate(() => {
    const v = getComputedStyle(document.documentElement).getPropertyValue('--container-max').trim();
    const max = v.endsWith('px') ? parseFloat(v) : 0; // under 768px the cap is 100%
    const w = document.querySelector('main')?.getBoundingClientRect().width ?? 0;
    return { max, w: Math.round(w) };
  });
  if (cap.max > 0 && cap.w > cap.max + 1) out.push(`the content is ${cap.w}px wide, past its ${cap.max}px cap`);
  const side = page.locator('[data-testid="sidebar"]');
  const bottom = (sel: string) => page.evaluate((s) => {
    const el = document.querySelector(s);
    const r = el?.getBoundingClientRect();
    return r && r.height ? { b: Math.round(r.bottom), h: window.innerHeight } : null;
  }, sel);
  if (await side.isVisible()) {
    for (const where of ['top', 'foot'] as const) {
      await page.evaluate((w) => window.scrollTo(0, w === 'top' ? 0 : document.documentElement.scrollHeight), where);
      await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
      const got = await bottom('[data-testid="sidebar"] .side-in');
      if (got && Math.abs(got.b - got.h) > 1) out.push(`the sidebar ends at ${got.b}px, not at the window bottom (${got.h}px), at the page ${where}`);
    }
    await page.evaluate(() => window.scrollTo(0, 0));
    return out;
  }
  const menu = page.getByRole('button', { name: 'Menu' });
  if (await menu.isVisible()) {
    await menu.click();
    await expect(page.locator('#mobile-drawer')).toBeVisible();
    const got = await bottom('#mobile-drawer');
    if (got && got.b < got.h - 1) out.push(`the menu drawer ends at ${got.b}px, short of the window bottom (${got.h}px)`);
    await page.keyboard.press('Escape');
    await expect(page.locator('#mobile-drawer')).toBeHidden();
  }
  return out;
}

/**
 * touchProblems is the SPEC 23 touch rule: on a coarse pointer every visible control and
 * every link outside running text is at least 44x44px. A link inside a sentence keeps its
 * line (WCAG 2.5.8 exempts inline targets). It reports nothing on a fine pointer.
 */
export async function touchProblems(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    if (!matchMedia('(pointer: coarse)').matches) return [];
    const out: string[] = [];
    const hidden = (el: Element) => {
      for (let p: Element | null = el; p && p !== document.body; p = p.parentElement) {
        const cs = getComputedStyle(p);
        if (cs.display === 'none' || cs.visibility === 'hidden') return true;
        if (cs.position === 'absolute' && (cs.clip === 'rect(0px, 0px, 0px, 0px)' || cs.clipPath === 'inset(50%)')) return true;
        if (p.tagName === 'DETAILS' && !(p as HTMLDetailsElement).open && !el.closest('summary')) return true;
      }
      return false;
    };
    // In a sentence: an inline link whose nearest block also holds words of its own, or
    // a link in running text (a paragraph that is not a .go row of links, a caption).
    const inSentence = (a: Element) => {
      if (a.closest('p:not(.go), figcaption, .t-meta, .lead, .dist-scope')) return true;
      if (!/^inline$/.test(getComputedStyle(a).display)) return false;
      let blk = a.parentElement;
      while (blk && /^inline/.test(getComputedStyle(blk).display)) blk = blk.parentElement;
      return !!blk && (blk.textContent ?? '').replace(/\s+/g, '').length > (a.textContent ?? '').replace(/\s+/g, '').length;
    };
    const sel = 'a[href], button, select, input:not([type=hidden]):not([type=checkbox]):not([type=radio]), summary, [role=button]';
    for (const el of document.querySelectorAll(`#app :is(${sel})`)) {
      const r = el.getBoundingClientRect();
      if (!r.width || !r.height || hidden(el) || el.closest('[popover]:not(:popover-open), dialog:not([open])')) continue;
      if (el.tagName === 'A' && inSentence(el)) continue;
      if (r.width < 43.5 || r.height < 43.5) {
        const t = (el.textContent || el.getAttribute('aria-label') || '').trim().replace(/\s+/g, ' ').slice(0, 32);
        out.push(`a ${Math.round(r.width)}x${Math.round(r.height)}px touch target, under 44x44: <${el.tagName.toLowerCase()}> "${t}"`);
      }
    }
    return [...new Set(out)];
  });
}
