/**
 * Pure, user-facing labels for the two identity dimensions and entity kinds.
 *
 * The dashboard replaces internal ontology with product language and, per
 * requirement H, distinguishes revision-match certainty from content
 * retrievability visibly. These are the ONE source of those labels/tones so a
 * component never hard-codes "exact" strings inconsistently.
 */

export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'neutral';

/**
 * kindLabel is the user-facing singular name for an entity kind.
 *
 * The internal identifiers (EntityKind 'target', TargetKey, /fleet/targets/...) are
 * UNCHANGED; only the words a first-time user reads are. Two of them are deliberate:
 *   - 'target' reads "Operational target" -- a concrete place a revision runs. It is
 *     NOT "Deployment": Pacto observes where a revision runs, it never deploys.
 *   - 'source' reads "Data source" -- the ingestion seam a snapshot was built from.
 *     It is NOT a collector (a collector observes a real environment and emits
 *     evidence; OCI, local and cache are sources but observe nothing).
 */
export function kindLabel(kind: string | undefined): string {
  switch (kind) {
    case 'service': return 'Service';
    case 'revision': return 'Revision';
    case 'target': return 'Operational target';
    case 'owner': return 'Owner';
    case 'source': return 'Data source';
    default: return kind ? kind[0].toUpperCase() + kind.slice(1) : 'Entity';
  }
}

/** kindLabelPlural is the user-facing plural of kindLabel (section headings, lists). */
export function kindLabelPlural(kind: string | undefined): string {
  return `${kindLabel(kind)}s`;
}

// ── revision-match certainty (a target's LinkState) ──────────────────────────
export function linkStateLabel(s: string | undefined): string {
  switch (s) {
    case 'exact': return 'Exact revision match';
    case 'inferred': return 'Inferred revision match';
    case 'ambiguous': return 'Ambiguous revision match';
    case 'unresolved': return 'Unresolved revision';
    default: return s || 'Unknown';
  }
}
export function linkStateTone(s: string | undefined): Tone {
  switch (s) {
    case 'exact': return 'ok';
    case 'inferred': return 'info';
    case 'ambiguous': return 'warn';
    case 'unresolved': return 'neutral';
    default: return 'neutral';
  }
}

// ── content retrievability (RevisionIdentity.retrievable + identityClass) ─────
// This is a SEPARATE dimension from revision-match certainty: an exact revision
// match can point at content that is not resolver-retrievable.
export function retrievabilityLabel(identityClass: string | undefined, retrievable: boolean | undefined): string {
  if (retrievable) return 'Retrievable content';
  switch (identityClass) {
    case 'mutable': return 'Mutable reference (not retrievable)';
    case 'no-ref': return 'No canonical reference';
    case 'local': return 'Local reference (not retrievable)';
    case 'malformed': return 'Malformed identity';
    case 'digest-mismatch': return 'Digest/reference mismatch';
    default: return 'Content not retrievable';
  }
}
export function retrievabilityTone(identityClass: string | undefined, retrievable: boolean | undefined): Tone {
  if (retrievable) return 'ok';
  // A digest/ref disagreement or malformed identity is a genuine inconsistency
  // (error); merely-not-retrievable-but-consistent content is a neutral limitation.
  return identityClass === 'digest-mismatch' || identityClass === 'malformed' ? 'err' : 'neutral';
}

// ── snapshot knowledge level ─────────────────────────────────────────────────
export function knowledgeLabel(level: string | undefined): string {
  switch (level) {
    case 'complete': return 'Complete knowledge';
    case 'empty': return 'Nothing known yet';
    case 'partial': return 'Partial knowledge';
    case 'stale': return 'Stale knowledge';
    case 'unavailable': return 'Source unavailable';
    case 'unknown': return 'Knowledge state unknown';
    default: return 'Knowledge state unknown';
  }
}
export function knowledgeTone(level: string | undefined): Tone {
  switch (level) {
    case 'complete': return 'ok';
    case 'empty': return 'neutral';
    case 'partial': return 'warn';
    case 'stale': return 'warn';
    case 'unavailable': return 'err';
    default: return 'neutral';
  }
}

// ── relationship difference: see differenceLabel/differenceTone in lib/graphState.ts ──

// provenanceLabel names where a relationship's knowledge came from. The wire enum
// (pkg/fleet/neighborhood.go) has THREE values: a merged edge carries the combined
// "declared+observed", which had no case here and so printed as the raw wire token.
export function provenanceLabel(p: string | undefined): string {
  switch (p) {
    case 'declared': return 'Expected';
    case 'observed': return 'Observed';
    case 'declared+observed': return 'Expected and observed';
    default: return p || '';
  }
}

// In the ordinary cases a relationship's provenance and its reconciliation are two names
// for one fact: an edge that is "Expected, not observed" was declared BY DEFINITION, and
// a "Matched" edge is both declared and observed. Printing both put a second word beside
// a badge that already said it. This is the provenance each difference already implies,
// so a caller can drop the redundant chip and keep it where it still carries news --
// insufficient evidence, or a pairing we did not anticipate.
const IMPLIED_PROVENANCE: Record<string, string> = {
  matched: 'declared+observed',
  'expected-not-observed': 'declared',
  'observed-not-expected': 'observed',
};
export function provenanceIsImplied(difference: string | undefined, provenance: string | undefined): boolean {
  return !!provenance && IMPLIED_PROVENANCE[difference || ''] === provenance;
}

