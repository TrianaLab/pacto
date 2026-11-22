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

type S = components['schemas'];
interface DeclaredRevision {
  declaration?: S['Fleet.DeclarationView'];
  inferredCoverage?: S['Fleet.InferredCoverageRow'][] | null;
  inferredHidden?: S['Fleet.InferredHiddenCounts'];
}

const HIDDEN = 'a workload you may not read';

/** The muted line under the Inferred label; empty without a declaration. */
export function declarationLine(r: DeclaredRevision): string {
  const d = r.declaration;
  if (!d) return '';
  if (!d.signatureChecked) return 'Declaration by an unverified signer';
  if (!d.signer) return 'Declaration by a signer this installation does not allow';
  const from = d.inferredFrom ? ` from ${d.inferredFrom}` : d.inferredFromHidden ? ` from ${HIDDEN}` : '';
  const cov = r.inferredCoverage ?? [];
  const read = cov.length ? ` · cluster read ${formatCount(cov.filter((c) => c.state === 'complete').length)} of ${countOf(cov.length, 'source')} complete` : '';
  return `Declared by ${d.signer.subject}${from}${read}`;
}

/** Who signed, as the note words it. */
function signedBy(d: S['Fleet.DeclarationView']): string {
  if (d.signatureChecked && d.signer) return `Signed by ${d.signer.subject} (${d.signer.issuer}); signature checked.`;
  if (d.signatureChecked) return `Claims ${d.claimedSubject}; signature checked. Not accepted: ${d.notAccepted}.`;
  return d.claimedSubject ? `Claims ${d.claimedSubject}; signature not checked or not by that signer.` : 'Signature not checked.';
}

/** A repository rung or folder rung in words, as pacto declare prints it. */
const RUNG: Record<string, string> = {
  given: 'given', 'signed-provenance': 'signed provenance', gitops: 'GitOps source',
  'image-label': 'image label', 'repository-map': 'repository map', default: 'no source names the folder; pass --folder',
};

/** A CI signer's subject as pacto declare names it: the workflow or pipeline file at its ref. */
function signerLabel(subject: string): string {
  const wf = subject.split('/.github/workflows/');
  if (wf.length > 1) return `workflow ${wf.slice(1).join('/.github/workflows/')}`;
  const rest = subject.split('://').slice(1).join('://');
  const at = rest.indexOf('//');
  return at >= 0 ? `pipeline ${rest.slice(at + 2)}` : subject;
}

/** Where the declared service's source lives and how Pacto found it. */
function repositoryOf(d: S['Fleet.DeclarationView']): string {
  const r = d.repository;
  if (!r) return '';
  const how = (RUNG[r.rung] ?? r.rung) + (r.signer ? `, ${signerLabel(r.signer)}` : '');
  return `Repository ${r.url} (${how}), folder ${r.folder} (${RUNG[r.folderRung] ?? r.folderRung}).`;
}

/** The mark's note: signer and issuer, baseline, accepted ranges, repository and hidden counts. */
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
    repositoryOf(d),
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
