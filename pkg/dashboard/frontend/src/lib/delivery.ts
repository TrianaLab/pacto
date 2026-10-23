/**
 * Plain helpers the delivery views share: labels, digests, Mira links, the
 * sign-in URLs and the decision a person left behind when a fresh login took
 * them to the identity provider.
 */
import type { DeliveryDecision, DeliveryOperation } from './api.ts';

const TYPE_LABELS: Record<string, string> = {
  'pacto.dev/promote': 'Promote',
  'pacto.dev/rollback': 'Roll back',
  'pacto.dev/override': 'Override',
  'pacto.dev/unfreeze': 'Unpause',
};

/** typeLabel names an operation type in plain words. */
export function typeLabel(type: string | undefined): string {
  return TYPE_LABELS[type ?? ''] ?? (type || 'Change');
}

const DECISION_LABELS: Record<string, string> = {
  approve: 'Approve',
  reject: 'Reject',
  'accept-unknown': 'Accept the unknown',
};

export function decisionLabel(d: string | undefined): string {
  return DECISION_LABELS[d ?? ''] ?? (d || '');
}

/**
 * versionLabel names a digest by the version it carries when the server could
 * read it, else by its short form.
 */
export function versionLabel(digest: string | undefined, versions: Record<string, string> | null | undefined): string {
  return (digest && versions?.[digest]) || shortDigest(digest);
}

/** shortDigests shortens every full OCI digest in a sentence. */
export function shortDigests(text: string | undefined): string {
  return (text ?? '').replace(/sha256:([0-9a-f]{12})[0-9a-f]{52}/g, 'sha256:$1');
}

/** timeLabel shows an RFC 3339 UTC time to the minute: 2026-10-21 11:00 UTC. */
export function timeLabel(iso: string | undefined): string {
  const m = /^(\d{4}-\d\d-\d\d)T(\d\d:\d\d)(:\d\d(\.\d+)?)?Z$/.exec(iso ?? '');
  return m ? `${m[1]} ${m[2]} UTC` : (iso ?? '');
}

/** sentence turns a server note into a sentence: capitalised, digests short, a full stop. */
export function sentence(text: string | undefined): string {
  const t = shortDigests(text).trim();
  if (!t) return '';
  return t[0].toUpperCase() + t.slice(1) + (/[.!?]$/.test(t) ? '' : '.');
}

/** Who the signed-in person is, as personLabel reads it. */
export interface Me {
  user?: string;
  name?: string;
}

/**
 * personLabel names a cluster username for a reader: the name of a person who
 * signed in to this dashboard, a service account by its own name and an
 * `issuer#subject` username by its subject. Person prints the full username
 * next to it, never only in a tooltip.
 */
export function personLabel(user: string | undefined, people?: Record<string, string> | null, me?: Me | null): string {
  if (!user) return '';
  if (people?.[user]) return people[user];
  if (me?.name && me.user === user) return me.name;
  const sa = /^system:serviceaccount:[^:]+:(.+)$/.exec(user);
  if (sa) return sa[1];
  const hash = user.lastIndexOf('#');
  return hash > 0 && hash < user.length - 1 ? user.slice(hash + 1) : user;
}

/** shortDigest keeps the first 12 hex characters of an OCI digest. */
export function shortDigest(d: string | undefined): string {
  if (!d) return '';
  const hex = d.replace(/^sha256:/, '');
  return hex.length > 12 ? hex.slice(0, 12) : hex;
}

/**
 * decisionAsked is the decision a held operation waits for, the way the
 * server decides it: a change that already shipped and whose checks stayed
 * Unknown asks for accept-unknown, anything else asks what its plan named.
 */
export function decisionAsked(op: DeliveryOperation['operation'] | undefined): DeliveryDecision {
  const st = op?.status;
  const asked = st?.plan?.floor?.decision;
  if (st?.applied || !asked) return 'accept-unknown';
  return asked as DeliveryDecision;
}

