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

import { isUnknownStatus, statusLabel, statusTone } from './format.ts';

export interface Segment {
  label: string;
  value: number;
  tone: 'ok' | 'warn' | 'err' | 'info' | 'neutral';
  /**
   * Unknown, partial and hidden are drawn as the grey hatch, never as a status tone.
   * Set only on the unknown family, whose tone is always neutral; a plain neutral
   * bucket ("Not evaluated") stays a flat fill so the hatch keeps one meaning.
   */
  hatch?: boolean;
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
  hatch?: boolean;
}

function stateSegments<F extends string>(
  states: BucketState<F>[],
  tally: Partial<Record<F, number>> | undefined,
  hrefs: Record<string, string>,
): Segment[] {
  const t: Partial<Record<F, number>> = tally || {};
  return states.map((s) => ({
    label: s.label, value: n(t[s.field]), tone: s.tone, href: hrefs[s.field], ...(s.hatch ? { hatch: true } : {}),
  }));
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
 * CANONICAL_ORDER is the order every status distribution is shown in, whatever the
 * data: err, warn, the unknown hatch, ok, then the tones that carry no verdict (info,
 * flat neutral). Empty buckets keep their place. `canonicalRank` is what the builders
 * below are tested against.
 */
const CANONICAL_ORDER = ['err', 'warn', 'hatch', 'ok', 'info', 'neutral'] as const;
export function canonicalRank(s: { tone: Segment['tone']; hatch?: boolean }): number {
  return CANONICAL_ORDER.indexOf(s.hatch ? 'hatch' : s.tone);
}

/**
 * Every canonical compliance state, plus the catch-all, in the canonical order.
 *
 * Both the wording and the tone are `format.ts`'s, never a second opinion: a legend
 * reading "Non-Compliant" above rows badged "Not compliant" asks the reader to believe
 * those are two states, and an amber swatch above a blue badge asks the same thing in
 * colour. The two used to be separate tables and they drifted apart on "Unknown".
 */
export const COMPLIANCE_STATES: BucketState<keyof ComplianceTally>[] = [
  ...(
    [
      ['NonCompliant', 'nonCompliant'],
      ['Invalid', 'invalid'],
      ['Warning', 'warning'],
      ['Unknown', 'unknown'],
      ['Compliant', 'compliant'],
      ['Reference', 'reference'],
      ['NotEvaluated', 'notEvaluated'],
    ] as [string, keyof ComplianceTally][]
  ).map(([value, field]) => ({ value, field, label: statusLabel(value), tone: statusTone(value), ...(isUnknownStatus(value) ? { hatch: true } : {}) })),
  // No wire value: "Other" is by definition not a status this build can filter for.
  { value: '', field: 'other', label: 'Other', tone: 'neutral' } as BucketState<keyof ComplianceTally>,
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
    { label: 'Ambiguous', value: n(l.ambiguous), tone: 'warn' },
    { label: 'Unresolved', value: n(l.unresolved), tone: 'warn' },
    { label: 'Exact', value: n(l.exact), tone: 'ok' },
    { label: 'Inferred', value: n(l.inferred), tone: 'info' },
  ];
}

/**
 * Every finding severity, in the order always shown. A finding the backend gave no
 * severity has no wire value, so it is a bucket with no filter behind it -- the same
 * shape "Other" takes in COMPLIANCE_STATES, and for the same reason.
 */
export const SEVERITY_STATES: BucketState<keyof SeverityTally>[] = [
  { value: 'error', field: 'errors', label: 'Errors', tone: 'err' },
  { value: 'warning', field: 'warnings', label: 'Warnings', tone: 'warn' },
  { value: '', field: 'unknown', label: 'Unknown severity', tone: 'neutral', hatch: true },
  { value: 'info', field: 'infos', label: 'Info', tone: 'info' },
];

/** severitySegments renders a finding-severity distribution. */
export function severitySegments(t: SeverityTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(SEVERITY_STATES, t, hrefs);
}

