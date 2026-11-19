import { describe, it, expect } from 'vitest';
import { declarationLine, declarationNote } from './declaration.ts';

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
  it('names a draft the declaration accepted', () => {
    expect(declarationNote({ declaration: { ...checked.declaration, draft: 'draft-3' } })).toContain('Accepted draft draft-3.');
  });
});
