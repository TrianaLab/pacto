import { describe, it, expect } from 'vitest';
import {
  buildFleetGraph,
  layerAvailability,
  distinctValues,
  type FleetSnapshot,
} from './fleetGraph.ts';

// Two same-named services in different domains, one with a running target (so its
// declared dependency edge is "reconciled"), plus a stale target and an owner.
const snap: FleetSnapshot = {
  services: {
    'domain-a/payments': {
      key: 'domain-a/payments', name: 'payments', domain: 'domain-a', status: 'Compliant',
      owner: { team: 'core' }, revisions: ['domain-a/payments@sha256:a1'], targets: ['prod/k8s/pay-a'],
      sources: ['oci'],
    },
    'domain-b/payments': {
      key: 'domain-b/payments', name: 'payments', domain: 'domain-b', status: 'NonCompliant',
      owner: { team: 'risk' }, revisions: ['domain-b/payments@sha256:b1'], targets: [],
      sources: ['local'],
    },
    'domain-a/ledger': {
      key: 'domain-a/ledger', name: 'ledger', domain: 'domain-a', status: 'Compliant',
      revisions: ['domain-a/ledger@sha256:l1'], targets: ['prod/k8s/ledger-a'], sources: ['oci'],
    },
  },
  revisions: {
    'domain-a/payments@sha256:a1': { key: 'domain-a/payments@sha256:a1', serviceKey: 'domain-a/payments', service: 'payments', domain: 'domain-a', version: '2.0.0', valid: true },
    'domain-b/payments@sha256:b1': { key: 'domain-b/payments@sha256:b1', serviceKey: 'domain-b/payments', service: 'payments', domain: 'domain-b', version: '1.0.0', valid: false },
    'domain-a/ledger@sha256:l1': { key: 'domain-a/ledger@sha256:l1', serviceKey: 'domain-a/ledger', service: 'ledger', domain: 'domain-a', version: '3.1.0', valid: true },
  },
  targets: {
    'prod/k8s/pay-a': { key: 'prod/k8s/pay-a', serviceKey: 'domain-a/payments', service: 'payments', domain: 'domain-a', name: 'pay-a', scope: 'prod', compliance: 'Compliant', contractRevision: 'domain-a/payments@sha256:a1', stale: false },
    'prod/k8s/ledger-a': { key: 'prod/k8s/ledger-a', serviceKey: 'domain-a/ledger', service: 'ledger', domain: 'domain-a', name: 'ledger-a', scope: 'prod', compliance: 'Compliant', contractRevision: 'domain-a/ledger@sha256:l1', stale: true },
  },
  relationships: [
    // domain-a/payments depends on domain-a/ledger, resolved + ledger has a target → reconciled.
    { fromService: 'domain-a/payments', fromRevision: 'domain-a/payments@sha256:a1', toService: 'domain-a/ledger', resolvedRevision: 'domain-a/ledger@sha256:l1', type: 'dependency', provenance: 'declared', required: true, resolved: true },
    // domain-b/payments depends on domain-a/ledger too — declared, resolved, ledger has a target → reconciled.
    { fromService: 'domain-b/payments', fromRevision: 'domain-b/payments@sha256:b1', toService: 'domain-a/ledger', resolvedRevision: 'domain-a/ledger@sha256:l1', type: 'dependency', provenance: 'declared', required: false, resolved: true },
  ],
};

describe('buildFleetGraph — service perspective', () => {
  it('keys nodes by ServiceKey so same-named cross-domain services stay distinct', () => {
    const g = buildFleetGraph(snap, 'service', 'declared');
    const ids = g.nodes.map((n) => n.id).sort();
    expect(ids).toEqual(['domain-a/ledger', 'domain-a/payments', 'domain-b/payments']);
    // Both payments nodes carry the same display name but distinct ids.
    const payments = g.nodes.filter((n) => n.serviceName === 'payments');
    expect(payments).toHaveLength(2);
  });

  it('declared layer includes every declared dependency edge', () => {
    const g = buildFleetGraph(snap, 'service', 'declared');
    const from = g.nodes.find((n) => n.id === 'domain-a/payments')!;
    expect(from.edges).toEqual([{ targetId: 'domain-a/ledger', required: true, type: 'dependency' }]);
  });

  it('domain filter isolates one domain (no cross-contamination)', () => {
    const g = buildFleetGraph(snap, 'service', 'all', { domain: 'domain-b' });
    expect(g.nodes.map((n) => n.id)).toEqual(['domain-b/payments']);
    // The edge to domain-a/ledger is dropped because the target node is filtered out.
    expect(g.nodes[0].edges).toEqual([]);
  });

  it('status filter keeps only matching services', () => {
    const g = buildFleetGraph(snap, 'service', 'all', { status: 'NonCompliant' });
    expect(g.nodes.map((n) => n.id)).toEqual(['domain-b/payments']);
  });

  it('owner filter matches the owner key', () => {
    const g = buildFleetGraph(snap, 'service', 'all', { owner: 'risk' });
    expect(g.nodes.map((n) => n.id)).toEqual(['domain-b/payments']);
  });

  it('source and scope filters apply', () => {
    expect(buildFleetGraph(snap, 'service', 'all', { source: 'local' }).nodes.map((n) => n.id)).toEqual(['domain-b/payments']);
    // scope=prod keeps only services with a prod target.
    expect(buildFleetGraph(snap, 'service', 'all', { scope: 'prod' }).nodes.map((n) => n.id).sort())
      .toEqual(['domain-a/ledger', 'domain-a/payments']);
  });

  it('freshness filter keeps stale vs fresh targets', () => {
    expect(buildFleetGraph(snap, 'service', 'all', { freshness: 'stale' }).nodes.map((n) => n.id)).toEqual(['domain-a/ledger']);
    expect(buildFleetGraph(snap, 'service', 'all', { freshness: 'fresh' }).nodes.map((n) => n.id)).toEqual(['domain-a/payments']);
  });
});

