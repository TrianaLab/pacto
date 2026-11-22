import { describe, it, expect } from 'vitest';
import { replacementLine, fieldLabel, fieldProvenanceText } from './fieldProvenance.ts';

describe('replacementLine', () => {
  it('reads like pacto fleet get for each link', () => {
    expect(replacementLine({ from: 'k8s:billing%2FDeployment%2Finvoices', link: 'inferred-from', by: 'ana@acme.example', revision: 'invoices@0.1.0' }))
      .toBe('Declared by ana@acme.example from inferred k8s:billing/Deployment/invoices (revision invoices@0.1.0)');
    expect(replacementLine({ from: 'k8s:prod%2FDeployment%2Fa', link: 'pacto-target', target: 'prod/kubernetes/a', at: '2026-11-17T11:56:00Z' }, '2026-11-17T12:00:00Z'))
      .toBe('Replaces k8s:prod/Deployment/a 4 minutes ago (linked by Pacto target prod/kubernetes/a)');
    expect(replacementLine({ from: 'k8s:prod%2FDeployment%2Fa', link: 'pacto-target' })).toBe('Replaces k8s:prod/Deployment/a (linked by Pacto target of this service)');
    expect(replacementLine({ from: 'k8s:shop%2FDeployment%2Fpayments', to: 'shop/payments', link: 'observed', signal: 'image-digest', value: `sha256:ab12${'0'.repeat(60)}`, at: '2026-11-17T11:56:00Z' }, '2026-11-17T12:00:00Z'))
      .toBe('k8s:shop/Deployment/payments is linked to shop/payments by its running image digest sha256:ab1200000000… (observed, 4 minutes ago)');
    expect(replacementLine({ from: 'k8s:shop%2FDeployment%2Fcart', link: 'observed', signal: 'annotation', value: 'oci://ghcr.io/acme/cart:1.0.0' }))
      .toBe('k8s:shop/Deployment/cart is linked to this service by its pacto.trianalab.io/contract annotation (observed)');
  });
});

describe('fieldLabel', () => {
  it('shortens the owner and a dependency range, and keeps any other path', () => {
    expect(fieldLabel('service.owner')).toBe('owner');
    expect(fieldLabel('dependencies[ledger].compatibility')).toBe('ledger range');
    expect(fieldLabel('interfaces[0].port')).toBe('interfaces[0].port');
  });
});

describe('fieldProvenanceText', () => {
  it('says where each value comes from and never calls a proposal declared', () => {
    const asOf = '2026-11-17T11:56:00Z';
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'declared', source: 'pacto.yaml of invoices@0.1.0', asOf }))
      .toMatch(/^Declared in pacto\.yaml of invoices@0\.1\.0, read .+$/);
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'declared' })).toBe('Declared');
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'declared', source: 'Pacto target' })).toBe('Declared by the contract a running Pacto target reports');
    expect(fieldProvenanceText({ field: 'dependencies[ledger].compatibility', provenance: 'accepted', by: 'ana@acme.example', source: 'pacto.yaml of invoices@0.1.0' }))
      .toBe('Accepted by ana@acme.example in invoices@0.1.0');
    expect(fieldProvenanceText({ field: 'x', provenance: 'accepted' })).toBe('Accepted by an unknown signer in its revision');
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'attested' })).toBe('Attested by the repository');
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'attested', source: 'CODEOWNERS' })).toBe('Attested by the repository: read from CODEOWNERS');
    expect(fieldProvenanceText({ field: 'interfaces[0].port', provenance: 'inferred', source: 'cluster:manifest k8s:billing/Deployment/invoices', asOf, completeness: 'complete' }))
      .toMatch(/^Inferred from cluster:manifest k8s:billing\/Deployment\/invoices as of .+, complete$/);
    expect(fieldProvenanceText({ field: 'x', provenance: 'inferred' })).toBe('Inferred from an unknown source');
    expect(fieldProvenanceText({ field: 'dependencies[audit].compatibility', provenance: 'declared', proposed: '^1.4.0', source: 'cluster:runtime k8s:billing/Deployment/invoices', completeness: 'partial' }))
      .toBe('Proposed ^1.4.0 from runtime calls, partial; never declared');
    expect(fieldProvenanceText({ field: 'd', provenance: 'declared', proposed: '^1', source: 'cluster:manifest x' })).toBe('Proposed ^1 from manifest; never declared');
    expect(fieldProvenanceText({ field: 'd', provenance: 'declared', proposed: '^1' })).toBe('Proposed ^1 from an unknown source; never declared');
  });

  it('dates every row that carries asOf, a proposal first', () => {
    const asOf = '2026-08-01T00:00:00Z';
    expect(fieldProvenanceText({ field: 'd', provenance: 'declared', proposed: '^1.4.0', source: 'cluster:fleet x', asOf, completeness: 'partial' }))
      .toMatch(/^Proposed \^1\.4\.0 from fleet as of .+, partial; never declared$/);
    expect(fieldProvenanceText({ field: 'x', provenance: 'accepted', by: 'ana', source: 'pacto.yaml of invoices@0.1.0', asOf })).toMatch(/^Accepted by ana in invoices@0\.1\.0 as of .+$/);
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'attested', source: 'CODEOWNERS', asOf })).toMatch(/^Attested by the repository: read from CODEOWNERS as of .+$/);
    expect(fieldProvenanceText({ field: 'service.owner', provenance: 'declared', source: 'Pacto target', asOf })).toMatch(/^Declared by the contract a running Pacto target reports as of .+$/);
  });
});
