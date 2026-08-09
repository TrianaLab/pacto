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

// ── relationship difference (a NeighborhoodEdge's declared-vs-observed state) ──
// The product "Differences" view reads this backend fact verbatim (ADR-3): it is the
// reconciliation of an EXPECTED (declared) dependency against OBSERVED runtime traffic.
export function differenceLabel(d: string | undefined): string {
  switch (d) {
    case 'matched': return 'Matched';
    case 'expected-not-observed': return 'Expected, not observed';
    case 'observed-not-expected': return 'Observed, not expected';
    case 'insufficient': return 'Insufficient evidence';
    default: return d || 'Unknown';
  }
}
export function differenceTone(d: string | undefined): Tone {
  switch (d) {
    case 'matched': return 'ok';
    case 'expected-not-observed': return 'warn';
    case 'observed-not-expected': return 'info';
    case 'insufficient': return 'neutral';
    default: return 'neutral';
  }
}

// provenanceLabel names where a relationship's knowledge came from.
export function provenanceLabel(p: string | undefined): string {
  switch (p) {
    case 'declared': return 'Expected';
    case 'observed': return 'Observed';
    default: return p || '';
  }
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
