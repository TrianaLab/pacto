import { describe, it, expect } from 'vitest';
import { phaseTone, phaseLabel, provisioningTitle, type ProvisioningStatus } from './provisioning.ts';

const NOW = Date.parse('2026-11-06T12:00:00Z');
const st = (extra: Partial<ProvisioningStatus>): ProvisioningStatus => ({ phase: 'Unknown', provenance: 'observed', verified: false, ...extra });

describe('phaseTone', () => {
  it.each([
    ['Ready', 'ok'], ['Failed', 'err'], ['Requested', 'info'], ['Provisioning', 'info'], ['Deleting', 'info'], ['Unknown', 'neutral'], [undefined, 'neutral'],
  ])('%s is %s', (phase, tone) => expect(phaseTone(phase)).toBe(tone));
});

describe('phaseLabel', () => {
  const ready = { phase: 'Ready', reportedPhase: 'Ready', asOf: '2026-11-06T10:00:00Z', producer: 'platform-adapter', source: 'evidence' } as const;
  it('names the signed producer and source', () => {
    expect(phaseLabel(st({ ...ready, verified: true }), NOW)).toBe('Ready (reported by platform-adapter through evidence, 2 hours ago)');
  });
  it('says a target-state file is unsigned', () => {
    expect(phaseLabel(st({ ...ready, verified: false }), NOW)).toBe('Ready (unsigned target-state file, 2 hours ago)');
  });
  it('words every other phase the same way', () => {
    expect(phaseLabel(st({ ...ready, phase: 'Failed', verified: true }), NOW)).toBe('Failed (reported by platform-adapter through evidence, 2 hours ago)');
  });
  it('says why it is Unknown', () => {
    expect(phaseLabel(st({ reason: 'no provider has reported provisioning for this use' }))).toBe('Unknown: no provider has reported provisioning for this use');
    expect(phaseLabel(st({}))).toBe('Unknown: no provider has reported');
    expect(phaseLabel(st({ reportedPhase: 'Ready', stale: true, reason: 'the last report is older than the provisioning window' }))).toBe('Unknown: last reported Ready, stale');
    expect(phaseLabel(st({ reportedPhase: 'Ready', reason: 'last report is for 1.2.0; 1.3.0 has no report yet' }))).toBe('Unknown: last report is for 1.2.0; 1.3.0 has no report yet');
    expect(phaseLabel(st({ conflict: ['Failed', 'Ready'], reason: 'the providers that report on this use disagree' }))).toBe('Unknown: reporters disagree (Failed, Ready)');
    expect(phaseLabel(st({ reportedPhase: 'Ready', reason: 'the provider reported notobserved' }))).toBe('Unknown: the provider reported notobserved');
  });
});

describe('provisioningTitle', () => {
  it('carries the provider, externalRef, source kind, time and consumer revision', () => {
    const t = provisioningTitle(st({
      phase: 'Ready', verified: true, producer: 'p', source: 's', asOf: '2026-11-06T10:00:00Z', provider: 'postgresql',
      externalRef: 'db-orders-prod', sourceKind: 'evidence-http', revision: { kind: 'revision', key: 'k', label: 'orders 1.3.0', href: '' },
    }), NOW).split('\n');
    expect(t[0]).toBe('Ready (reported by p through s, 2 hours ago)');
    expect(t.slice(1, 4)).toEqual(['Provider: postgresql', 'External reference: db-orders-prod', 'Source: evidence-http']);
    expect(t[4]).toMatch(/^As of: /);
    expect(t[5]).toBe('Consumer revision: orders 1.3.0');
    expect(provisioningTitle(st({}))).toBe('Unknown: no provider has reported');
  });
});
