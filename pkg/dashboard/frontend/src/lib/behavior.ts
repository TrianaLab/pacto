/**
 * Words for the behavior half of a contract (pactoVersion 2.2 and 2.3): what an
 * interface promises, what a dependency requires, whether the provider meets it, an
 * interface's retirement schedule and an environment's check. Every verdict here was
 * computed by the backend (pkg/fleet, pkg/impact); these helpers only name it.
 */
import type { components } from './generated/schema.ts';
import { formatDate, todayUTC } from './dateFormat.ts';

type Guarantees = components['schemas']['Contract.Guarantees'];
type Lifecycle = components['schemas']['Contract.InterfaceLifecycle'];
type Mismatch = components['schemas']['Contract.Mismatch'];
export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'neutral';

// The closed vocabulary, in the order the contract reference lists it. `bound` is how an
// offered duration has to compare with a required one.
const TERMS: Array<{ key: keyof Guarantees; label: string; bound: string }> = [
  { key: 'delivery', label: 'Delivery', bound: '' },
  { key: 'ordering', label: 'Ordering', bound: '' },
  { key: 'dedupeWindow', label: 'Dedupe window', bound: 'at least ' },
  { key: 'retention', label: 'Retention', bound: 'at least ' },
  { key: 'timeout', label: 'Timeout', bound: 'at most ' },
  { key: 'idempotent', label: 'Idempotent', bound: '' },
];
const LABEL: Record<string, string> = Object.fromEntries(TERMS.map((t) => [t.key, t.label]));
const BOUND: Record<string, string> = Object.fromEntries(TERMS.map((t) => [t.key, t.bound]));
const yes = (v: unknown) => (v === true || v === 'true' ? 'yes' : String(v));

export interface Term { label: string; bound: string; value: string }

/** guaranteeTerms lists the guarantees an interface offers, or (`required`) a dependency
 *  requires, with each required duration's bound spelled out. */
export function guaranteeTerms(g: Partial<Guarantees> | null | undefined, required = false): Term[] {
  if (!g) return [];
  return TERMS.filter((t) => g[t.key]).map((t) => ({ label: t.label, bound: required ? t.bound.trim() : '', value: yes(g[t.key]) }));
}

/** mismatchText names one guarantee a provider refuses. */
export function mismatchText(m: Mismatch): string {
  if (m.guarantee === 'interface') return `${m.interface}: the provider declares no such interface`;
  const what = LABEL[m.guarantee] ?? m.guarantee;
  return `${m.interface}: ${what} ${BOUND[m.guarantee] ?? ''}${yes(m.required)} required, ${m.offered ? `${yes(m.offered)} offered` : 'nothing offered'}`;
}

const GUARANTEE: Record<string, { label: string; tone: Tone; title: string }> = {
  met: { label: 'Guarantees met', tone: 'ok', title: 'The provider offers everything this dependency requires.' },
  unmet: { label: 'Refused', tone: 'err', title: 'The provider does not offer what this dependency requires.' },
  unknown: { label: 'Not verified', tone: 'neutral', title: 'Pacto could not check what this dependency requires.' },
};

/** guaranteeBadge names a dependency's guarantee status. Refused and Not verified stay
 *  apart: a check that could not run is Unknown, never a refusal. */
export function guaranteeBadge(status: string | null | undefined, reason = ''): { label: string; tone: Tone; title: string } | null {
  const b = status ? GUARANTEE[status] : undefined;
  if (!b) return null;
  return status === 'unknown' && reason ? { ...b, title: `Not verified: ${reason}.` } : b;
}

/** sunsetPassed reports whether today (UTC) is on or after the sunset date, as the
 *  backend's SunsetReached does. Dates are YYYY-MM-DD, so they compare as text. */
export function sunsetPassed(l: Partial<Lifecycle> | null | undefined, today: string = todayUTC()): boolean {
  return !!l?.sunset && today >= l.sunset;
}

/** lifecycleBadge names a deprecated interface's state; it is louder once the sunset has
 *  passed, when the interface may be removed in any release. */
export function lifecycleBadge(l: Partial<Lifecycle> | null | undefined, today: string = todayUTC()): { label: string; tone: Tone; title: string } | null {
  if (!l?.deprecated) return null;
  return sunsetPassed(l, today)
    ? { label: 'Past sunset', tone: 'err', title: lifecycleText(l) + '. It may be removed in any release.' }
    : { label: 'Deprecated', tone: 'warn', title: lifecycleText(l) + '.' };
}

/** lifecycleText is the schedule as one line: "Deprecated Jan 1, 2026 · sunset Jun 1, 2026 · successor orders-v2". */
export function lifecycleText(l: Partial<Lifecycle>): string {
  const parts = [`Deprecated ${formatDate(l.deprecated) || l.deprecated}`];
  if (l.sunset) parts.push(`sunset ${formatDate(l.sunset) || l.sunset}`);
  if (l.successor) parts.push(`successor ${l.successor}`);
  return parts.join(' · ');
}

const ENVIRONMENT: Record<string, { label: string; tone: Tone }> = {
  pass: { label: 'Passed', tone: 'ok' },
  fail: { label: 'Failed', tone: 'err' },
  unverified: { label: 'Not verified', tone: 'neutral' },
  skip: { label: 'Not checked', tone: 'neutral' },
};

/** environmentBadge names an environment's check result. */
export function environmentBadge(status: string | null | undefined): { label: string; tone: Tone } {
  return ENVIRONMENT[status ?? ''] ?? { label: 'Unknown', tone: 'neutral' };
}

const STAGE: Record<string, { word: string; tone: Tone }> = {
  pass: { word: 'passed', tone: 'ok' },
  fail: { word: 'failed', tone: 'err' },
  skip: { word: 'not run', tone: 'neutral' },
};
const STAGE_NAME: Record<string, string> = { structural: 'Structure', 'cross-field': 'Cross-field', policy: 'Policy', environments: 'Environments' };

/** stageBadge names one validation stage and how it ended. */
export function stageBadge(st: { name: string; status: string; reason?: string }): { label: string; tone: Tone; title: string } {
  const s = STAGE[st.status] ?? { word: st.status, tone: 'neutral' as Tone };
  const name = STAGE_NAME[st.name] ?? st.name;
  return { label: `${name} ${s.word}`, tone: s.tone, title: st.reason ? `${name}: ${st.reason}` : `${name} ${s.word}` };
}

/** locationText is where a finding points in its file: "envs/prod.yaml:3:5". */
export function locationText(f: { file?: string; line?: number; column?: number }): string {
  if (!f.file) return '';
  return f.file + (f.line ? `:${f.line}${f.column ? `:${f.column}` : ''}` : '');
}