/** severityHrefs is statusHrefs for severity: the drill-down for every real filter. */
export function severityHrefs(url: (severity: string) => string): Record<string, string> {
  return Object.fromEntries(SEVERITY_STATES.filter((s) => s.value).map((s) => [s.field, url(s.value)]));
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
    { label: 'Stale evidence', value: n(w.stale), tone: 'warn' },
    { label: 'No evidence', value: n(w.withoutEvidence), tone: 'neutral', hatch: true },
    { label: 'Fresh evidence', value: fresh, tone: 'ok' },
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
  { value: 'conflicting', field: 'conflicting', label: 'Revisions name different owners', tone: 'err' },
  { value: 'unowned', field: 'unowned', label: 'No declared owner', tone: 'warn' },
  { value: 'consistent', field: 'consistent', label: 'One declared owner', tone: 'ok' },
];

export function ownershipSegments(t: OwnershipTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(OWNERSHIP_STATES, t, hrefs);
}

/**
 * ownershipHrefs builds the drill-down for all three ownership buckets from one
 * `ownership` URL builder — the counterpart of `statusHrefs`. Every bucket IS a real
 * filter, so unlike compliance there is nothing here to leave out.
 */
export function ownershipHrefs(url: (ownership: string) => string): Record<string, string> {
  return Object.fromEntries(OWNERSHIP_STATES.map((s) => [s.field, url(s.value)]));
}

/** OwnerCount as emitted by the Product API (pkg/fleet/aggregate.go). */
export interface OwnerCount {
  /** Canonical identity: `team:NAME` or `dri:NAME`. What a link and a filter carry. */
  key?: string;
  /** The owner's name as authored. What a reader reads. */
  label?: string;
  /** `team` or `dri` — the namespace that tells two same-named owners apart. */
  kind?: string;
  /**
   * Another canonical owner ANYWHERE in the matched population carries this same
   * label, so the row has to show its namespace to name one owner. Decided over the
   * whole population, not the ranked rows: the colliding owner may be the one the
   * ranking cut off.
   */
  ambiguous?: boolean;
  services?: number;
  targets?: number;
}

/** The ranking fields of an EntityAggregate. */
export interface OwnerRankingTally {
  byOwner?: OwnerCount[];
  /** Consistently owned services under canonical owners past the ranking bound. */
  beyondRanking?: number;
  /** Consistently owned services whose owner has no canonical identity at all. */
  unidentifiedOwnership?: number;
  /** Canonical owners with at least one consistently owned service — the rankable ones. */
  rankedOwners?: number;
  /** Every canonical owner named by the matched services, disputed ones included. */
  distinctOwners?: number;
  /** The consistent/conflicting/unowned partition the ranking is carved out of. */
  ownership?: OwnershipTally;
}

/**
 * The backend's top-owner ranking, in the one shape and the one wording every surface
 * shows it under.
 *
 * It is a RANKING, not a partition, and three separate populations sit outside it:
 *
 *  - services whose revisions name different owners, and services nobody claims —
 *    counted in `ownership`, in no row, and the ones most in need of an owner;
 *  - consistently owned services whose owner ranks past the bound (`beyondRanking`);
 *  - consistently owned services whose owner has NO canonical identity at all
 *    (`unidentifiedOwnership`) — an owner block of contacts alone is a real
 *    declaration with nobody named in it, so there is no row it could ever occupy.
 *
 * The last two are never added together. A tail is "more owners than fit"; an
 * unidentified remainder is "ownership we cannot file under anyone", and a reader who
 * reads the second as the first goes looking for a page that does not exist.
 *
 * The counts reconcile exactly, and this note is where a reader can check it:
 *   rows + beyondRanking + unidentifiedOwnership == ownership.consistent
 *
 * `href` reproduces what a row COUNTED, not merely who it names: `owner=x` alone
 * means "some revision of this service names x", which also selects what x co-owns
 * with somebody else. A caller passes a key-plus-consistent URL, so a bar and its own
 * destination cannot disagree.
 */
