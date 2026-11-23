/**
 * How the revision page words a declaration: who declared it from which inferred
 * service and how complete the cluster read was, in one line, the rest in the mark's
 * note. Only a declarer this reader accepts (a checked signature the installation
 * allows) is named in the line; any other is named in the note as a claim, with what the
 * record claims beside it. A viewer who may not read the workload the declaration came
 * from is told so, with no namespace.
 */
import type { components } from './generated/schema.ts';
import { joinAnd } from './answers.ts';
import { countOf, formatCount } from './format.ts';
import { fleetEntityUrl } from './router.ts';

type S = components['schemas'];
interface DeclaredRevision {
  declaration?: S['Fleet.DeclarationView'];
  inferredCoverage?: S['Fleet.InferredCoverageRow'][] | null;
  inferredHidden?: S['Fleet.InferredHiddenCounts'];
}

const HIDDEN = 'a workload you may not read';
// A claim's inferredFrom names the external system it claims.
const EXTERNAL = 'external:';

/** The muted line under the Inferred label, in parts: the words before the external
 *  system a claim came from, its link (text and href, empty for any other declaration)
 *  and the words after it. All empty without a declaration. */
export function declarationParts(r: DeclaredRevision): { before: string; link: string; href: string; after: string } {
  const d = r.declaration;
  const none = { before: '', link: '', href: '', after: '' };
  if (!d) return none;
  if (!d.signatureChecked) return { ...none, before: 'Declaration by an unverified signer' };
  if (!d.signer) return { ...none, before: 'Declaration by a signer this installation does not allow' };
  const cov = r.inferredCoverage ?? [];
  const read = cov.length ? ` · cluster read ${formatCount(cov.filter((c) => c.state === 'complete').length)} of ${countOf(cov.length, 'source')} complete` : '';
  const ext = d.inferredFrom?.startsWith(EXTERNAL) ? d.inferredFrom.slice(EXTERNAL.length) : '';
  if (ext) return { before: `Declared by ${d.signer.subject} from `, link: `external ${ext}`, href: fleetEntityUrl('external', ext), after: read };
  const from = d.inferredFrom ? ` from ${d.inferredFrom}` : d.inferredFromHidden ? ` from ${HIDDEN}` : '';
  return { ...none, before: `Declared by ${d.signer.subject}${from}${read}` };
}

/** The muted line under the Inferred label as one string; empty without a declaration. */
export function declarationLine(r: DeclaredRevision): string {
  const p = declarationParts(r);
  return p.before + p.link + p.after;
}

/** Who signed, as the note words it. */
function signedBy(d: S['Fleet.DeclarationView']): string {
  if (d.signatureChecked && d.signer) return `Signed by ${d.signer.subject} (${d.signer.issuer}); signature checked.`;
  if (d.signatureChecked) return `Claims ${d.claimedSubject}; signature checked. Not accepted: ${d.notAccepted}.`;
  return d.claimedSubject ? `Claims ${d.claimedSubject}; signature not checked or not by that signer.` : 'Signature not checked.';
}

/** The mark's note: signer and issuer, baseline, accepted ranges and hidden counts. */
export function declarationNote(r: DeclaredRevision): string {
  const d = r.declaration;
  if (!d) return '';
  const h = r.inferredHidden ?? {};
  const left = h.fields ? `${countOf(h.fields, 'cluster field')} left out` : '';
  const seen = [h.services ? countOf(h.services, 'service') : '', h.callers ? countOf(h.callers, 'caller') : ''].filter(Boolean);
  // What only the record says: claimed, never stated, when the declarer is not accepted.
  const claims = [
    d.baseline ? `Baseline ${d.baseline}.` : '',
    d.accepted?.length ? `Accepted ${joinAnd(d.accepted)}.` : '',
    d.draft ? `Accepted draft ${d.draft}.` : '',
  ].filter(Boolean).join(' ');
  const parts = [
    signedBy(d),
    d.inferredFromHidden ? `From ${HIDDEN}${left ? `; ${left}` : ''}.` : left ? `${left}: they name a service you may not read.` : '',
    claims && !d.signer ? `The record claims: ${claims}` : claims,
    seen.length ? `The cluster read could not see ${joinAnd(seen)}.` : '',
    'Cluster facts are inferred and never clear a gate.',
  ];
  return parts.filter(Boolean).join(' ');
}
