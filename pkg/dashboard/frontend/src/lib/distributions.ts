/**
 * Backend tallies -> visualization segments.
 *
 * Every product surface that draws a compliance, revision-match or severity
 * distribution reads its buckets from here, so the label wording and the tone of a
 * given state are decided ONCE. Drawing the same four compliance states with
 * different words or different colours on the overview and on a service page is how
 * a taxonomy stops teaching itself.
 *
 * These functions only reshape counts the backend already computed over a COMPLETE
 * population. They never derive a bucket, infer a state, or accept a truncated
 * preview's Items as a population: no semantics are invented in the frontend.
 */

import { statusLabel } from './format.ts';

export interface Segment {
  label: string;
  value: number;
  tone: 'ok' | 'warn' | 'err' | 'info' | 'neutral';
  href?: string;
}

/**
 * A bucket state: the wire value the Entities API filters by, the tally field it is
 * counted in, and the one wording every surface shows it under.
 *
 * `value` and `field` are kept together on purpose. A legend row and the filter it
 * drills into MUST describe the same slice, and the surest way to guarantee that is for
 * the chart, the filter <select> and the active-filter chip to read the pair from here
 * rather than each spelling its own copy.
 */
export interface BucketState<F> {
  value: string;
  field: F;
  label: string;
  tone: Segment['tone'];
}

function stateSegments<F extends string>(
  states: BucketState<F>[],
  tally: Partial<Record<F, number>> | undefined,
  hrefs: Record<string, string>,
): Segment[] {
  const t: Partial<Record<F, number>> = tally || {};
  return states.map((s) => ({ label: s.label, value: n(t[s.field]), tone: s.tone, href: hrefs[s.field] }));
}

/** The label a bucket state is always shown under, for a filter chip or an <option>. */
export function bucketLabel<F extends string>(states: BucketState<F>[], value: string): string {
  return states.find((s) => s.value === value)?.label || value;
}

/** ComplianceTally as emitted by the Product API (pkg/fleet/aggregate.go). */
export interface ComplianceTally {
  compliant?: number;
  nonCompliant?: number;
  unknown?: number;
  warning?: number;
  invalid?: number;
  reference?: number;
  notEvaluated?: number;
  /** A status value this build does not know. Never a canonical state. */
  other?: number;
}

/** LinkTally as emitted by the Product API (revision-match certainty). */
export interface LinkTally {
  exact?: number;
  inferred?: number;
  ambiguous?: number;
  unresolved?: number;
}

/** SeverityTally as emitted by the Product API. */
export interface SeverityTally {
  errors?: number;
  warnings?: number;
  infos?: number;
  unknown?: number;
}

const n = (v: number | undefined) => v || 0;

/**
 * Every canonical compliance state, plus the catch-all, in the order always shown.
 *
 * The label is `statusLabel`'s, never a second spelling: a legend reading
 * "Non-Compliant" above rows badged "Not compliant" asks the reader to believe those
 * are two states. "Unknown" is deliberately toned warn, not neutral: a target we cannot
 * evaluate is an open question, and painting it grey beside a green Compliant reads as
 * benign. "Not evaluated" IS neutral -- nothing is running to evaluate.
 */
export const COMPLIANCE_STATES: BucketState<keyof ComplianceTally>[] = [
  { value: 'Compliant', field: 'compliant', label: statusLabel('Compliant'), tone: 'ok' },
  { value: 'NonCompliant', field: 'nonCompliant', label: statusLabel('NonCompliant'), tone: 'err' },
  { value: 'Unknown', field: 'unknown', label: statusLabel('Unknown'), tone: 'warn' },
  { value: 'Warning', field: 'warning', label: statusLabel('Warning'), tone: 'warn' },
  { value: 'Invalid', field: 'invalid', label: statusLabel('Invalid'), tone: 'err' },
  { value: 'Reference', field: 'reference', label: statusLabel('Reference'), tone: 'info' },
  { value: 'NotEvaluated', field: 'notEvaluated', label: statusLabel('NotEvaluated'), tone: 'neutral' },
  // No wire value: "Other" is by definition not a status this build can filter for.
  { value: '', field: 'other', label: 'Other', tone: 'neutral' },
];

export function complianceSegments(t: ComplianceTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(COMPLIANCE_STATES, t, hrefs);
}

/**
 * statusHrefs builds the drill-down for every compliance bucket that IS a real filter,
 * from one `status` URL builder. Callers used to hand-list three or four buckets, so a
 * fleet that was mostly "Not evaluated" drew its largest slice as a dead end.
 */
export function statusHrefs(url: (status: string) => string): Record<string, string> {
  return Object.fromEntries(COMPLIANCE_STATES.filter((s) => s.value).map((s) => [s.field, url(s.value)]));
}

/**
 * linkSegments renders revision-match certainty. Inferred is info rather than ok: a
 * unique mutable-tag correlation is a good guess, not proof, and the whole point of
 * keeping the dimension separate from compliance is that the difference is visible.
 */
export function linkSegments(t: LinkTally | undefined): Segment[] {
  const l = t || {};
  return [
    { label: 'Exact', value: n(l.exact), tone: 'ok' },
    { label: 'Inferred', value: n(l.inferred), tone: 'info' },
    { label: 'Ambiguous', value: n(l.ambiguous), tone: 'warn' },
    { label: 'Unresolved', value: n(l.unresolved), tone: 'warn' },
  ];
}

/** severitySegments renders a finding-severity distribution. */
export function severitySegments(t: SeverityTally | undefined): Segment[] {
  const s = t || {};
  return [
    { label: 'Errors', value: n(s.errors), tone: 'err' },
    { label: 'Warnings', value: n(s.warnings), tone: 'warn' },
    { label: 'Info', value: n(s.infos), tone: 'info' },
    { label: 'Unknown severity', value: n(s.unknown), tone: 'neutral' },
  ];
}

