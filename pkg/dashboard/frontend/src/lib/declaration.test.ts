import { describe, it, expect } from 'vitest';
import { declarationLine, declarationNote, declarationParts } from './declaration.ts';

const signer = { subject: 'ana@acme.com', issuer: 'https://accounts.google.com', class: 'human' as const };
const coverage = [
  { source: 'cluster:billing/Deployment/invoices', state: 'complete' as const },
  { source: 'cluster:runtime-calls', state: 'partial' as const, reason: 'node unobserved' },
  { source: 'cluster:services', state: 'complete' as const },
];
const checked = {
  declaration: { state: 'declared' as const, signatureChecked: true, signer, inferredFrom: 'k8s:billing/Deployment/invoices', baseline: '1.2.0', accepted: ['dependencies[payments] ^2.0.0', 'dependencies[ledger] ^1.0.0'] },
  inferredCoverage: coverage,
  inferredHidden: { services: 2, callers: 3 },
};
const unverified = {
  declaration: { state: 'declared' as const, signatureChecked: false, claimedSubject: 'mallory@evil.example', inferredFrom: 'k8s:billing/Deployment/invoices' },
};

const notAllowed = {
  declaration: { state: 'declared' as const, signatureChecked: true, claimedSubject: 'mallory@gmail.com', notAccepted: 'the installation signer policy lists no issuer https://accounts.google.com', baseline: '1.4.0' },
};

describe('declarationLine', () => {
  it('names the signer, the inferred service and the cluster read when the signature was checked', () => {
    expect(declarationLine(checked)).toBe('Declared by ana@acme.com from k8s:billing/Deployment/invoices · cluster read 2 of 3 sources complete');
  });
  it('leaves the coverage out when there is none', () => {
    expect(declarationLine({ declaration: checked.declaration })).toBe('Declared by ana@acme.com from k8s:billing/Deployment/invoices');
  });
  it('names no namespace when the viewer may not read it', () => {
    const line = declarationLine({ declaration: { ...checked.declaration, inferredFrom: undefined, inferredFromHidden: true } });
    expect(line).toBe('Declared by ana@acme.com from a workload you may not read');
    expect(line).not.toContain('billing');
  });
  it('names no subject when the signature was not checked', () => {
    const line = declarationLine(unverified);
    expect(line).toBe('Declaration by an unverified signer');
    expect(line).not.toContain('mallory');
  });
  it('names no subject when the installation does not allow the signer', () => {
    const line = declarationLine({ declaration: { ...notAllowed.declaration } });
    expect(line).toBe('Declaration by a signer this installation does not allow');
    expect(line).not.toContain('mallory');
  });
  it('words a single coverage source in the singular', () => {
    expect(declarationLine({ declaration: checked.declaration, inferredCoverage: [coverage[0]] })).toContain('cluster read 1 of 1 source complete');
  });
  it('is empty without a declaration', () => {
    expect(declarationLine({})).toBe('');
    expect(declarationNote({})).toBe('');
  });
});

describe('declarationNote', () => {
  it('names the issuer, the baseline, the accepted ranges and the hidden counts', () => {
    const note = declarationNote(checked);
    expect(note).toContain('Signed by ana@acme.com (https://accounts.google.com); signature checked.');
    expect(note).toContain('Baseline 1.2.0.');
    expect(note).toContain('Accepted dependencies[payments] ^2.0.0 and dependencies[ledger] ^1.0.0.');
    expect(note).toContain('The cluster read could not see 2 services and 3 callers.');
    expect(note).toContain('never clear a gate');
  });
  it('says the claimed subject only as a claim when the signature was not checked', () => {
    const note = declarationNote(unverified);
    expect(note).toContain('Claims mallory@evil.example; signature not checked or not by that signer.');
    expect(note).not.toContain('Signed by');
  });
  it('says the signature was not checked when the record claims no one', () => {
    expect(declarationNote({ declaration: { state: 'declared', signatureChecked: false } })).toContain('Signature not checked.');
  });
  it('counts the fields hidden from the viewer and names no namespace', () => {
    const note = declarationNote({ declaration: { ...checked.declaration, inferredFrom: undefined, inferredFromHidden: true }, inferredHidden: { fields: 4 } });
    expect(note).toContain('From a workload you may not read; 4 cluster fields left out.');
    expect(note).not.toContain('billing');
  });
  it('names only the hidden counts it has, in the singular for one', () => {
    const note = declarationNote({ ...checked, inferredHidden: { services: 1 } });
    expect(note).toContain('The cluster read could not see 1 service.');
    expect(note).not.toContain('callers');
    expect(declarationNote({ ...checked, inferredHidden: { callers: 1 } })).toContain('could not see 1 caller.');
  });
  it('words one hidden cluster field in the singular', () => {
    const note = declarationNote({ declaration: { ...checked.declaration, inferredFrom: undefined, inferredFromHidden: true }, inferredHidden: { fields: 1 } });
    expect(note).toContain('From a workload you may not read; 1 cluster field left out.');
  });
  it('names no count when the hidden workload left no cluster field to drop', () => {
    const note = declarationNote({ declaration: { ...unverified.declaration, inferredFrom: undefined, inferredFromHidden: true } });
    expect(note).toContain('From a workload you may not read.');
    expect(note).not.toContain('cluster fields left out');
  });
  it('counts the fields left out because they name a service the viewer may not read', () => {
    expect(declarationNote({ ...checked, inferredHidden: { fields: 2 } })).toContain('2 cluster fields left out: they name a service you may not read.');
  });
  it('says why a signer the installation does not allow is not accepted, and words the record as a claim', () => {
    const note = declarationNote(notAllowed);
    expect(note).toContain('Claims mallory@gmail.com; signature checked. Not accepted: the installation signer policy lists no issuer https://accounts.google.com.');
    expect(note).toContain('The record claims: Baseline 1.4.0.');
    expect(note).not.toContain('Signed by');
  });
  it('words what an unverified record says as a claim', () => {
    expect(declarationNote({ declaration: { ...unverified.declaration, accepted: ['dependencies[ledger] ^2.3.0'], draft: 'draft-3' } }))
      .toContain('The record claims: Accepted dependencies[ledger] ^2.3.0. Accepted draft draft-3.');
  });
  it('names a draft the declaration accepted', () => {
    expect(declarationNote({ declaration: { ...checked.declaration, draft: 'draft-3' } })).toContain('Accepted draft draft-3.');
  });
});

describe('declarationParts', () => {
  it('links the external system a claim came from', () => {
    const claim = { declaration: { ...checked.declaration, inferredFrom: 'external:aws/rds/eu-west-1/orders-db' } };
    expect(declarationParts(claim)).toEqual({ before: 'Declared by ana@acme.com from ', link: 'external aws/rds/eu-west-1/orders-db',
      href: '#/fleet/externals/aws%2Frds%2Feu-west-1%2Forders-db', after: '' });
    expect(declarationLine(claim)).toBe('Declared by ana@acme.com from external aws/rds/eu-west-1/orders-db');
  });
  it('links nothing for a workload, an unverified signer or none', () => {
    expect(declarationParts(checked).href).toBe('');
    expect(declarationParts({ declaration: { ...unverified.declaration, inferredFrom: 'external:stripe' } }).href).toBe('');
    expect(declarationParts({})).toEqual({ before: '', link: '', href: '', after: '' });
  });
});
