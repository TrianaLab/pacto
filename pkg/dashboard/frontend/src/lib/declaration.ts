/**
 * How the revision page words a declaration: who declared it from which inferred
 * service and how complete the cluster read was, in one line, the rest in the mark's
 * note. Only a signature this reader checked names the declarer; an unverified one is
 * named in the note as a claim and never in the line. A viewer who may not read the
 * namespace the declaration came from is told so, with no namespace.
 */
import type { components } from './generated/schema.ts';
import { joinAnd } from './answers.ts';
import { formatCount } from './format.ts';

type S = components['schemas'];
export interface DeclaredRevision {
  declaration?: S['Fleet.DeclarationView'];
  inferredCoverage?: S['Fleet.InferredCoverageRow'][] | null;
  inferredHidden?: S['Fleet.InferredHiddenCounts'];
}

const HIDDEN = 'a workload you may not read';

/** The muted line under the Inferred label; empty without a declaration. */
export function declarationLine(r: DeclaredRevision): string {
  const d = r.declaration;
  if (!d) return '';
  if (!d.signatureChecked || !d.signer) return 'Declaration by an unverified signer';
  const from = d.inferredFrom ? ` from ${d.inferredFrom}` : d.inferredFromHidden ? ` from ${HIDDEN}` : '';
  const cov = r.inferredCoverage ?? [];
  const read = cov.length ? ` · cluster read ${formatCount(cov.filter((c) => c.state === 'complete').length)} of ${formatCount(cov.length)} sources complete` : '';
  return `Declared by ${d.signer.subject}${from}${read}`;
}

/** The mark's note: signer and issuer, baseline, accepted ranges and hidden counts. */
export function declarationNote(r: DeclaredRevision): string {
  const d = r.declaration;
  if (!d) return '';
  const h = r.inferredHidden ?? {};
  const parts = [
    d.signatureChecked && d.signer ? `Signed by ${d.signer.subject} (${d.signer.issuer}); signature checked.`
      : d.claimedSubject ? `Claims ${d.claimedSubject}; signature not checked or not by that signer.` : 'Signature not checked.',
    d.inferredFromHidden ? `From ${HIDDEN}; ${formatCount(h.fields ?? 0)} cluster fields left out.` : '',
    d.baseline ? `Baseline ${d.baseline}.` : '',
    d.accepted?.length ? `Accepted ${joinAnd(d.accepted)}.` : '',
    d.draft ? `Accepted draft ${d.draft}.` : '',
    h.services || h.callers ? `The cluster read could not see ${formatCount(h.services ?? 0)} services and ${formatCount(h.callers ?? 0)} callers.` : '',
    'Cluster facts are inferred and never clear a gate.',
  ];
  return parts.filter(Boolean).join(' ');
}
