/**
 * Plain helpers the delivery views share: labels, digests, Mira links and the
 * decision a person left behind when a fresh login took them to the identity
 * provider.
 */
import { api } from './api.ts';
import type { DeliveryDecision, DeliveryEnvironments, DeliveryInbox, DeliveryOffer, DeliveryOperation } from './api.ts';
import { formatCount } from './format.ts';

/** The operation type that asks a person to confirm a new publisher. */
export const TRUST_TYPE = 'delivery.pacto.trianalab.io/trust';

const TYPE_LABELS: Record<string, string> = {
  'delivery.pacto.trianalab.io/promote': 'Promote',
  'delivery.pacto.trianalab.io/rollback': 'Roll back',
  'delivery.pacto.trianalab.io/override': 'Override',
  'delivery.pacto.trianalab.io/unfreeze': 'Unfreeze',
  'delivery.pacto.trianalab.io/freeze': 'Freeze',
  'delivery.pacto.trianalab.io/autonomy': 'Level change',
  'delivery.pacto.trianalab.io/acknowledge': 'Acknowledge upstream',
  [TRUST_TYPE]: 'Confirm publisher',
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
/** Where approving works: a cluster must map the login time for anyone to decide. */
export const APPROVING_DOCS = 'https://pacto.run/latest/integrations/kubernetes/delivery-identity/#where-approving-works';
/** The caller is a person on a cluster that reports no login time, so nobody's decision counts here. */
export const cannotApproveHere = (caller?: { class?: string; authTime?: string }) => !!caller && caller.class === 'human' && !caller.authTime;

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
    case 'pacto.manifests':
      return 'Pacto could not tell what this version applies. The Proof names what it could not read: a templated or unknown kind, a kustomization or applier object that transforms the artifact or an artifact it could not download.';
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

/**
 * impactText is the pacto.impact verdict a history line's record carries, in
 * words: recorded (and signed) only on a record line of a verified history,
 * otherwise "signature not checked". A line without a verdict has none.
 */
export function impactText(verified: boolean | undefined, en: { record?: string; kind?: string; finished?: string; impact?: { result?: string; message?: string } | null }): string {
  if (!en.impact) return '';
  const head = verified && en.record && en.kind === 'operation'
    ? `Impact recorded${en.finished ? ` ${timeLabel(en.finished)}` : ''}` : 'Impact recorded, signature not checked';
  return `${head}: ${[en.impact.result, en.impact.message].filter(Boolean).join(' ')}`;
}

/**
 * sentence turns a server note into a sentence: capitalised, digests short, a
 * full stop. A note that starts with an identifier (k8s:pay/x, sha256:...)
 * keeps it as written.
 */
export function sentence(text: string | undefined): string {
  const t = shortDigests(text).trim();
  if (!t) return '';
  const lead = /^[a-z]+(?=[\s,;]|$)/.test(t) ? t[0].toUpperCase() + t.slice(1) : t;
  return lead + (/[.!?]$/.test(t) ? '' : '.');
}

/**
 * shortDigest keeps the first 12 hex characters of an OCI digest. Anything that
 * is not one (a level name like auto-nonbreaking) is kept whole: cutting it
 * would only make it unreadable.
 */
export function shortDigest(d: string | undefined): string {
  if (!d) return '';
  const hex = d.replace(/^sha256:/, '');
  return /^[0-9a-f]{13,}$/i.test(hex) ? hex.slice(0, 12) : hex;
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

/**
 * cliDecision is the command that takes a decision a dashboard cannot. It
 * uses the kubeconfig the dashboard runs with, which works whether or not
 * the installation accepts signed approvals: the CLI signs keyless when it
 * does. The --intent form needs --trust and --signers, so it is left to
 * `pacto delivery record get <intent ref>`, which prints it.
 */
export function cliDecision(namespace: string, name: string, decision: string): string {
  return `pacto delivery approve -n ${namespace} ${name} --decision ${decision}`;
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

/**
 * miraServiceLink opens a service's spans in Mira's trace view over the last
 * `range` (one of Mira's own ranges). Mira matches by service name only.
 * Null without a web address.
 */
export function miraServiceLink(name: string, miraURL: string | undefined, range = '-1h'): string | null {
  if (!miraURL || !/^https?:\/\//i.test(miraURL)) return null;
  const base = miraURL.replace(/\/+$/, '');
  return `${base}/#/traces?q=${encodeURIComponent(`service.name="${name}"`)}&range=${encodeURIComponent(range)}`;
}

/**
 * entityMiraURL is the Mira address a service page may link to: only for a
 * caller whose permissions hide nothing. Mira has no sign-in and matches by
 * name, so a hidden same-named service would be one click away.
 */
export function entityMiraURL(caps: { miraURL?: string; auth?: { hidden?: boolean } } | null | undefined): string {
  return caps && !caps.auth?.hidden ? caps.miraURL ?? '' : '';
}

export const OUTCOMES: Record<string, string> = { Succeeded: 'succeeded', Failed: 'failed', Rejected: 'was rejected', Errored: 'could not finish' };
export const VERIFICATIONS: Record<string, string> = { Verified: 'verified', Unverified: 'not verified', AcceptedUnknown: 'a person accepted the unknown' };
export const PHASES: Record<string, string> = { Held: 'waits for a person', Pending: 'waits its turn', Running: 'is running', Verifying: 'is being checked', '': 'is not admitted yet' };

/** verificationLabel says how a change's checks ended, in words. */
export function verificationLabel(v: string | undefined): string {
  const w = VERIFICATIONS[v ?? ''] ?? v ?? '';
  return w ? w[0].toUpperCase() + w.slice(1) : '';
}

/** outcomeTone is the badge colour of how a change ended. */
export function outcomeTone(outcome: string | undefined): string {
  if (outcome === 'Succeeded') return 'badge-ok';
  return outcome === 'Failed' || outcome === 'Errored' || outcome === 'Rejected' ? 'badge-err' : 'badge-neutral';
}

/**
 * reasonAfter is a reason without the codes already shown next to it: the
 * controller writes "AcceptedUnknown: ..." where the verification already
 * says AcceptedUnknown, and a line must not say it twice.
 */
export function reasonAfter(reason: string | undefined, ...shown: (string | undefined)[]): string {
  let r = (reason ?? '').trim();
  for (let c = shown.find((x) => x && (r === x || r.startsWith(`${x}: `))); c; c = shown.find((x) => x && (r === x || r.startsWith(`${x}: `)))) {
    r = r.slice(c.length).replace(/^: /, '');
  }
  return r;
}

const REASON_WORDS: Record<string, string> = {
  ApprovalRequired: 'Waiting for a person to approve',
  AwaitingHuman: 'Waiting for a person',
  AcceptedUnknown: 'A person accepted the unknown',
  RejectedByHuman: 'A person rejected it',
  VerifyFailed: 'A check failed after release',
  VerifyUnknown: 'A check after release ended Unknown',
  VerifyUndecided: 'The checks after release did not decide',
  ApplyFailed: 'The release did not apply',
  RecheckChanged: 'A gate no longer passes',
  Unguarded: 'Waiting for the admission policies',
  Waiting: 'Waiting',
};

/**
 * reasonWords says a controller reason code in plain words: the known codes
 * by name, any other code split into words (ExecutionDeadline reads
 * "Execution deadline"). A reason that is already a sentence is kept.
 */
export function reasonWords(reason: string | undefined): string {
  const r = reason ?? '';
  if (!/^[A-Z][A-Za-z]+$/.test(r)) return r;
  return REASON_WORDS[r] ?? r.replace(/(?<=[a-z])(?=[A-Z])/g, ' ').replace(/ ([A-Z])/g, (_, c) => ` ${c.toLowerCase()}`);
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

const CLASS_SOURCES: Record<string, string> = {
  'service-account': 'service account',
  'agent-group': 'agent group',
  'outside-human-groups': 'outside humanGroups',
  'break-glass-group': 'break-glass group',
  controller: 'the promoter',
  'system-user': 'system user',
  user: 'signed-in user',
};

/** classSourceLabel says how an actor was classified; a record from an earlier promoter kept nothing, and says so. */
export function classSourceLabel(source: string | undefined): string {
  return CLASS_SOURCES[source ?? ''] ?? (source || 'classification not recorded');
}

const ACCESS_SOURCES: Record<string, string> = {
  'subject-access-review': 'checked by the promoter',
  'self-subject-access-review': "checked with the caller's own credentials",
};

/** accessSourceLabel says who checked that an approver may update the workload. */
export function accessSourceLabel(source: string | undefined): string {
  return ACCESS_SOURCES[source ?? ''] ?? (source || 'not recorded');
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
  const done = entries.filter((e) => e.type === 'delivery.pacto.trianalab.io/promote' && e.kind !== 'gap' && e.kind !== 'unrecordable' && e.phase === 'Concluded');
  if (done.some((e) => e.approvals === undefined)) return null;
  const unknown = (e: AutonomyEntry) => e.verification === 'AcceptedUnknown' || (e.approvals ?? []).some((a) => a.decision === 'accept-unknown');
  return {
    total: done.length,
    noPerson: done.filter((e) => e.actorClass !== 'human' && !(e.approvals ?? []).length && !unknown(e)).length,
    onlyUnknown: done.filter((e) => unknown(e) && !(e.approvals ?? []).some((a) => a.decision === 'approve')).length,
  };
}

/**
 * undoCause says why the controller undid a failed change, read from its
 * reason: the controller writes the code in reason and the rest in message
 * (RejectedByHuman, "carol: note"); the demo writes it all in reason.
 * `by` is set when a person rejected the unknown.
 */
export function undoCause(reason?: string, message?: string): { by?: string; text?: string } {
  const [code, ...rest] = [reason, message].filter(Boolean).join(': ').split(': ');
  if (code === 'RejectedByHuman') return rest[0] ? { by: rest[0] } : { text: 'a person rejected the unknown' };
  if (code === 'VerifyFailed') return { text: 'a check failed after release' };
  if (code === 'ApplyFailed') return { text: 'the release did not apply' };
  return {};
}

const PREDICTIONS: Record<string, string> = {
  Proceeds: 'It would go ahead with no person involved.',
  Held: 'It would wait for a person to decide.',
  Rejected: 'It would be refused.',
  Waits: 'It would wait until a person unfreezes it.',
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

/**
 * The checks that gather evidence: they pass once they ran, whatever they
 * found. What they found that holds the change is in the plan's question.
 */
const EVIDENCE_GATES = new Set(['pacto.diff', 'pacto.impact', 'pacto.environment']);

const CLASSIFICATION_CHIPS: Record<string, string> = { BREAKING: 'Breaking', POTENTIAL_BREAKING: 'May break' };

export interface VerdictBadge {
  label: string;
  tone: string;
  /** chip names what an evidence check found that a person must weigh, else ''. */
  chip: string;
}

/**
 * verdictBadge says what a check concluded. A diff, impact or environment
 * check passes as soon as it ran, so its Pass reads Checked, never a green
 * Pass, and what it found that can break a consumer gets its own warning chip.
 * A check that does not apply to the version reads N/A.
 */
export function verdictBadge(v: { gate?: string; result?: string; citation?: Record<string, string> }): VerdictBadge {
  const chip = CLASSIFICATION_CHIPS[v.citation?.classification ?? ''] ?? '';
  if (v.citation?.applicable === 'false') return { label: 'N/A', tone: 'badge-neutral', chip };
  if (v.result === 'Pass' && EVIDENCE_GATES.has(v.gate ?? '')) return { label: 'Checked', tone: 'badge-neutral', chip };
  // Unknown is neutral, never warn: a check that could not decide is an open question.
  const tone = ({ Pass: 'badge-ok', Fail: 'badge-err' } as Record<string, string>)[v.result ?? ''] ?? 'badge-neutral';
  return { label: v.result ?? '', tone, chip };
}

const GATE_LABELS: Record<string, string> = {
  'pacto.diff': 'Contract diff',
  'pacto.impact': 'Consumer impact',
  'pacto.contract': 'Contract readable',
  'pacto.published': 'Published and signed',
  'pacto.suites': 'Test suites shipped',
  'pacto.environment': 'Environment values',
  'pacto.autonomy': 'Level change',
  'pacto.runtime': 'Runtime callers',
  'pacto.preempted': 'Superseded',
  'pacto.applier': 'Applier owns the workloads',
  'pacto.manifests': 'Objects stay in the Stage namespace',
};

// ── Who applies a Stage's workloads ─────────────────────────────────────────

/**
 * The promoter rewrites a Stage's applier asOf at least once per resync and
 * stamps staleAt twice its resync later; a status without staleAt falls back
 * to twice the default resync (delivery.ApplierStaleAfter).
 */
const APPLIER_STALE_MS = 2 * 60_000;

interface ApplierLine {
  /** display is the one manager string every surface prints. */
  display: string;
  /** problem says in words why the Stage does not adopt it, else ''. */
  problem: string;
  /** asOf is when the promoter last looked, stale when it stopped, else ''. */
  asOf: string;
  warn: boolean;
  text: string;
}

/**
 * applierLine is a Stage's applier as one line says it, or null when the
 * promoter reports none. Only Adopted and Unmanaged read as plain: Conflict
 * and Unknown say why in visible words, and so does a stale observation.
 * now is the server's clock (the listing's asOf), so a browser clock that
 * runs ahead never marks a healthy promoter stale. ready is the row's Ready
 * condition: when the applier is itself the reason, the Cannot promote line
 * already says why, so the problem is not repeated.
 */
/** serverNow is the listing's asOf as a clock, or the browser's without one. */
export function serverNow(asOf: string | undefined): number {
  return Date.parse(asOf ?? '') || Date.now();
}

export function applierLine(a: { state?: string; display?: string; manager?: string; message?: string; asOf?: string; staleAt?: string } | null | undefined,
  now = Date.now(), ready = ''): ApplierLine | null {
  if (!a) return null;
  const bad = a.state === 'Conflict' || a.state === 'Unknown';
  const staleAt = a.staleAt ? Date.parse(a.staleAt) : Date.parse(a.asOf ?? '') + APPLIER_STALE_MS;
  const stale = !!a.asOf && now > staleAt;
  const display = a.display || a.manager || 'not known';
  const said = /^False: Applier(Conflict|Unknown)\b/.test(ready);
  const problem = bad && !said ? `${a.state!.toLowerCase()}: ${a.message ?? ''}` : '';
  const asOf = a.asOf ? `as of ${timeLabel(a.asOf)}${stale ? ', stale: the promoter has not checked since' : ''}` : '';
  return { display, problem, asOf, warn: bad || stale, text: `Applier: ${display}${problem ? `, ${problem}` : ''}${asOf ? ` (${asOf})` : ''}` };
}

/** gateLabel names a built-in check in plain words; a check the Stage names itself keeps its own name. */
export function gateLabel(gate: string | undefined): string {
  return GATE_LABELS[gate ?? ''] ?? (gate || 'Check');
}

/** isBreaking says whether a plan's checks classified the change as breaking. */
export function isBreaking(verdicts: { citation?: Record<string, string> }[] | null | undefined): boolean {
  return (verdicts ?? []).some((v) => v.citation?.classification === 'BREAKING');
}

/**
 * andList joins names the way a sentence does, with no serial comma. Past max
 * it names the first max and counts the rest ("a, b, c and 297 others"), so
 * one sentence never runs to a page; word is the joining word ("or").
 */
export function andList(names: string[], max = Infinity, word = 'and'): string {
  const xs = names.length > max + 1 ? [...names.slice(0, max), `${formatCount(names.length - max)} others`] : names;
  return xs.length < 2 ? (xs[0] ?? '') : `${xs.slice(0, -1).join(', ')} ${word} ${xs[xs.length - 1]}`;
}

/**
 * placedByName says why some environments of a fleet figure are placed by name
 * (SPEC 21): a Stage there is on a cycle of upstream links, or different services
 * order them both ways, which is no cycle. Empty when the links place them all.
 */
export function placedByName(byEnv: { environment: string; cycle?: boolean; unordered?: boolean }[]): string {
  const cyc = byEnv.filter((e) => e.cycle).map((e) => e.environment);
  const both = byEnv.filter((e) => e.unordered).map((e) => e.environment);
  const placed = (n: number) => (n === 1 ? 'it is' : 'they are');
  return [
    cyc.length ? `A Stage in ${andList(cyc, 3)} is on a cycle of upstream links, so ${placed(cyc.length)} placed by name.` : '',
    both.length ? `Services order ${andList(both, 3)} both ways, so ${placed(both.length)} placed by name.` : '',
  ].filter(Boolean).join(' ');
}

/**
 * The `except` URL parameter: the environments a figure draws, which its folded
 * station leaves out so its counts open exactly what they count. It travels as one
 * JSON array, so any environment name (commas included) survives the round trip.
 */
export function exceptParam(envs: string[] | undefined): string | undefined {
  return envs?.length ? JSON.stringify(envs) : undefined;
}

/** exceptList reads the `except` URL parameter; anything but an array of strings is no filter. */
export function exceptList(raw: string): string[] {
  try {
    const v: unknown = JSON.parse(raw);
    return Array.isArray(v) && v.every((x) => typeof x === 'string') ? v : [];
  } catch {
    return [];
  }
}

// ── The autonomy ladder ─────────────────────────────────────────────────────

/** The operation type that moves a Stage's level, one level at a time. */
export const AUTONOMY_TYPE = 'delivery.pacto.trianalab.io/autonomy';

const LEVELS: Record<string, { label: string; help: string }> = {
  observe: { label: 'Observe', help: 'Pacto plans every change and moves nothing. GitOps applies what git says.' },
  advise: { label: 'Advise', help: 'Pacto shows each candidate and its plan. A person proposes or approves every promote.' },
  'auto-nonbreaking': { label: 'Auto non-breaking', help: 'Pacto promotes non-breaking changes on its own. A person approves every other change.' },
  autonomous: { label: 'Autonomous', help: 'Pacto promotes any change the contract graph clears. Unknown still waits for a person.' },
};

/** levelLabel names a level in plain words; an empty level is one the Stage does not report. */
export function levelLabel(level: string | undefined): string {
  return LEVELS[level ?? '']?.label ?? (level || 'Not reported');
}

/** levelHelp says what Pacto does on a level, in a sentence. */
export function levelHelp(level: string | undefined): string {
  return LEVELS[level ?? '']?.help ?? 'This Stage does not report a level. An operator older than the autonomy ladder writes none.';
}

/** The fields of a would-have decision shadowLabel reads. */
export interface ShadowFacts {
  predicted?: string;
  owners?: string[] | null;
  question?: string;
  observedAt?: string;
}

/**
 * shadowLabel says what a fully automatic Stage would have done with a
 * candidate: ship, hold for the teams it would ask, or ask the question.
 */
export function shadowLabel(s: ShadowFacts): string {
  switch (s.predicted) {
    case 'Proceeds': return 'ship';
    case 'Held': return s.owners?.length ? `hold for ${andList(s.owners, 3)}` : `ask: ${s.question || 'a person decides'}`;
    case 'Rejected': return 'refuse';
    case 'Waits': return 'wait until a person unfreezes it';
    default: return s.predicted || 'not planned yet';
  }
}

/** heldButRan: GitOps' source reported a candidate Pacto would not have shipped on its own. */
export function heldButRan(s: ShadowFacts): boolean {
  return !!s.observedAt && s.predicted !== 'Proceeds';
}

const BLOCKERS: Record<string, string> = {
  'missing-contract': 'No contract',
  'undeclared-range': 'No version range declared',
  'unlabelled-apply-ref': 'GitOps can still write the version',
  'no-observed-digest': 'The running version is not known',
  'no-record-store': 'Signed records are off',
  'needs-verified': 'Needs more verified releases',
  'needs-cleared-breaking': 'Needs a breaking change the graph cleared',
  incident: 'A release failed here',
  'partial-graph': 'The contract graph is partial',
};

/**
 * blockerService is the service a blocker is about, or '' when its subject is
 * something else: a blocker's subject can also be a GitOps object, a version
 * digest, an operation UID or a caller with no contract (`k8s:ns/Kind/name`).
 */
// ponytail: shape-based, because the wire carries one subject string; a typed
// subject kind on the blocker would replace this guess.
export function blockerService(b: { kind?: string; subject?: string }): string {
  const s = b.subject ?? '';
  if (!s || b.kind === 'unlabelled-apply-ref' || b.kind === 'no-observed-digest') return '';
  if (s.includes(':') || /^[0-9a-f]{8}-[0-9a-f]{4}-/i.test(s)) return '';
  return s;
}

/**
 * blockerReason is a blocker's message for its one-line row, where the subject is
 * already printed beside it: "orders calls cart at runtime but has no contract" reads
 * "It calls cart at runtime but has no contract" and "incident for orders" reads
 * "incident". The row's detail keeps the whole message.
 */
export function blockerReason(message: string | undefined, subject: string | undefined): string {
  const m = message ?? '';
  if (!subject || !m.includes(subject)) return m;
  if (m.startsWith(`${subject} `)) return `It ${m.slice(subject.length + 1)}`;
  const esc = subject.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return m.replace(new RegExp(`\\s+(?:for|of|on|in|by)\\s+${esc}(?=$|[\\s.,;:])`), '');
}

/** blockerLabel names what keeps a Stage from the next level. */
export function blockerLabel(kind: string | undefined): string {
  return BLOCKERS[kind ?? ''] ?? (kind || 'Blocker');
}

/**
 * proposalText says what Pacto proposes for a Stage and what earned it, or
 * that it proposes nothing yet.
 */
export function proposalText(level: string | undefined, proposal: { level?: string; evidence?: string[] | null } | null | undefined): string {
  if (!proposal?.level) return 'Nothing to propose yet.';
  const n = proposal.evidence?.length ?? 0;
  const what = level === 'observe' ? `version${n === 1 ? '' : 's'} GitOps ran that verified` : `verified promotion${n === 1 ? '' : 's'}`;
  return `Pacto proposes ${levelLabel(proposal.level)}: ${n} ${what}, no incident.`;
}

const OFFERS: Record<DeliveryOffer['action'], string> = { promote: 'Promote', unfreeze: 'Unfreeze', freeze: 'Freeze', undo: 'Undo' };

/** offerLabel names an action a Stage row offers. */
// samePerson compares user names the way the promoter's approval rule does:
// the on-behalf-of name is asserted text, so case and padding do not count.
export function samePerson(a: string | undefined, b: string | undefined): boolean {
  return !!a?.trim() && a.trim().toLowerCase() === b?.trim().toLowerCase();
}

export function offerLabel(action: string): string {
  return OFFERS[action as DeliveryOffer['action']] ?? action;
}

type OfferRow = { namespace: string; stage: string; pointer?: string; candidate?: string; candidateVersion?: string };

/**
 * offerRequest is the propose request an offer opens (promote, unfreeze or freeze; undo
 * opens the undo panel instead) and the version a promote names, known only for the
 * Stage's candidate. Offers only describe: the request is planned, then proposed with
 * the caller's own identity, and admission decides.
 */
export function offerRequest(r: OfferRow, o: DeliveryOffer) {
  const request: { namespace: string; stage: string; type: string; to?: string; from?: string } = { namespace: r.namespace, stage: r.stage, type: `delivery.pacto.trianalab.io/${o.action}` };
  const to = o.to || r.candidate;
  if (o.action === 'promote') Object.assign(request, { to, from: r.pointer || undefined });
  return { request, version: o.action === 'promote' && to === r.candidate ? (r.candidateVersion ?? '') : '' };
}

type StageRows = DeliveryEnvironments['environments'];

/**
 * stageRowsOf is a service's Stage rows, for the offers an investigation page shows;
 * none when this dashboard serves no delivery or the rows cannot be read: the page's
 * own Delivery section says why, and an offer is never guessed.
 */
export async function stageRowsOf(service: string, domain: string): Promise<StageRows> {
  try {
    const d = await api.deliveryEnvironments({ service, domain, limit: 1 });
    return (d?.environments ?? []).filter((r) => r.service === service && (!r.domain || r.domain === domain));
  } catch { return []; }
}

/**
 * artifactOf is the one artifact digest of the service's Stages whose contract is
 * `contract` (a revision's digest), '' when none, several or it cannot be read. A
 * promote is matched by it, never by a version label: two artifacts can carry one.
 */
export async function artifactOf(service: string, domain: string, contract: string): Promise<string> {
  if (!contract) return '';
  try { return (await api.deliveryLifecycle({ service, domain, contract }))?.digest ?? ''; } catch { return ''; }
}

/** promotes keeps the promote offer of artifact `digest`, compared as offerRequest does. */
export const promotes = (digest: string) => (r: OfferRow, o: DeliveryOffer) =>
  !!digest && o.action === 'promote' && (o.to || r.candidate) === digest;

/** undoes keeps the undo offer of a Stage that runs artifact `digest`. */
export const undoes = (digest: string) => (r: OfferRow, o: DeliveryOffer) =>
  !!digest && o.action === 'undo' && r.pointer === digest;

/**
 * lifecycleWords words a lifecycle answer: the signature as the Publish page words it
 * ("unsigned" only for an artifact no publisher check accepted), and unknown never as yes or no.
 */
export const lifecycleWords = {
  signature: { signed: 'Signature checked', unsigned: 'Not signed', unknown: 'Signature not known here' },
  verified: { yes: 'verified', no: 'not verified', unknown: 'verified: not known' },
  failed: { yes: 'failed', no: '', unknown: 'failed: not known' },
  role: { arrived: 'arrived', left: 'moved off it', attempted: 'attempted, moved nothing' },
} as const;

/**
 * checkText says when the newest re-check of a Stage ran and its worst
 * verdict. A check about an earlier version or change reads stale: its age
 * comes from when it ran, never from now.
 */
export function checkText(c: { at?: string; result?: string; current?: boolean } | null | undefined): string {
  if (!c) return '';
  return `Re-checked ${timeLabel(c.at)}, ${c.result}${c.current ? '' : ', stale: about an earlier version'}`;
}

const WATCH: Record<string, string> = { True: 'Watched: the newest re-check passed', False: 'The newest re-check failed' };

/** watchText says whether the running version still passes its re-checks. */
export function watchText(w: { status?: string; reason?: string } | null | undefined): string {
  if (!w) return '';
  if (w.reason === 'NotRechecked') return 'Not re-checked since it shipped';
  return WATCH[w.status ?? ''] ?? 'The newest re-check could not decide';
}

const PROGRESS_STAMP: Record<string, { label: string; tone: string }> = {
  'up-to-date': { label: 'Up to date', tone: 'ok' },
  'can-promote': { label: 'Can promote', tone: 'accent' },
  'needs-person': { label: 'Needs a person', tone: 'warn' },
  blocked: { label: 'Blocked', tone: 'err' },
};

/** progressStamp is the AnswerCard stamp for where a Stage stands against its candidate; anything else is not known. */
export function progressStamp(state: string | undefined): { label: string; tone: string } {
  return PROGRESS_STAMP[state ?? ''] ?? { label: 'Not known', tone: 'neutral' };
}

/** isCommand says whether a fix is a pacto command to copy rather than words about who acts. */
export function isCommand(fix: string | undefined): boolean {
  return !!fix?.startsWith('pacto ');
}

/**
 * causeText says why a change was raised, as its intent said: the reason, else the
 * check whose verdict raised it, else the change that caused it.
 */
export function causeText(c: { reason?: string; operation?: string; verdict?: { gate: string; message?: string } | null } | null | undefined): string {
  if (!c) return '';
  if (c.reason) return c.reason;
  if (c.verdict) return c.verdict.message ? `${c.verdict.gate}: ${c.verdict.message}` : c.verdict.gate;
  return c.operation ? `operation ${c.operation}` : '';
}

/** The windows the history offers, as the time before now each keeps. */
export const HISTORY_WINDOWS = [
  { key: '24h', label: 'Last 24 hours', ms: 864e5 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 864e5 },
  { key: '30d', label: 'Last 30 days', ms: 30 * 864e5 },
] as const;

/**
 * historyWindow names the window a history URL holds: all with no since, custom for a
 * timeline slice (until or timed) or a since no window names.
 */
export function historyWindow(since: string, until: string, timed: string, now = Date.now()): string {
  if (until || timed) return 'custom';
  if (!since) return 'all';
  const age = now - Date.parse(since);
  // ponytail: within a tenth of the window, so a page left open a while keeps its name; a since entered by hand that lands there reads as the window
  return HISTORY_WINDOWS.find((w) => Math.abs(age - w.ms) <= w.ms / 10)?.key ?? 'custom';
}

/** windowSince is the RFC 3339 since a window keeps changes from; none for all. */
export function windowSince(key: string, now = Date.now()): string | undefined {
  const w = HISTORY_WINDOWS.find((x) => x.key === key);
  return w ? new Date(now - w.ms).toISOString().replace(/\.\d+Z$/, 'Z') : undefined;
}

type EnvironmentRowOf = NonNullable<DeliveryEnvironments['environments']>[number];
type InboxLadder = NonNullable<DeliveryInbox['ladder']>;

/** Where an environment comes from, as the provenance mark's tip says it. */
const ENVIRONMENT_SOURCE: Record<string, string> = {
  declared: "the contract's environments[]",
  ladder: 'the installation ladder the chart declares',
  'inferred-confirmed': 'an inferred rung of the installation ladder a person confirmed',
  'hand-written': 'a Stage a person wrote',
};

/** placementWords is a placement in words: its provenance, evidence and confirmer. */
function placementWords(p: { provenance?: string; evidence?: string; confirmedBy?: string } | null | undefined): string {
  if (!p?.provenance) return '';
  return `placement ${p.provenance}${p.evidence ? ` (${p.evidence})` : ''}${p.confirmedBy ? `, confirmed by ${p.confirmedBy}` : ''}`;
}

/**
 * placementTip is the tip of a Stage's one provenance mark: where its environment comes
 * from and where its service runs there and why. Empty where the promoter publishes no
 * installation ladder, so the row shows no mark.
 */
export function placementTip(r: Pick<EnvironmentRowOf, 'source' | 'placement'>): string {
  if (!r.source) return '';
  const from = `From ${ENVIRONMENT_SOURCE[r.source] ?? r.source}.`;
  const words = placementWords(r.placement);
  if (!words) return from;
  const ns = r.placement?.namespace;
  return `${from} ${ns ? `In namespace ${ns}, ${words}` : `P${words.slice(1)}`}.`;
}

/** ladderCommand is the command that confirms exactly the proposal the inbox shows. */
export function ladderCommand(l: Pick<InboxLadder, 'command' | 'proposalDigest'>): string {
  return l.command || `pacto delivery ladder confirm --proposal ${l.proposalDigest}`;
}
