/**
 * Pure, user-facing labels for the two identity dimensions and entity kinds.
 *
 * The dashboard replaces internal ontology with product language and, per
 * requirement H, distinguishes revision-match certainty from content
 * retrievability visibly. These are the ONE source of those labels/tones so a
 * component never hard-codes "exact" strings inconsistently.
 */

export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'neutral';

/** kindLabel is the user-facing singular name for an entity kind. */
export function kindLabel(kind: string | undefined): string {
  switch (kind) {
    case 'service': return 'Service';
    case 'revision': return 'Revision';
    case 'target': return 'Deployment';
    case 'owner': return 'Owner';
    case 'source': return 'Source';
    default: return kind ? kind[0].toUpperCase() + kind.slice(1) : 'Entity';
  }
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
    case 'partial': return 'warn';
    case 'stale': return 'warn';
    case 'unavailable': return 'err';
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