describe('buildFleetGraph — layers', () => {
  it('reconciled layer keeps only edges whose target actually runs', () => {
    const g = buildFleetGraph(snap, 'service', 'reconciled');
    const from = g.nodes.find((n) => n.id === 'domain-a/payments')!;
    expect(from.edges).toHaveLength(1); // ledger runs → reconciled
  });

  it('observed layer is empty when the snapshot has no observed edges', () => {
    const g = buildFleetGraph(snap, 'service', 'observed');
    expect(g.nodes.every((n) => (n.edges || []).length === 0)).toBe(true);
  });

  it('layerAvailability reports declared+reconciled but not observed', () => {
    expect(layerAvailability(snap)).toEqual({ declared: true, observed: false, reconciled: true });
    expect(layerAvailability(null)).toEqual({ declared: false, observed: false, reconciled: false });
  });

  it('observed becomes available when an observed edge is present', () => {
    const withObs: FleetSnapshot = {
      ...snap,
      relationships: [...snap.relationships!, { fromService: 'domain-a/ledger', toService: 'domain-a/payments', type: 'dependency', provenance: 'observed', resolved: true }],
    };
    expect(layerAvailability(withObs).observed).toBe(true);
    const g = buildFleetGraph(withObs, 'service', 'observed');
    expect(g.nodes.find((n) => n.id === 'domain-a/ledger')!.edges).toHaveLength(1);
  });
});

describe('buildFleetGraph — revision & target perspectives', () => {
  it('revision perspective keys nodes by RevisionKey and links resolved revisions', () => {
    const g = buildFleetGraph(snap, 'revision', 'declared');
    expect(g.nodes.map((n) => n.id).sort()).toEqual([
      'domain-a/ledger@sha256:l1', 'domain-a/payments@sha256:a1', 'domain-b/payments@sha256:b1',
    ]);
    const from = g.nodes.find((n) => n.id === 'domain-a/payments@sha256:a1')!;
    expect(from.edges).toEqual([{ targetId: 'domain-a/ledger@sha256:l1', required: true, type: 'dependency' }]);
    // An invalid revision is surfaced as NonCompliant.
    expect(g.nodes.find((n) => n.id === 'domain-b/payments@sha256:b1')!.status).toBe('NonCompliant');
  });

  it('target perspective keys nodes by TargetKey and links running dependencies', () => {
    const g = buildFleetGraph(snap, 'target', 'reconciled');
    expect(g.nodes.map((n) => n.id).sort()).toEqual(['prod/k8s/ledger-a', 'prod/k8s/pay-a']);
    // pay-a → ledger-a (payments depends on ledger, both run).
    expect(g.nodes.find((n) => n.id === 'prod/k8s/pay-a')!.edges).toEqual([{ targetId: 'prod/k8s/ledger-a', required: true, type: 'dependency' }]);
    // A stale target is flagged.
    expect(g.nodes.find((n) => n.id === 'prod/k8s/ledger-a')!.reason).toBe('not_found');
  });
});

describe('buildFleetGraph — edge cases', () => {
  it('returns an empty graph for a null snapshot', () => {
    expect(buildFleetGraph(null, 'service', 'all').nodes).toEqual([]);
  });
});

describe('distinctValues', () => {
  it('collects sorted distinct filter options from the snapshot', () => {
    expect(distinctValues(snap)).toEqual({
      domains: ['domain-a', 'domain-b'],
      scopes: ['prod'],
      owners: ['core', 'risk'],
      statuses: ['Compliant', 'NonCompliant'],
      sources: ['local', 'oci'],
    });
    expect(distinctValues(null).domains).toEqual([]);
  });
});