/** cliDecision is the command that takes a decision a dashboard cannot. */
export function cliDecision(namespace: string, name: string, decision: string): string {
  return `pacto delivery approve -n ${namespace} ${name} --decision ${decision}`;
}

/** loginUrl signs in and comes back to `hash`; fresh makes the person log in again. */
export function loginUrl(hash: string, fresh = false): string {
  const ret = '/' + (hash.startsWith('#') ? hash : `#${hash}`);
  return `/auth/login?return=${encodeURIComponent(ret)}${fresh ? '&fresh=1' : ''}`;
}

// ── the decision kept across a fresh login ──────────────────────────────────

export interface PendingDecision {
  namespace: string;
  name: string;
  decision: DeliveryDecision;
  note: string;
}

const PENDING_KEY = 'pacto.delivery.pending';

/** keepPending remembers a decision before the browser leaves for a fresh login. */
export function keepPending(p: PendingDecision): void {
  sessionStorage.setItem(PENDING_KEY, JSON.stringify(p));
}

/** pendingFor returns the remembered decision for this operation, if any. It is
 *  only ever shown again, never sent on its own. */
export function pendingFor(namespace: string, name: string): PendingDecision | null {
  try {
    const p = JSON.parse(sessionStorage.getItem(PENDING_KEY) || 'null') as PendingDecision | null;
    return p && p.namespace === namespace && p.name === name ? p : null;
  } catch {
    return null;
  }
}

export function dropPending(): void {
  sessionStorage.removeItem(PENDING_KEY);
}

// ── Mira citations ──────────────────────────────────────────────────────────

interface MiraTerm {
  attr?: string;
  field?: string;
  [op: string]: unknown;
}

const MIRA_OPS: Record<string, string> = { eq: '=', ne: '!=', gt: '>', gte: '>=', lt: '<', lte: '<=', contains: '~' };

/**
 * miraLink turns a Mira citation's query into a link to Mira's own trace view,
 * with the same filter. Mira's UI cannot express an absolute window, so the
 * link opens every time and the citation's window is shown next to it.
 * Null when there is no Mira address or the citation is not a Mira query.
 */
export function miraLink(citation: Record<string, string> | undefined, miraURL: string | undefined): string | null {
  // Only a web address: a configured value is still never turned into a script link.
  if (!miraURL || !/^https?:\/\//i.test(miraURL) || !citation?.query) return null;
  let q: { signal?: string; where?: MiraTerm[] };
  try {
    q = JSON.parse(citation.query);
  } catch {
    return null;
  }
  if (!q || typeof q.signal !== 'string') return null;
  const terms: string[] = [];
  for (const t of q.where ?? []) {
    const key = t.attr ?? t.field;
    const op = Object.keys(MIRA_OPS).find((o) => o in t);
    if (!key || !op) continue;
    const v = t[op];
    terms.push(`${key}${MIRA_OPS[op]}${typeof v === 'string' ? `"${v}"` : String(v)}`);
  }
  const base = miraURL.replace(/\/+$/, '');
  return `${base}/#/${q.signal}?q=${encodeURIComponent(terms.join(' '))}&range=all`;
}

const PREDICTIONS: Record<string, string> = {
  Proceeds: 'It would go ahead with no person involved.',
  Held: 'It would wait for a person to decide.',
  Rejected: 'It would be refused.',
  Waits: 'It would wait until the environment is free.',
};

/** predictionLabel says what a plan predicts, in a sentence. */
export function predictionLabel(p: string | undefined): string {
  return PREDICTIONS[p ?? ''] ?? (p || '');
}

/** authTimeText shows a sign-in time the cluster keeps as Unix seconds. */
export function authTimeText(s: string | undefined): string {
  const n = Number(s);
  return s && Number.isFinite(n) && n > 0 ? new Date(n * 1000).toISOString() : (s ?? '');
}
