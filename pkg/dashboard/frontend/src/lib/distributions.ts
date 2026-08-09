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

export interface Segment {
  label: string;
  value: number;
  tone: 'ok' | 'warn' | 'err' | 'info' | 'neutral';
  href?: string;
}

/** ComplianceTally as emitted by the Product API (pkg/fleet/aggregate.go). */
export interface ComplianceTally {
  compliant?: number;
  nonCompliant?: number;
  unknown?: number;
  invalid?: number;
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
 * complianceSegments renders the four Compliance 2.0 states plus the catch-all.
 * "Unknown" is deliberately toned warn, not neutral: a target we cannot evaluate is
 * an open question, and painting it grey beside a green Compliant reads as benign.
 */
export function complianceSegments(t: ComplianceTally | undefined, hrefs: Record<string, string> = {}): Segment[] {
  const c = t || {};
  return [
    { label: 'Compliant', value: n(c.compliant), tone: 'ok', href: hrefs.compliant },
    { label: 'Non-compliant', value: n(c.nonCompliant), tone: 'err', href: hrefs.nonCompliant },
    { label: 'Unknown', value: n(c.unknown), tone: 'warn', href: hrefs.unknown },
    { label: 'Invalid', value: n(c.invalid), tone: 'err', href: hrefs.invalid },
    { label: 'Other', value: n(c.other), tone: 'neutral', href: hrefs.other },
  ];
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
