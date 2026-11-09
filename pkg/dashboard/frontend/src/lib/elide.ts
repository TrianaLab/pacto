/**
 * C6: measured segment elision. An identity is rendered whole; only when it does not fit
 * its box are whole segments in its middle (a segment ends after - _ . / :) replaced
 * with "…", keeping the separator, so the head and the tail both end on a word boundary:
 * `risk-us-eu-west-1-partner-feed-compliance` reads `risk-us-…-feed-compliance`. The
 * full value stays in the element's `title`.
 */

const BIDI = /[\u202A-\u202E\u2066-\u2069\u200E\u200F]/;

/** elidable: longer than 12 code points and free of bidi controls (those keep CSS end truncation). */
export const elidable = (name: string) => !BIDI.test(name) && Array.from(name).length > 12;

const segsOf = (n: string) => n.split(/(?<=[-_./:])/);

/** Whole-segment elisions of a name, longest first. A pin keeps that segment visible. */
function elisions(parts: string[], pin?: number): string[] {
  const n = parts.length, out: string[] = [];
  for (let h = n - 2; h >= 1; h--) {
    for (let t = n - 1 - h; t >= 1; t--) {
      if (parts.slice(0, h).join('').length < 4 || parts.slice(n - t).join('').length < 5) continue;
      const hid = parts.slice(h, n - t);
      const sep = /[-_./:]$/.test(hid[hid.length - 1]) ? hid[hid.length - 1].slice(-1) : '';
      let mid = '…' + sep;
      if (pin != null && pin >= h && pin < n - t) {
        if (hid.length === 1) continue;
        mid = (pin > h ? '…' : '') + parts[pin] + (pin < n - t - 1 ? '…' + sep : '');
      }
      out.push(parts.slice(0, h).join('') + mid + parts.slice(n - t).join(''));
    }
  }
  return out.sort((a, b) => b.length - a.length);
}

/**
 * fitName returns the longest elision of `name` that `fits`, the name itself when it
 * fits, or null when nothing readable does (never a head cut mid-word or a tail under 5
 * characters). With a pin (siblings that would read alike)
 * the distinguishing segment must start within the first 12 visible characters.
 */
export function fitName(name: string, fits: (s: string) => boolean, pin?: number): string | null {
  if (fits(name)) return name;
  const parts = segsOf(name);
  const near = (c: string) => {
    if (pin == null) return true;
    const off = c.indexOf(parts[pin]);
    return off >= 0 && Array.from(c.slice(0, off)).length <= 12;
  };
  const c = elisions(parts, pin).filter(near).find(fits);
  if (c) return c;
  if (pin != null) return null;
  // No whole-segment elision fits. A name whose last segment is a word of 16 characters
  // or fewer has no other readable cut (a character cut would leave a fragment such as
  // `consumer-0-b…wes`): null, and the caller folds it. Otherwise keep whole head
  // segments (or, for a single word, at least 4 head characters) and the last segment's
  // last 8 characters.
  const cp = Array.from(name), last = Array.from(parts[parts.length - 1]);
  if (last.length <= 16) return null;
  const tail = last.slice(-8).join('');
  if (parts.length > 1) {
    for (let h = parts.length - 1; h >= 1; h--) {
      const head = parts.slice(0, h).join('');
      if (Array.from(head).length >= 4 && fits(head + '…' + tail)) return head + '…' + tail;
    }
    return null;
  }
  const head = cp.slice(0, cp.length - 8);
  let lo = 0, hi = head.length;
  while (lo < hi) {
    const m = (lo + hi + 1) >> 1;
    if (fits(head.slice(0, m).join('') + '…' + tail)) lo = m;
    else hi = m - 1;
  }
  return lo >= 4 ? head.slice(0, lo).join('') + '…' + tail : null;
}

/**
 * fitHead is the last resort for an identity that must be shown (a lane's only chip) and
 * has no readable elision: its longest whole-segment head (at least 4 characters), "…"
 * and the last 8 to 5 characters of its end, so the reader still sees both ends (C6: a
 * tail of at least 5). Null when not even that fits.
 */
