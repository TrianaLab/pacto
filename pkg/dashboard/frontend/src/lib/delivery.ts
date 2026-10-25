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
  'pacto.dev/unfreeze': 'Unfreeze',
  'pacto.dev/freeze': 'Freeze',
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

/** Where the docs say what Unknown means and how a person resolves it. */
export const UNKNOWN_DOCS = 'https://pacto.run/latest/integrations/kubernetes/delivery/#what-unknown-means';

/**
 * unknownHelp says, for a check that ended Unknown, what Pacto could not see
 * and what makes it see it next time. Unknown is never a yes: until it can
 * see, a person accepts the unknown or rejects the change.
 */
export function unknownHelp(v: { gate?: string; citation?: Record<string, string> }): string {
  if (v.citation?.provider === 'mira') {
    return 'Pacto could not read enough traces to judge the release. Send the service\'s spans to Mira and keep the verify window long enough to collect them.';
  }
  switch (v.gate) {
    case 'pacto.contract':
    case 'pacto.runtime':
      return 'Pacto could not read the service\'s contract status. Check that the operator evaluates the Pacto for this service and that its runtime agent reports.';
    case 'pacto.impact':
      return 'Pacto could not tell which services this affects. Publish a contract for each service that calls this one, with the versions it accepts.';
    default:
      return 'Pacto could not get an answer from this check. Check that it is configured and reachable.';
  }
}

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

/**
 * shortDigests shortens every full OCI digest in a sentence, and names the
 * version a digest carries when versions knows it.
 */
export function shortDigests(text: string | undefined, versions?: Record<string, string> | null): string {
  return (text ?? '').replace(/sha256:([0-9a-f]{12})[0-9a-f]{52}/g, (d, hex) => (versions?.[d] ? `${versions[d]} (${hex})` : `sha256:${hex}`));
}

/** A piece of text, or a digest in it: short to read, full to hover and copy. */
export interface DigestPart {
  text: string;
  full?: string;
}

/**
 * digestParts splits a sentence around its OCI digests. A full digest keeps
 * itself; a short one (sha256: and 12 hex characters, as the server writes
 * them) takes the one id in known it starts, when exactly one does.
 */