// ── attention categories ─────────────────────────────────────────────────────
// The backend enum (pkg/fleet/product.go) is the wire truth; these are the words a
// first-time user reads. Readiness is one of them ON PURPOSE: readiness is a
// dimension of triage (declared contract preparedness), not a separate workspace,
// so this list is also the Overview's set of entry points into the triage view.
export const ATTENTION_CATEGORIES = [
  'non-compliant', 'unknown', 'stale', 'invalid', 'readiness', 'unresolved',
] as const;

export function attentionCategoryLabel(c: string | undefined): string {
  switch (c) {
    case 'non-compliant': return 'Not compliant';
    case 'unknown': return 'Compliance unknown';
    case 'stale': return 'Stale evidence';
    case 'invalid': return 'Invalid contract';
    case 'readiness': return 'Readiness gate';
    case 'unresolved': return 'Unresolved revision';
    default: return c || 'Other';
  }
}

// ── attention severity ───────────────────────────────────────────────────────
// Severity is a THIRD vocabulary, orthogonal to compliance and to source health:
// it grades how much a triage item matters, not what state an entity is in. Routed
// through the compliance badge it had no case and no tone, so the highest-severity
// row on the triage screen printed a grey lowercase "error" — the one signal the
// screen exists to convey, rendered as the most ignorable thing on it.
// A finding severity carries one extra value the attention enum does not: "unknown"
// (pkg/fleet/preview.go), which is a severity we could not grade — not a fourth grade.
export function severityLabel(s: string | undefined): string {
  switch (s) {
    case 'error': return 'Error';
    case 'warning': return 'Warning';
    case 'info': return 'Info';
    case 'unknown': return 'Unknown';
    default: return s || 'Unknown';
  }
}
export function severityTone(s: string | undefined): Tone {
  switch (s) {
    case 'error': return 'err';
    case 'warning': return 'warn';
    case 'info': return 'info';
    default: return 'neutral';
  }
}

// ── source health ────────────────────────────────────────────────────────────
export function sourceHealthLabel(status: string | undefined): string {
  switch (status) {
    case 'available': return 'Available';
    case 'partial': return 'Partial';
    case 'stale': return 'Stale';
    case 'unavailable': return 'Unavailable';
    default: return status || 'Unknown';
  }
}
export function sourceHealthTone(status: string | undefined): Tone {
  switch (status) {
    case 'available': return 'ok';
    case 'partial': return 'warn';
    case 'stale': return 'warn';
    case 'unavailable': return 'err';
    default: return 'neutral';
  }
}
/** The complete-population source tally (Fleet.SourceCounts). */
export interface SourceCounts {
  total?: number;
  available?: number;
  partial?: number;
  stale?: number;
  unavailable?: number;
}
/** One non-empty bucket of the fleet-wide source tally. `status` is the backend
 *  health filter value, or '' for the unclassified remainder — which has no filter
 *  value precisely because the product does not recognize what those sources are. */
export interface SourceTallyPart { status: string; count: number; text: string }
/**
 * sourceHealthTallyParts breaks the COMPLETE source population into its non-empty
 * health buckets — never the meta's source list, which is capped and, once capped,
 * deliberately biased towards the least healthy.
 *
 * Least-healthy first, so the number that changes what a reader does is the first
 * one they read. A status the product does not recognize is NOT folded into a
 * bucket; it is named as unclassified, because the alternative is a tally that adds
 * up perfectly and is wrong.
 */
export function sourceHealthTallyParts(c: SourceCounts | null | undefined): SourceTallyPart[] {
  const parts: SourceTallyPart[] = [];
  for (const [status, n] of [
    ['unavailable', c?.unavailable ?? 0],
    ['stale', c?.stale ?? 0],
    ['partial', c?.partial ?? 0],
    ['available', c?.available ?? 0],
  ] as Array<[string, number]>) {
    if (n > 0) parts.push({ status, count: n, text: `${n} ${status}` });
  }
  const classified = parts.reduce((n, p) => n + p.count, 0);
  const total = c?.total ?? 0;
  if (total > classified) {
    parts.push({ status: '', count: total - classified, text: `${total - classified} unclassified` });
  }
  return parts;
}
/** sourceHealthTally is [sourceHealthTallyParts] as one sentence, for a surface with
 *  no room (or no need) for a per-bucket drill-down. */
export function sourceHealthTally(c: SourceCounts | null | undefined): string {
  const total = c?.total ?? 0;
  if (total === 0) return 'No data sources reported.';
  const noun = `${total} data source${total === 1 ? '' : 's'}`;
  const parts = sourceHealthTallyParts(c);
  if (parts.length === 1 && parts[0].status === 'available') return `${noun}, all available.`;
  return `${noun} — ${parts.map((p) => p.text).join(', ')}.`;
}
/**
 * sourceHealthSentence says what a source's health MEANS for the records around it,
 * in a sentence rather than a badge.
 *
 * A source page carries two health facts that are routinely different: this source's
 * own status, and the snapshot's completeness. The snapshot caveat is a banner, so
 * this one has to be more than a second badge repeating the header — a reader
 * comparing two badges cannot tell which is about what. A sentence naming the
 * consequence can only be read one way.
 */
export function sourceHealthSentence(status: string | undefined): string {
  switch (status) {
    case 'available':
      return 'This data source answered in full, so everything it holds is in the snapshot.';
    case 'partial':
      return 'This data source answered only in part, so records it holds are missing from the snapshot rather than absent from the fleet.';
    case 'stale':
      return 'This data source last answered too long ago to be treated as current, so what it contributed to the snapshot may already have changed.';
    case 'unavailable':
      return 'This data source did not answer, so nothing it holds reached the snapshot.';
    default:
      return 'This data source reported a health state Pacto does not recognize, so how much of it reached the snapshot is unknown.';
  }
}