export function fitHead(name: string, fits: (s: string) => boolean): string | null {
  const parts = segsOf(name), cp = Array.from(name);
  for (let t = 8; t >= 5; t--) {
    const tail = cp.slice(-t).join('');
    for (let h = parts.length - 1; h >= 1; h--) {
      const head = parts.slice(0, h).join('');
      if (Array.from(head).length >= 4 && Array.from(head).length + t < cp.length && fits(head + '…' + tail)) return head + '…' + tail;
    }
  }
  return null;
}

/**
 * fitSiblings elides names that sit side by side (chips in one lane). Two that would read
 * the same keep the first segment that tells them apart; two still alike in their first
 * 16 visible characters are not elided at all (null: the caller folds or end-truncates).
 */
export function fitSiblings(names: string[], fits: (name: string, s: string) => boolean): (string | null)[] {
  const out = names.map((n) => fitName(n, (s) => fits(n, s)));
  const head = (s: string | null) => Array.from(s ?? '').slice(0, 16).join('');
  names.forEach((n, i) => {
    const twins = names.filter((o, j) => j !== i && o !== n && out[j] === out[i]);
    if (!twins.length || out[i] === n) return;
    const p = segsOf(n);
    let pin = p.length;
    for (const o of twins) {
      const q = segsOf(o);
      let k = 0;
      while (k < p.length && p[k] === q[k]) k++;
      pin = Math.min(pin, k);
    }
    if (pin < p.length) out[i] = fitName(n, (s) => fits(n, s), pin) ?? out[i];
  });
  return out.map((o, i) => (o !== names[i] && names.some((m, j) => m !== names[i] && head(out[j]) === head(o)) ? null : o));
}

let ctx: CanvasRenderingContext2D | null | undefined;

/**
 * fitIdents measures every `.fit` identity under root in its own computed font (canvas,
 * no layout thrash) and elides the ones that overflow their box. The full name is read
 * back from the nearest `[title]`, so a second pass starts from the whole name again.
 * It returns the identities that overflow with no readable elision, for the caller to fold.
 * A lane's only shown chip has nothing left to fold into, so it takes its whole-segment head.
 */
export function fitIdents(root: Element): HTMLElement[] {
  if (ctx === undefined) {
    try { ctx = document.createElement('canvas').getContext('2d'); } catch { ctx = null; }
  }
  const els = [...root.querySelectorAll<HTMLElement>('.fit')].filter((e) => e.getClientRects().length);
  for (const e of els) {
    const full = e.closest<HTMLElement>('[title]')?.title;
    if (full && e.textContent !== full) e.textContent = full;
  }
  if (!ctx) return [];
  const c = ctx, unfit: HTMLElement[] = [];
  const groups = new Map<Element, HTMLElement[]>();
  for (const e of els) {
    if (e.scrollWidth <= e.clientWidth + 0.5) continue;
    const g = e.closest('.lane-chips') ?? root;
    groups.set(g, [...(groups.get(g) ?? []), e]);
  }
  for (const [g, list] of groups) {
    const lone = g.matches('.lane-chips') && [...g.children].filter((ch) => ch.getClientRects().length).length === 1;
    const names = list.map((e) => e.closest<HTMLElement>('[title]')?.title ?? e.textContent ?? '');
    const fits = (name: string, s: string) => {
      const e = list[names.indexOf(name)];
      const cs = getComputedStyle(e);
      c.font = `${cs.fontStyle} ${cs.fontWeight} ${cs.fontSize} ${cs.fontFamily}`;
      return c.measureText(s).width <= e.clientWidth - 2;
    };
    const res = fitSiblings(names, fits);
    list.forEach((e, i) => {
      const r = res[i] ?? (lone ? fitHead(names[i], (s) => fits(names[i], s)) : null);
      if (r) e.textContent = r;
      if (!res[i]) unfit.push(e);
    });
  }
  return unfit;
}
