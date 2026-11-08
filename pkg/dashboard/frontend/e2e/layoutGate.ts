import type { Page } from '@playwright/test';

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
// partial), 25 providers, and identities of 60 to 200 characters including an emoji and
// a right-to-left override. Keys and hrefs never change, so every link still lands.

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

  const amend = (method: string, path: string, doc: Record<string, unknown>) => {
    const [route, query = ''] = path.split('?');
    const q = new URLSearchParams(query);
    const meta = doc.meta as Record<string, unknown> | undefined;
    if (meta && meta.sourceCounts) meta.sourceCounts = { ...SOURCE_COUNTS };
    if (route === '/api/fleet/entities' && Number(q.get('limit') || 25) > 1 && !q.get('businessDomain') && !q.get('service')) {
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
      Object.assign(cs, {
        items: all.slice(0, 10), highlights: all.slice(0, 8), total: 1284, direct: 412, count: 10, offset: 0, limit: 10,
        nextOffset: 10, truncated: true, hidden: 3, refused: 5,
        byVerdict: [{ key: 'incompatible', count: 14 }, { key: 'unknown', count: 61 }, { key: 'compatible', count: 1209 }],
        // Disjoint: the 5 refused are counted with the incompatible.
        byOutcome: [{ key: 'incompatible', count: 37 }, { key: 'unknown', count: 52 }, { key: 'compatible', count: 1189 }, { key: 'unclassified', count: 6 }],
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
    try { amend(method, path, doc); } catch (e) { console.error('scale fixture', path, e); return res; }
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
 * problem. `skip` names subtrees owned by a later phase (the figure it rewrites);
 * nothing else is ever excluded.
 */
export async function layoutProblems(page: Page, skip: string[] = []): Promise<string[]> {
  return page.evaluate((skipSel) => {
    const out: string[] = [];
    const root = document.querySelector('#app') ?? document.body;
    const skipped = (el: Element) => skipSel.some((s) => el.closest(s));
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
      if (skipped(el) || hiddenByA11y(el)) continue;
      const range = document.createRange();
      range.selectNodeContents(n);
      const rects = [...range.getClientRects()].filter((r) => r.width > 0.5 && r.height > 0.5);
      if (!rects.length) continue;
      if (/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(n.textContent ?? '')) lone = say(el);
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
      if (el instanceof SVGElement || skipped(el)) continue;
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
      if (skipped(bdi) || hiddenByA11y(bdi)) continue;
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
      const ok = title.includes(t) || (elided && title.startsWith(parts[0]) && title.endsWith(parts[parts.length - 1]));
      if (!ok) out.push(`identity "${t.slice(0, 40)}" is not an elision of its title "${title.slice(0, 40)}"`);
    }

    // Figures (the [data-fig] drawings): at most 20 marks, and every number in a lane,
    // station or aside is a filter link or says it has none.
    for (const f of root.querySelectorAll('[data-fig]')) {
      if (skipped(f)) continue;
      const marks = [...f.querySelectorAll('.mk')].filter((m) => m.getClientRects().length);
      if (marks.length > 20) out.push(`${say(f)} draws ${marks.length} marks, more than 20`);
      // A mark never covers text (a "+N" count chip is itself the mark).
      const words = boxes.filter((x) => f.contains(x.el) && !x.el.closest('.mk, .on-mark'));
      for (const m of marks) {
        const r = m.getBoundingClientRect();
        const hit = words.find((x) => Math.min(x.r, r.right) - Math.max(x.l, r.left) > 1 && Math.min(x.b, r.bottom) - Math.max(x.t, r.top) > 1);
        if (hit) out.push(`a mark covers text in a figure: ${say(hit.el)}`);
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
      && !skipped(el) && el.getClientRects().length > 0 && !hiddenByA11y(el));
    if (rings.length > 1) out.push(`${rings.length} err rings on one page, more than 1: ${rings.slice(0, 3).map(say).join(', ')}`);

    return [...new Set(out)];
  }, skip);
}

/**
 * sweepOverflow checks horizontal overflow at every 20px from 760 to 1440 (SPEC 19):
 * a layout clean at 1024 and 1280 spilled at 840 to 900, 1040 to 1100 and 1140 to
 * 1160. Only overflow is checked here; the full gate runs at the fixed widths.
 */
export async function sweepOverflow(page: Page, skip: string[] = []): Promise<string[]> {
  const out: string[] = [];
  for (let w = 760; w <= 1440; w += 20) {
    await page.setViewportSize({ width: w, height: 900 });
    await page.evaluate(() => new Promise((res) => requestAnimationFrame(() => requestAnimationFrame(res))));
    const found = await page.evaluate((skipSel) => {
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
          if (skipSel.some((s) => el.closest(s))) continue;
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
    }, skip);
    for (const m of found) out.push(`${w}px: ${m}`);
  }
  return out;
}