export function ownerRanking(agg: OwnerRankingTally | undefined, href: (ownerKey: string) => string): {
  /** Every canonical owner in the population — what "N owners" must be worded as. */
  distinct: number;
  /** Canonical owners that could rank. Never more than `distinct`. */
  ranked: number;
  services: Segment[];
  targets: Segment[];
  note: string;
  /** Consistently owned services with no canonical owner to file them under. */
  unidentified: number;
  /** The sentence stating that remainder, or '' when there is none. */
  unidentifiedNote: string;
} {
  const rows = agg?.byOwner ?? [];
  const distinct = n(agg?.distinctOwners);
  const ranked = n(agg?.rankedOwners);
  const beyond = n(agg?.beyondRanking);
  const unidentified = n(agg?.unidentifiedOwnership);
  const shown = rows.length;
  const svc = (v: number) => `${v} ${v === 1 ? 'service' : 'services'}`;
  // The namespace is shown only where it is load-bearing — on the rows a reader
  // could otherwise not tell apart. Two owners called alice must not be two
  // identical bars; every other row reads as the name its owner authored.
  //
  // Which rows those are is the BACKEND's answer (`ambiguous`), computed over the
  // whole population, and deliberately not recomputed from the rows on screen. With
  // `team:alice` ranked first and `dri:alice` one place past the bound, the visible
  // rows contain exactly one alice, so counting them would print an unqualified
  // `alice` — a label whose meaning changed because of how many owners happened to
  // fit. Identity cannot depend on the truncation boundary.
  const kindOf = (o: OwnerCount) => (o.kind === 'dri' ? 'DRI' : o.kind === 'team' ? 'Team' : '');
  const rowLabel = (o: OwnerCount) => {
    const label = o.label || o.key || '';
    const kind = kindOf(o);
    return o.ambiguous && kind ? `${label} (${kind})` : label;
  };
  const row = (o: OwnerCount, value: number | undefined): Segment => ({
    label: rowLabel(o),
    value: n(value),
    tone: 'info',
    href: href(o.key || ''),
  });
  // "N owners" is a claim about a population, so it names which one. `distinct`
  // includes owners that rank nowhere because every service naming them is disputed.
  const scope = ranked === distinct
    ? `Top ${shown} of ${ranked} named ${ranked === 1 ? 'owner' : 'owners'} by service count.`
    : `Top ${shown} of ${ranked} rankable ${ranked === 1 ? 'owner' : 'owners'} by service count, out of ${distinct} named across these services. The other ${distinct - ranked} ${distinct - ranked === 1 ? 'is named only by services whose revisions disagree' : 'are named only by services whose revisions disagree'}.`;
  const rest = ranked - shown;
  const tail = beyond > 0
    ? ` ${rest === 1 ? 'The remaining owner accounts' : `The remaining ${rest} of ${ranked} owners account`} for ${beyond} more ${beyond === 1 ? 'service' : 'services'}.`
    : '';
  const unidentifiedNote = unidentified > 0
    ? `${svc(unidentified)} ${unidentified === 1 ? 'is' : 'are'} consistently owned by a declaration that names no team or DRI, only contacts. It has no team or person to rank or link to, so it appears in no row above.`
    : '';
  return {
    distinct,
    ranked,
    unidentified,
    unidentifiedNote,
    services: rows.map((o) => row(o, o.services)),
    targets: rows.map((o) => row(o, o.targets)),
    note: shown
      ? `${scope}${tail} Services with no declared owner, or whose revisions name different owners, appear in no row here.`
      : '',
  };
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
  { value: 'below-threshold', field: 'belowThreshold', label: 'Below its own threshold', tone: 'warn' },
  { value: 'expired', field: 'expired', label: 'Assessment expired', tone: 'warn' },
  { value: 'passing', field: 'passing', label: 'Passing', tone: 'ok' },
  { value: 'not-declared', field: 'notDeclared', label: 'Not assessed', tone: 'neutral' },
];

export function readinessSegments(t: ReadinessTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  return stateSegments(READINESS_STATES, t, hrefs);
}

/** SignatureTally as emitted by the Product API (ServiceSummary.signatures). */
export interface SignatureTally {
  verified?: number;
  unverified?: number;
  unsigned?: number;
  unknown?: number;
  notChecked?: number;
}