export function digestParts(text: string | undefined, known: (string | undefined)[] = []): DigestPart[] {
  const s = text ?? '';
  const out: DigestPart[] = [];
  let last = 0;
  for (const m of s.matchAll(/sha256:[0-9a-f]{12}(?:[0-9a-f]{52})?(?![0-9a-f])/g)) {
    const idx = m.index ?? 0;
    if (idx > last) out.push({ text: s.slice(last, idx) });
    const hits = m[0].length === 71 ? [m[0]] : [...new Set(known.filter((k) => k?.length === 71 && k.startsWith(m[0])))];
    out.push(hits.length === 1 ? { text: shortDigest(hits[0]), full: hits[0] } : { text: m[0] });
    last = idx + m[0].length;
  }
  if (last < s.length) out.push({ text: s.slice(last) });
  return out;
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

/**
 * signIn sends the person to sign in and back to `hash`; fresh makes them log
 * in again. A host with no /auth routes handles it itself: the browser demo
 * sets window.__pactoSignIn (beside __pactoServe) to its simulated sign-in,
 * because leaving the page would lose everything the tab simulated.
 */
export function signIn(hash: string, fresh = false): void {
  const hook = (window as unknown as { __pactoSignIn?: (hash: string, fresh: boolean) => void }).__pactoSignIn;
  if (typeof hook === 'function') hook(hash, fresh);
  else location.assign(loginUrl(hash, fresh));
}

/** hostSignsIn: the host handles sign-in itself (see signIn). */
export function hostSignsIn(): boolean {
  return typeof (window as unknown as { __pactoSignIn?: unknown }).__pactoSignIn === 'function';
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

const OUTCOMES: Record<string, string> = { Succeeded: 'succeeded', Failed: 'failed', Rejected: 'was rejected', Errored: 'could not finish' };
const VERIFICATIONS: Record<string, string> = { Verified: 'verified', Unverified: 'not verified', AcceptedUnknown: 'a person accepted the unknown' };
const PHASES: Record<string, string> = { Held: 'waits for a person', Pending: 'waits its turn', Running: 'is running', Verifying: 'is being checked', '': 'is not admitted yet' };

/** The fields changeSummary reads from an explanation. */
export interface ChangeFacts {
  spec?: { type?: string; stage?: string; params?: { from?: string; to?: string } | null };
  status?: { phase?: string; outcome?: string; verification?: string };
}

/**
 * changeSummary says in one sentence what a change asked and where it stands,
 * with only the parts it has: no empty fields and no raw machine words.
 */
export function changeSummary(e: ChangeFacts | null | undefined, namespace: string, versions?: Record<string, string> | null): string {
  if (!e) return '';
  const p = e.spec?.params ?? {};
  const v = (d: string | undefined) => (d ? versionLabel(d, versions) : '');
  const move = p.from && p.to ? ` from ${v(p.from)} to ${v(p.to)}` : p.to ? ` to ${v(p.to)}` : '';
  const what = `${typeLabel(e.spec?.type)} ${e.spec?.stage ?? ''} in ${namespace}${move}`;
  const st = e.status ?? {};
  if (st.phase === 'Concluded') {
    const end = [OUTCOMES[st.outcome ?? ''] ?? st.outcome, VERIFICATIONS[st.verification ?? ''] ?? st.verification].filter(Boolean).join(', ');
    return `${what} ${end ? `ended: it ${end}` : 'ended'}.`;
  }
  return `${what} ${PHASES[st.phase ?? ''] ?? `is ${(st.phase ?? '').toLowerCase()}`}.`;
}

const ACTOR_BADGES: Record<string, { label: string; tone: string }> = {
  human: { label: 'Person', tone: 'badge-ok' },
  agent: { label: 'Agent', tone: 'badge-info' },
  controller: { label: 'Controller', tone: 'badge-neutral' },
};

/** actorBadge is the chip that says whether a person, an agent or Pacto's own controller acted. */
export function actorBadge(cls: string | undefined): { label: string; tone: string } | null {
  return ACTOR_BADGES[cls ?? ''] ?? null;
}

/** One line of a history, as autonomyOf reads it. */
export interface AutonomyEntry {
  type?: string;
  kind?: string;
  phase?: string;
  actorClass?: string;
  verification?: string;
  approvals?: { decision?: string }[] | null;
}

/**
 * autonomyOf counts how far delivery runs on its own: of the promotions that
 * ended, how many no person asked for or decided, and how many waited for a
 * person only because a check was Unknown (what more visibility would remove).
 * It is null when a promotion's line does not say who approved it (no
 * approvals key at all): a person may have, so nothing can be counted.
 */
export function autonomyOf(entries: AutonomyEntry[]): { total: number; noPerson: number; onlyUnknown: number } | null {
  const done = entries.filter((e) => e.type === 'pacto.dev/promote' && e.kind !== 'gap' && e.kind !== 'unrecordable' && e.phase === 'Concluded');
  if (done.some((e) => e.approvals === undefined)) return null;
  const unknown = (e: AutonomyEntry) => e.verification === 'AcceptedUnknown' || (e.approvals ?? []).some((a) => a.decision === 'accept-unknown');
  return {
    total: done.length,
    noPerson: done.filter((e) => e.actorClass !== 'human' && !(e.approvals ?? []).length && !unknown(e)).length,
    onlyUnknown: done.filter((e) => unknown(e) && !(e.approvals ?? []).some((a) => a.decision === 'approve')).length,
  };
}

/** The fields of a history line undoneBy reads. */
export interface UndoEntry {
  operation?: string;
  type?: string;
  from?: string;
  finished?: string;
  actor?: string;
  actorClass?: string;
  pointer?: { after?: string } | null;
}

/**
 * undoneBy finds the rollback that undid the change `name`, which moved its
 * Stage to `after`: the first change after it that moved the Stage off
 * `after` (one that did not move it, failed or still running, undid
 * nothing). That is the undo only if it is a rollback; anything else means the
 * Stage moved on, and a later rollback from `after` undid that, not this.
 * Lines are in the order the history keeps them, oldest first.
 */
// ponytail: matched by version, order and time, because history lines do not name what a rollback undoes; read params.undoes if they ever do.
export function undoneBy<T extends UndoEntry>(entries: T[], name: string, after: string, finished?: string): T | null {
  const i = entries.findIndex((en) => en.operation === name);
  const next = entries.slice(i + 1).find((en) => en.operation && en.from === after && en.pointer?.after
    && (!finished || !en.finished || en.finished >= finished));
  return next?.type === 'pacto.dev/rollback' ? next : null;
}

/**
 * NO_RERUN is what the server says for a check with nothing to run again
 * (pkg/delivery RerunHint); the dashboard leaves that line out.
 */
const NO_RERUN = 'no citation to run again';

/** rerunHint is the command that runs a check again, or '' when there is none. */
export function rerunHint(rerun: string | undefined): string {
  return rerun && rerun !== NO_RERUN ? rerun : '';
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

// ── What a check concluded ──────────────────────────────────────────────────

/** The checks that gather evidence: they pass once they ran, whatever they found. */
const EVIDENCE_GATES = new Set(['pacto.diff', 'pacto.impact']);

const CLASSIFICATION_CHIPS: Record<string, string> = { BREAKING: 'Breaking', POTENTIAL_BREAKING: 'May break' };

export interface VerdictBadge {
  label: string;
  tone: string;
  /** chip names what an evidence check found that a person must weigh, else ''. */
  chip: string;
}

/**
 * verdictBadge says what a check concluded. A diff or impact check passes as
 * soon as it ran, so its Pass reads Checked, never a green Pass, and what it
 * found that can break a consumer gets its own warning chip.
 */
export function verdictBadge(v: { gate?: string; result?: string; citation?: Record<string, string> }): VerdictBadge {
  const chip = CLASSIFICATION_CHIPS[v.citation?.classification ?? ''] ?? '';
  if (v.result === 'Pass' && EVIDENCE_GATES.has(v.gate ?? '')) return { label: 'Checked', tone: 'badge-neutral', chip };
  const tone = ({ Pass: 'badge-ok', Fail: 'badge-err', Unknown: 'badge-warn' } as Record<string, string>)[v.result ?? ''] ?? 'badge-neutral';
  return { label: v.result ?? '', tone, chip };
}

/** isBreaking says whether a plan's checks classified the change as breaking. */
export function isBreaking(verdicts: { citation?: Record<string, string> }[] | null | undefined): boolean {
  return (verdicts ?? []).some((v) => v.citation?.classification === 'BREAKING');
}

/** andList joins names the way a sentence does, with no serial comma. */
export function andList(names: string[]): string {
  return names.length < 2 ? (names[0] ?? '') : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
}