/**
 * evidenceSegments renders freshness. Targets with NO evidence are their own bucket
 * and are never folded into "stale": we have not observed them recently is a
 * different statement from we have never observed them.
 */
export function evidenceSegments(e: { withEvidence?: number; withoutEvidence?: number; stale?: number } | undefined): Segment[] {
  const w = e || {};
  const fresh = Math.max(0, n(w.withEvidence) - n(w.stale));
  return [
    { label: 'Fresh evidence', value: fresh, tone: 'ok' },
    { label: 'Stale evidence', value: n(w.stale), tone: 'warn' },
    { label: 'No evidence', value: n(w.withoutEvidence), tone: 'neutral' },
  ];
}

/** OwnershipTally as emitted by the Product API. Partitions a SERVICE population. */
export interface OwnershipTally {
  consistent?: number;
  conflicting?: number;
  unowned?: number;
}

/** ReadinessTally as emitted by the Product API. Partitions a CONTRACT REVISION population. */
export interface ReadinessTally {
  passing?: number;
  belowThreshold?: number;
  expired?: number;
  notDeclared?: number;
}

/**
 * How ownership is DECLARED across a service population, in the order always shown.
 *
 * "Conflicting" is toned err and never merged into "No declared owner": two teams
 * claiming one service and nobody claiming it are different failures needing opposite
 * fixes, and a page that shows one number for both tells its reader to go declare an
 * owner that already exists twice. It is not toned warn either — a service whose
 * revisions name two teams has no answer at all to "who do I page".
 */
export const OWNERSHIP_STATES: BucketState<keyof OwnershipTally>[] = [
  { value: 'consistent', field: 'consistent', label: 'One declared owner', tone: 'ok' },
  { value: 'conflicting', field: 'conflicting', label: 'Revisions name different owners', tone: 'err' },
  { value: 'unowned', field: 'unowned', label: 'No declared owner', tone: 'warn' },
];

export function ownershipSegments(t: OwnershipTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(OWNERSHIP_STATES, t, hrefs);
}

/**
 * Declared readiness across a CONTRACT REVISION population.
 *
 * The four buckets are exactly the states the readiness engine computes, so nothing
 * here is a frontend invention. "Not assessed" is neutral, not a failure: nobody
 * writing an assessment is a gap in what we know, and painting it red would claim the
 * revision was judged and found wanting. "Assessment expired" is its own bucket for
 * the same reason — an out-of-date assessment is not a low score.
 */
export const READINESS_STATES: BucketState<keyof ReadinessTally>[] = [
  { value: 'passing', field: 'passing', label: 'Passing', tone: 'ok' },
  { value: 'below-threshold', field: 'belowThreshold', label: 'Below its own threshold', tone: 'warn' },
  { value: 'expired', field: 'expired', label: 'Assessment expired', tone: 'warn' },
  { value: 'not-declared', field: 'notDeclared', label: 'Not assessed', tone: 'neutral' },
];

export function readinessSegments(t: ReadinessTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(READINESS_STATES, t, hrefs);
}

/**
 * changeSegments renders a contract diff's severity mix. The counts are the backend's
 * complete change population (ProductChangesPreview.breaking/potential/nonBreaking
 * cover every change found, not the truncated Items preview beside them).
 */
export function changeSegments(c: { breaking?: number; potential?: number; nonBreaking?: number } | undefined): Segment[] {
  const x = c || {};
  return [
    { label: 'Breaking', value: n(x.breaking), tone: 'err' },
    { label: 'Potentially breaking', value: n(x.potential), tone: 'warn' },
    { label: 'Non-breaking', value: n(x.nonBreaking), tone: 'ok' },
  ];
}

/**
 * Impact consumer tallies. The backend emits ordered {key, count} buckets over EVERY
 * consumer (not the current page), so the ranking here is the whole blast radius. The
 * label and tone of each key are decided once, and an unrecognized key -- a value a
 * newer engine emits -- is shown as itself in neutral rather than dropped.
 */
const VERDICT_META: Record<string, { label: string; tone: Segment['tone'] }> = {
  incompatible: { label: 'Incompatible', tone: 'err' },
  compatible: { label: 'Compatible', tone: 'ok' },
  unknown: { label: 'Compatibility unknown', tone: 'warn' },
};

const CONFIDENCE_META: Record<string, { label: string; tone: Segment['tone'] }> = {
  corroborated: { label: 'Declared and observed', tone: 'ok' },
  contractual: { label: 'Declared with a range', tone: 'ok' },
  declared: { label: 'Declared only', tone: 'info' },
  observed: { label: 'Observed only', tone: 'info' },
  inferred: { label: 'Reached through another service', tone: 'neutral' },
  unknown: { label: 'Evidence incomplete', tone: 'warn' },
};

function buckets(items: { key?: string; count?: number }[] | undefined, meta: Record<string, { label: string; tone: Segment['tone'] }>): Segment[] {
  return (items || []).map((b) => {
    const m = meta[b.key || ''];
    return { label: m?.label ?? (b.key || 'Unknown'), value: n(b.count), tone: m?.tone ?? 'neutral' };
  });
}

export function verdictSegments(items: { key?: string; count?: number }[] | undefined): Segment[] {
  return buckets(items, VERDICT_META);
}

export function confidenceSegments(items: { key?: string; count?: number }[] | undefined): Segment[] {
  return buckets(items, CONFIDENCE_META);
}

/** total sums a segment list, for use as a DistributionBar denominator. */
export function segmentTotal(segments: Segment[]): number {
  return segments.reduce((sum, s) => sum + (s.value || 0), 0);
}