/**
 * The bundle signature check of a revision, in the order always shown. Only a
 * signature verified against the identity its own contract declares is `ok`; a check
 * that could not finish and a revision no source checks are neutral, never positive.
 */
export const SIGNATURE_STATES: BucketState<keyof SignatureTally>[] = [
  { value: 'unverified', field: 'unverified', label: 'Unverified', tone: 'err' },
  { value: 'unsigned', field: 'unsigned', label: 'Unsigned', tone: 'warn' },
  { value: 'unknown', field: 'unknown', label: 'Unknown', tone: 'neutral', hatch: true },
  { value: 'verified', field: 'verified', label: 'Verified', tone: 'ok' },
  { value: 'not-checked', field: 'notChecked', label: 'Not checked', tone: 'neutral' },
];

export function signatureSegments(t: SignatureTally | undefined): Segment[] {
  return stateSegments(SIGNATURE_STATES, t, {});
}

/**
 * The state one revision's signature check is shown under. A missing check is "Not
 * checked", and a verified signature that names no signer is shown as unknown: a
 * verified badge always has the signer beside it. A status this build does not know
 * is unknown too.
 */
export function signatureState(sig: { status?: string; signer?: string } | null | undefined): BucketState<keyof SignatureTally> {
  const v = !sig ? 'not-checked' : sig.status === 'verified' && !sig.signer ? 'unknown' : sig.status;
  return SIGNATURE_STATES.find((s) => s.value === v) ?? SIGNATURE_STATES.find((s) => s.value === 'unknown')!;
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
type Meta = Record<string, { label: string; tone: Segment['tone']; hatch?: boolean }>;

// Unknown is the grey hatch, never warn: a verdict Pacto cannot reach is not a warning
// about the consumer, and an amber bucket between red and green read as one.
const VERDICT_META: Meta = {
  incompatible: { label: 'Incompatible', tone: 'err' },
  unknown: { label: 'Compatibility unknown', tone: 'neutral', hatch: true },
  compatible: { label: 'Compatible', tone: 'ok' },
};

// Outcomes are the backend's DISJOINT partition of the consumers (byOutcome): a consumer
// that refuses a required guarantee is incompatible whatever its range accepts, so the
// buckets sum to the total and can be drawn as lanes. Unclassified is a verdict the
// engine does not recognize: a shortfall, hatched like unknown.
const OUTCOME_META: Meta = {
  incompatible: { label: 'Incompatible', tone: 'err' },
  unknown: { label: 'Unknown', tone: 'neutral', hatch: true },
  compatible: { label: 'Compatible', tone: 'ok' },
  unclassified: { label: 'Unclassified', tone: 'neutral', hatch: true },
};

const CONFIDENCE_META: Meta = {
  corroborated: { label: 'Declared and observed', tone: 'ok' },
  contractual: { label: 'Declared with a range', tone: 'ok' },
  declared: { label: 'Declared only', tone: 'info' },
  observed: { label: 'Observed only', tone: 'info' },
  inferred: { label: 'Reached through another service', tone: 'neutral' },
  unknown: { label: 'Evidence incomplete', tone: 'neutral', hatch: true },
};

function buckets(items: { key?: string; count?: number }[] | undefined, meta: Meta): Segment[] {
  return (items || []).map((b) => {
    const m = meta[b.key || ''];
    return { label: m?.label ?? (b.key || 'Unknown'), value: n(b.count), tone: m?.tone ?? 'neutral', ...(m?.hatch ? { hatch: true } : {}) };
  });
}

export function verdictSegments(items: { key?: string; count?: number }[] | undefined): Segment[] {
  return buckets(items, VERDICT_META);
}

export function outcomeSegments(items: { key?: string; count?: number }[] | undefined): Segment[] {
  return buckets(items, OUTCOME_META);
}

export function confidenceSegments(items: { key?: string; count?: number }[] | undefined): Segment[] {
  return buckets(items, CONFIDENCE_META);
}

/** total sums a segment list, for use as a DistributionBar denominator. */
export function segmentTotal(segments: Segment[]): number {
  return segments.reduce((sum, s) => sum + (s.value || 0), 0);
}
